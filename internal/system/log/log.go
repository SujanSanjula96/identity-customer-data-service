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

// Package log provides the structured logger of CDS, a wrapper around slog.
package log

import (
	"fmt"
	"log/slog"
	"os"
)

var (
	logger *Logger
)

// Logger is a wrapper around the slog logger.
type Logger struct {
	internal *slog.Logger
}

// GetLogger creates and returns a singleton instance of the logger.
func GetLogger() *Logger {
	return logger
}

// Init initializes the slog logger with the given log level string.
func Init(logLevel string) error {
	level, err := parseLogLevel(logLevel)
	if err != nil {
		return fmt.Errorf("error parsing log level: %w", err)
	}

	handlerOptions := &slog.HandlerOptions{Level: level}
	logHandler := slog.NewTextHandler(os.Stdout, handlerOptions)

	logger = &Logger{
		internal: slog.New(logHandler),
	}
	return nil
}

// With creates a new logger instance with additional fields.
func (l *Logger) With(fields ...Field) *Logger {
	return &Logger{
		internal: l.internal.With(convertFields(fields)...),
	}
}

// Info logs an informational message with custom fields.
func (l *Logger) Info(msg string, fields ...Field) {
	l.internal.Info(msg, convertFields(fields)...)
}

// Debug logs a debug message with custom fields.
func (l *Logger) Debug(msg string, fields ...Field) {
	l.internal.Debug(msg, convertFields(fields)...)
}

// Warn logs a warning message with custom fields.
func (l *Logger) Warn(msg string, fields ...Field) {
	l.internal.Warn(msg, convertFields(fields)...)
}

// Error logs an error message with custom fields.
func (l *Logger) Error(msg string, fields ...Field) {
	l.internal.Error(msg, convertFields(fields)...)
}

// Fatal logs a fatal message with custom fields and exits the application.
func (l *Logger) Fatal(msg string, fields ...Field) {
	l.internal.Error(msg, convertFields(fields)...)
	os.Exit(1)
}

// parseLogLevel parses the log level string and returns the corresponding slog.Level.
func parseLogLevel(logLevel string) (slog.Level, error) {
	var level slog.Level
	var err = level.UnmarshalText([]byte(logLevel))
	if err != nil {
		return slog.LevelError, err
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
