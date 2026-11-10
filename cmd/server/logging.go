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

package main

import (
	"context"
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

// Log file names. Each log stream has its own file.
const (
	defaultLogDir     = "repository/logs"
	appLogFileName    = "cds.log"
	accessLogFileName = "http_access.log"
	auditLogFileName  = "audit.log"
)

// logStreams holds the log streams that the server owns.
type logStreams struct {
	// access is the access logger, or nil when the access log is off.
	access *log.Logger
}

// setupLogging configures the application log, the access log, and the audit log from the
// deployment configuration.
func setupLogging(cdsHome string, cfg config.LogConfig) (*logStreams, error) {

	if err := log.Configure(log.Options{
		Level:  cfg.LogLevel,
		Format: cfg.Format,
		Output: logOutput(cdsHome, cfg, appLogFileName),
	}); err != nil {
		return nil, err
	}

	streams := &logStreams{}
	if enabledByDefault(cfg.Access.Enabled) {
		accessLogger, err := log.New(log.TypeAccess, log.Options{
			Format: cfg.Format,
			Output: logOutput(cdsHome, cfg, accessLogFileName),
		})
		if err != nil {
			return nil, err
		}
		streams.access = accessLogger
	}

	if err := audit.Init(audit.Options{
		Enabled:    enabledByDefault(cfg.Audit.Enabled),
		ReadEvents: cfg.Audit.ReadEvents,
		Output:     logOutput(cdsHome, cfg, auditLogFileName),
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

// logOutput returns the destinations of the log stream that writes to fileName.
func logOutput(cdsHome string, cfg config.LogConfig, fileName string) log.OutputOptions {

	output := log.OutputOptions{Console: enabledByDefault(cfg.Console)}
	if !cfg.File.Enabled {
		return output
	}
	dir := cfg.File.Dir
	if dir == "" {
		dir = defaultLogDir
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cdsHome, dir)
	}
	output.File = &rollingfile.Config{
		Path:       filepath.Join(dir, fileName),
		MaxSizeMB:  cfg.File.MaxSizeMB,
		MaxBackups: cfg.File.MaxBackups,
	}
	return output
}

// enabledByDefault returns the value of an optional switch that is on when it is not configured.
func enabledByDefault(value *bool) bool {

	return value == nil || *value
}

// serverErrorWriter sends the errors of the HTTP server, such as a failed TLS handshake, to the
// application log.
type serverErrorWriter struct{}

func (serverErrorWriter) Write(p []byte) (int, error) {

	log.FromContext(context.Background()).WithComponent("HTTPServer").Warn(strings.TrimSpace(string(p)))
	return len(p), nil
}

// newServerErrorLog returns the error logger of the HTTP server.
func newServerErrorLog() *stdlog.Logger {

	return stdlog.New(serverErrorWriter{}, "", 0)
}
