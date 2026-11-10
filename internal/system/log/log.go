/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

// Package log is the logging framework of CDS. It gives every component the same structured
// logger, the same field names, and the same output rules.
//
// CDS writes three log streams. Each record carries a log_type field that names its stream:
//
//   - app: the application log, written through GetLogger.
//   - access: one record for each HTTP request, written by the access log middleware.
//   - audit: one record for each audited operation, written by the audit package.
//
// A component gets its logger as follows:
//
//	logger := log.GetLogger().WithComponent("ProfileService")
//	logger.WithContext(ctx).Info("Profile created.", log.String(log.KeyProfileID, id))
//
// WithContext adds the correlation ID and the organization of the request to the record.
package log

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/log/rollingfile"
	"github.com/wso2/identity-customer-data-service/internal/system/reqctx"
)

// Log stream names, the values of the log_type field.
const (
	TypeApp    = "app"
	TypeAccess = "access"
	TypeAudit  = "audit"
)

// Record formats.
const (
	FormatText = "text"
	FormatJSON = "json"
)

// TimeLayout is the timestamp layout of all log streams: UTC, with milliseconds.
const TimeLayout = "2006-01-02T15:04:05.000Z"

const defaultLevel = "INFO"

// OutputOptions selects where a log stream goes. With no destination enabled, the stream goes to
// the console, so that a configuration error cannot silence it.
type OutputOptions struct {
	// Console writes the records to standard output.
	Console bool
	// File writes the records to a rotating file, when it is not nil.
	File *rollingfile.Config
}

// Options configures a log stream.
type Options struct {
	// Level is the minimum level: DEBUG, INFO, WARN, or ERROR. Empty means INFO.
	Level string
	// Format is FormatText or FormatJSON. Empty means FormatText.
	Format string
	// Output selects the destinations.
	Output OutputOptions
}

// core is the state that a logger shares with all loggers derived from it. Configure replaces the
// handler, and every derived logger follows.
type core struct {
	handler atomic.Pointer[handlerRef]
	level   *slog.LevelVar

	mu      sync.Mutex
	closer  io.Closer
	logType string
}

type handlerRef struct {
	handler slog.Handler
}

// Logger is a structured logger. Its methods are safe for concurrent use.
type Logger struct {
	internal *slog.Logger
	core     *core
	ctx      context.Context
}

var (
	logger   *Logger
	loggerMu sync.RWMutex
)

// GetLogger returns the application logger. Before Init or Configure runs, it returns a logger
// that writes text records at INFO level to the console.
func GetLogger() *Logger {
	loggerMu.RLock()
	l := logger
	loggerMu.RUnlock()
	if l != nil {
		return l
	}

	loggerMu.Lock()
	defer loggerMu.Unlock()
	if logger == nil {
		logger, _ = New(TypeApp, Options{Output: OutputOptions{Console: true}})
	}
	return logger
}

// FromContext returns the application logger with the correlation ID and the organization of ctx.
func FromContext(ctx context.Context) *Logger {
	return GetLogger().WithContext(ctx)
}

// Init sets the level of the application logger and sends its text records to the console.
func Init(logLevel string) error {
	return Configure(Options{Level: logLevel, Output: OutputOptions{Console: true}})
}

// Configure applies opts to the application logger. Loggers that components derived earlier also
// use the new configuration.
func Configure(opts Options) error {
	return GetLogger().configure(TypeApp, opts)
}

// Close releases the log file of the application logger. Call it at shutdown.
func Close() error {
	return GetLogger().Close()
}

// New creates the logger of a log stream. logType is the value of its log_type field.
func New(logType string, opts Options) (*Logger, error) {
	c := &core{level: new(slog.LevelVar)}
	l := &Logger{internal: slog.New(&dynamicHandler{core: c}), core: c}
	if err := l.configure(logType, opts); err != nil {
		return nil, err
	}
	return l, nil
}

// configure builds the output handler from opts and installs it.
func (l *Logger) configure(logType string, opts Options) error {
	level, err := parseLogLevel(opts.Level)
	if err != nil {
		return fmt.Errorf("error parsing log level: %w", err)
	}
	if opts.Format != "" && !strings.EqualFold(opts.Format, FormatText) &&
		!strings.EqualFold(opts.Format, FormatJSON) {
		return fmt.Errorf("unsupported log format: %s", opts.Format)
	}
	out, closer, err := NewOutput(opts.Output)
	if err != nil {
		return err
	}

	handlerOptions := &slog.HandlerOptions{Level: l.core.level, ReplaceAttr: replaceAttr}
	var handler slog.Handler
	if strings.EqualFold(opts.Format, FormatJSON) {
		handler = slog.NewJSONHandler(out, handlerOptions)
	} else {
		handler = slog.NewTextHandler(out, handlerOptions)
	}
	handler = handler.WithAttrs([]slog.Attr{slog.String(KeyLogType, logType)})

	l.core.mu.Lock()
	defer l.core.mu.Unlock()
	previous := l.core.closer
	l.core.level.Set(level)
	l.core.logType = logType
	l.core.handler.Store(&handlerRef{handler: handler})
	l.core.closer = closer
	if previous != nil {
		_ = previous.Close()
	}
	return nil
}

// Close releases the log file of the logger, if it has one.
func (l *Logger) Close() error {
	l.core.mu.Lock()
	defer l.core.mu.Unlock()
	if l.core.closer == nil {
		return nil
	}
	err := l.core.closer.Close()
	l.core.closer = nil
	// Continue on the console, so that records after Close are not lost.
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: l.core.level, ReplaceAttr: replaceAttr})
	l.core.handler.Store(&handlerRef{
		handler: handler.WithAttrs([]slog.Attr{slog.String(KeyLogType, l.core.logType)}),
	})
	return err
}

// NewOutput opens the destinations of opts and returns one writer for them. The closer is nil when
// there is no file to close.
func NewOutput(opts OutputOptions) (io.Writer, io.Closer, error) {
	if opts.File == nil {
		return os.Stdout, nil, nil
	}
	file, err := rollingfile.New(*opts.File)
	if err != nil {
		return nil, nil, err
	}
	if opts.Console {
		return io.MultiWriter(os.Stdout, file), file, nil
	}
	return file, file, nil
}

// FileOutput returns the file settings of a log file at path, with the default rotation limits.
func FileOutput(path string) *rollingfile.Config {
	return &rollingfile.Config{Path: path}
}

// With returns a logger that adds fields to every record.
func (l *Logger) With(fields ...Field) *Logger {
	return &Logger{internal: l.internal.With(convertFields(fields)...), core: l.core, ctx: l.ctx}
}

// WithComponent returns a logger that names the component in every record.
func (l *Logger) WithComponent(name string) *Logger {
	return l.With(String(KeyComponent, name))
}

// WithContext returns a logger that adds the correlation ID and the organization of ctx to every
// record.
func (l *Logger) WithContext(ctx context.Context) *Logger {
	return &Logger{internal: l.internal, core: l.core, ctx: ctx}
}

// IsDebugEnabled reports whether the logger writes DEBUG records. Use it to skip the work of an
// expensive debug message.
func (l *Logger) IsDebugEnabled() bool {
	return l.core.level.Level() <= slog.LevelDebug
}

// Info logs an informational message with custom fields.
func (l *Logger) Info(msg string, fields ...Field) {
	l.log(slog.LevelInfo, msg, fields)
}

// Debug logs a debug message with custom fields.
func (l *Logger) Debug(msg string, fields ...Field) {
	l.log(slog.LevelDebug, msg, fields)
}

// Warn logs a warning message with custom fields.
func (l *Logger) Warn(msg string, fields ...Field) {
	l.log(slog.LevelWarn, msg, fields)
}

// Error logs an error message with custom fields.
func (l *Logger) Error(msg string, fields ...Field) {
	l.log(slog.LevelError, msg, fields)
}

// Fatal logs a fatal message with custom fields and exits the application.
func (l *Logger) Fatal(msg string, fields ...Field) {
	l.log(slog.LevelError, msg, fields)
	_ = l.Close()
	os.Exit(1)
}

func (l *Logger) log(level slog.Level, msg string, fields []Field) {
	ctx := l.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if !l.internal.Enabled(ctx, level) {
		return
	}
	l.internal.Log(ctx, level, msg, convertFields(fields)...)
}

// dynamicHandler is the handler of every Logger. It adds the fields of the logger and of the
// request context, then delegates to the current output handler of the core.
type dynamicHandler struct {
	core  *core
	attrs []slog.Attr
}

var _ slog.Handler = (*dynamicHandler)(nil)

func (h *dynamicHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.core.level.Level()
}

func (h *dynamicHandler) Handle(ctx context.Context, record slog.Record) error {
	record.AddAttrs(h.attrs...)
	if info := reqctx.From(ctx); info != nil {
		if id := info.CorrelationID(); id != "" {
			record.AddAttrs(slog.String(KeyCorrelationID, id))
		}
		if org, _ := info.Org(); org != "" {
			record.AddAttrs(slog.String(KeyOrg, org))
		}
	}
	return h.core.handler.Load().handler.Handle(ctx, record)
}

func (h *dynamicHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	// Copy, so that loggers derived from the same parent do not share a backing array.
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &dynamicHandler{core: h.core, attrs: merged}
}

// WithGroup is not supported. CDS records are flat, so that all streams use the same field names.
func (h *dynamicHandler) WithGroup(string) slog.Handler {
	return h
}

// replaceAttr writes the timestamp of every stream in UTC with milliseconds.
func replaceAttr(groups []string, attr slog.Attr) slog.Attr {
	if len(groups) == 0 && attr.Key == slog.TimeKey {
		if t, ok := attr.Value.Any().(time.Time); ok {
			return slog.String(slog.TimeKey, t.UTC().Format(TimeLayout))
		}
	}
	return attr
}

// parseLogLevel parses the log level string and returns the corresponding slog.Level.
func parseLogLevel(logLevel string) (slog.Level, error) {
	if strings.TrimSpace(logLevel) == "" {
		logLevel = defaultLevel
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(logLevel)); err != nil {
		return slog.LevelError, errors.New("unknown log level: " + logLevel)
	}
	return level, nil
}

// convertFields converts a slice of Field to a variadic list of slog.Attr.
func convertFields(fields []Field) []any {
	attrs := make([]any, len(fields))
	for i, field := range fields {
		attrs[i] = slog.Any(field.Key, field.Value)
	}
	return attrs
}
