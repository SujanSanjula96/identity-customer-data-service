/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

func boolPtr(value bool) *bool {
	return &value
}

func TestLogOutputUsesBuiltInDefaults(t *testing.T) {
	output := logOutput("/cds", config.LogOutputsConfig{}, config.LogOutputsConfig{}, auditLogFileName)

	assert.True(t, output.Console)
	assert.Equal(t, log.FormatText, output.ConsoleFormat)
	assert.Equal(t, log.FormatJSON, output.FileFormat)
	assert.Nil(t, output.File, "file output is off by default")
}

func TestLogOutputTakesTypeValueBeforeDefault(t *testing.T) {
	defaults := config.LogOutputsConfig{
		Console: config.LogConsoleConfig{Format: log.FormatJSON},
		File: config.LogFileConfig{
			Enabled:    boolPtr(true),
			Dir:        "logs",
			MaxSizeMB:  5,
			MaxBackups: 3,
		},
	}
	own := config.LogOutputsConfig{
		Console: config.LogConsoleConfig{Enabled: boolPtr(false)},
		File:    config.LogFileConfig{Name: "audit-trail.log", MaxBackups: 90},
	}

	cdsHome := t.TempDir()
	output := logOutput(cdsHome, defaults, own, auditLogFileName)

	assert.False(t, output.Console, "the value of the log type wins")
	assert.Equal(t, log.FormatJSON, output.ConsoleFormat, "the default block fills a key that the type does not set")
	require.NotNil(t, output.File)
	assert.Equal(t, filepath.Join(cdsHome, "logs", "audit-trail.log"), output.File.Path)
	assert.Equal(t, 5, output.File.MaxSizeMB)
	assert.Equal(t, 90, output.File.MaxBackups)
}

func TestLogOutputLetsTypeTurnOffDefaultFile(t *testing.T) {
	defaults := config.LogOutputsConfig{File: config.LogFileConfig{Enabled: boolPtr(true)}}
	own := config.LogOutputsConfig{File: config.LogFileConfig{Enabled: boolPtr(false)}}

	output := logOutput("/cds", defaults, own, appLogFileName)

	assert.Nil(t, output.File)
}

func TestLogOutputKeepsAbsoluteDir(t *testing.T) {
	logDir := t.TempDir()
	own := config.LogOutputsConfig{File: config.LogFileConfig{Enabled: boolPtr(true), Dir: logDir}}

	output := logOutput(t.TempDir(), config.LogOutputsConfig{}, own, accessLogFileName)

	require.NotNil(t, output.File)
	assert.Equal(t, filepath.Join(logDir, accessLogFileName), output.File.Path)
}

func TestAppLevelPrefersNewKey(t *testing.T) {
	assert.Equal(t, "DEBUG", appLevel(config.LogConfig{LogLevel: "DEBUG"}))
	assert.Equal(t, "WARN", appLevel(config.LogConfig{LogLevel: "DEBUG", App: config.AppLogConfig{Level: "WARN"}}))
	assert.Empty(t, appLevel(config.LogConfig{}))
}

func TestValidateLogConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.LogConfig
		wantErr string
	}{
		{name: "empty configuration", cfg: config.LogConfig{}},
		{
			name: "audit with json format",
			cfg: config.LogConfig{Audit: config.AuditLogConfig{Outputs: config.LogOutputsConfig{
				File: config.LogFileConfig{Format: "JSON"},
			}}},
		},
		{
			name: "audit with text format",
			cfg: config.LogConfig{Audit: config.AuditLogConfig{Outputs: config.LogOutputsConfig{
				Console: config.LogConsoleConfig{Format: "text"},
			}}},
			wantErr: "log.audit.outputs.console.format must be json",
		},
		{
			name:    "file name in the default block",
			cfg:     config.LogConfig{Outputs: config.LogOutputsConfig{File: config.LogFileConfig{Name: "all.log"}}},
			wantErr: "log.outputs.file.name is not valid",
		},
		{
			name: "file name that is a path",
			cfg: config.LogConfig{Access: config.AccessLogConfig{Outputs: config.LogOutputsConfig{
				File: config.LogFileConfig{Name: "../access.log"},
			}}},
			wantErr: "log.access.outputs.file.name must be a file name",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLogConfig(tc.cfg)
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
