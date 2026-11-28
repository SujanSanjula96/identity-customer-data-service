/*
 * Copyright (c) (2025-2026), Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
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

package server

import (
	"context"
	"errors"
	"fmt"
	stdlog "log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/system/audit"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/httpmw"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/log/rollingfile"
)

// Built-in defaults of the log outputs. Each log type has its own file.
const (
	defaultLogDir        = "repository/logs"
	defaultConsoleFormat = log.FormatText
	defaultFileFormat    = log.FormatJSON
	appLogFileName       = "cds.log"
	accessLogFileName    = "http_access.log"
	auditLogFileName     = "audit.log"
)

// logStreams holds the log streams that the server owns.
type logStreams struct {
	// access is the access logger, or nil when the access log is off.
	access *log.Logger
}

// setupLogging configures the application log, the access log, and the audit log from the
// deployment configuration.
func setupLogging(cdsHome string, cfg config.LogConfig) (*logStreams, error) {
	if err := validateLogConfig(cfg); err != nil {
		return nil, err
	}
	if err := log.Configure(log.Options{
		Level:  appLevel(cfg),
		Output: logOutput(cdsHome, cfg.Outputs, cfg.App.Outputs, appLogFileName),
	}); err != nil {
		return nil, err
	}

	streams := &logStreams{}
	if enabledByDefault(cfg.Access.Enabled) {
		accessLogger, err := log.New(log.TypeAccess, log.Options{
			Output: logOutput(cdsHome, cfg.Outputs, cfg.Access.Outputs, accessLogFileName),
		})
		if err != nil {
			return nil, err
		}
		streams.access = accessLogger
	}

	if err := audit.Init(audit.Options{
		Enabled: enabledByDefault(cfg.Audit.Enabled),
		Output:  logOutput(cdsHome, cfg.Outputs, cfg.Audit.Outputs, auditLogFileName),
	}); err != nil {
		return nil, err
	}
	return streams, nil
}

// wrap puts the request middleware around handler. The request context is the outer layer, so
// that every later layer has the correlation ID.
func (s *logStreams) wrap(handler http.Handler) http.Handler {
	if s.access != nil {
		handler = httpmw.AccessLog(s.access, handler)
	}
	return httpmw.RequestContext(handler)
}

// close releases the log files. The application log closes last, so that it can report an error
// of the other streams.
func (s *logStreams) close() {
	if err := audit.Close(); err != nil {
		log.GetLogger().Error("Failed to close the audit log.", log.Error(err))
	}
	if s.access != nil {
		if err := s.access.Close(); err != nil {
			log.GetLogger().Error("Failed to close the access log.", log.Error(err))
		}
	}
	_ = log.Close()
}

// appLevel returns the level of the application log. log.app.level takes precedence over the older
// key log.log_level.
func appLevel(cfg config.LogConfig) string {
	return stringOf(cfg.LogLevel, cfg.App.Level)
}

// validateLogConfig refuses the keys that are not valid where they are set.
func validateLogConfig(cfg config.LogConfig) error {
	if cfg.Outputs.File.Name != "" {
		return errors.New("log.outputs.file.name is not valid: set the file name in the block of a log type")
	}
	for logType, name := range map[string]string{
		"app":    cfg.App.Outputs.File.Name,
		"access": cfg.Access.Outputs.File.Name,
		"audit":  cfg.Audit.Outputs.File.Name,
	} {
		if name != "" && (filepath.Base(name) != name || name == "." || name == "..") {
			return fmt.Errorf("log.%s.outputs.file.name must be a file name, not a path: %s", logType, name)
		}
	}
	for output, format := range map[string]string{
		"console": cfg.Audit.Outputs.Console.Format,
		"file":    cfg.Audit.Outputs.File.Format,
	} {
		if format != "" && !strings.EqualFold(strings.TrimSpace(format), log.FormatJSON) {
			return fmt.Errorf("log.audit.outputs.%s.format must be json: the audit log is always JSON", output)
		}
	}
	return nil
}

// logOutput returns the outputs of one log type. Each key comes from the block of the log type
// (own), then from log.outputs (defaults), then from the built-in default.
func logOutput(cdsHome string, defaults, own config.LogOutputsConfig, defaultFileName string) log.OutputOptions {
	output := log.OutputOptions{
		Console:       boolOf(true, own.Console.Enabled, defaults.Console.Enabled),
		ConsoleFormat: stringOf(defaultConsoleFormat, own.Console.Format, defaults.Console.Format),
		FileFormat:    stringOf(defaultFileFormat, own.File.Format, defaults.File.Format),
	}
	if !boolOf(false, own.File.Enabled, defaults.File.Enabled) {
		return output
	}
	dir := stringOf(defaultLogDir, own.File.Dir, defaults.File.Dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cdsHome, dir)
	}
	output.File = &rollingfile.Config{
		Path:       filepath.Join(dir, stringOf(defaultFileName, own.File.Name)),
		MaxSizeMB:  intOf(own.File.MaxSizeMB, defaults.File.MaxSizeMB),
		MaxBackups: intOf(own.File.MaxBackups, defaults.File.MaxBackups),
	}
	return output
}

// boolOf returns the first value that is set, else fallback.
func boolOf(fallback bool, values ...*bool) bool {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return fallback
}

// stringOf returns the first value that is not empty, else fallback.
func stringOf(fallback string, values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return fallback
}

// intOf returns the first value that is not zero. Zero means the default of the rolling file.
func intOf(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

// enabledByDefault returns the value of an optional switch that is on when it is not configured.
func enabledByDefault(value *bool) bool {
	return value == nil || *value
}

// serverErrorWriter sends the errors of the HTTP server, such as a failed TLS handshake, to the
// application log.
type serverErrorWriter struct{}

func (serverErrorWriter) Write(p []byte) (int, error) {
	// The HTTP server reports failures here, for example a recovered handler panic with its stack.
	// ERROR keeps them when the log level is ERROR.
	log.FromContext(context.Background()).WithComponent("HTTPServer").Error(strings.TrimSpace(string(p)))
	return len(p), nil
}

// newServerErrorLog returns the error logger of the HTTP server.
func newServerErrorLog() *stdlog.Logger {
	return stdlog.New(serverErrorWriter{}, "", 0)
}
