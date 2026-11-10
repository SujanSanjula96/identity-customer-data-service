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

package log

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wso2/identity-customer-data-service/internal/system/log/rollingfile"
	"github.com/wso2/identity-customer-data-service/internal/system/reqctx"
)

// fileLogger returns a logger that writes to a file in a temporary directory, and the file path.
func fileLogger(t *testing.T, opts Options) (*Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "logs", "cds.log")
	opts.Output = OutputOptions{File: &rollingfile.Config{Path: path}}
	logger, err := New(TypeApp, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })
	return logger, path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path) // #nosec G304 -- the test reads a file in its temporary directory.
	require.NoError(t, err)
	return string(content)
}

func TestTextRecordHasStandardFields(t *testing.T) {
	logger, path := fileLogger(t, Options{Level: "INFO"})

	logger.WithComponent("ProfileService").Info("Profile created.", String(KeyProfileID, "p-1"))

	line := readFile(t, path)
	assert.Regexp(t, `^time=\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z level=INFO msg="Profile created\." `, line)
	assert.Contains(t, line, "log_type=app")
	assert.Contains(t, line, "component=ProfileService")
	assert.Contains(t, line, "profile_id=p-1")
}

func TestJSONRecordHasContextFields(t *testing.T) {
	logger, path := fileLogger(t, Options{Level: "DEBUG", Format: FormatJSON})
	info := reqctx.New("corr-1", "", "")
	info.SetOrg("carbon.super", "")
	ctx := reqctx.With(context.Background(), info)

	logger.WithContext(ctx).Error("Update failed.", Error(errors.New("boom")))

	var record map[string]any
	require.NoError(t, json.Unmarshal([]byte(readFile(t, path)), &record))
	assert.Equal(t, "ERROR", record["level"])
	assert.Equal(t, "Update failed.", record["msg"])
	assert.Equal(t, TypeApp, record[KeyLogType])
	assert.Equal(t, "corr-1", record[KeyCorrelationID])
	assert.Equal(t, "carbon.super", record[KeyOrg])
	assert.Equal(t, "boom", record[KeyError])
}

func TestLevelFiltersRecords(t *testing.T) {
	logger, path := fileLogger(t, Options{Level: "WARN"})

	logger.Info("not written")
	logger.Debug("not written")
	logger.Warn("written")

	content := readFile(t, path)
	assert.NotContains(t, content, "not written")
	assert.Contains(t, content, "written")
	assert.False(t, logger.IsDebugEnabled())
}

func TestDerivedLoggerFollowsNewConfiguration(t *testing.T) {
	logger, firstPath := fileLogger(t, Options{Level: "INFO"})
	derived := logger.WithComponent("Worker")

	secondPath := filepath.Join(t.TempDir(), "cds.log")
	require.NoError(t, logger.configure(TypeApp, Options{
		Level:  "INFO",
		Format: FormatJSON,
		Output: OutputOptions{File: &rollingfile.Config{Path: secondPath}},
	}))
	derived.Info("after configure")

	assert.Empty(t, readFile(t, firstPath))
	second := readFile(t, secondPath)
	assert.True(t, strings.HasPrefix(second, "{"), "the derived logger uses the new format")
	assert.Contains(t, second, `"component":"Worker"`)
}

func TestInvalidOptionsAreRefused(t *testing.T) {
	_, err := New(TypeApp, Options{Level: "LOUD"})
	assert.Error(t, err)

	_, err = New(TypeApp, Options{Format: "xml"})
	assert.Error(t, err)
}

func TestGetLoggerWorksBeforeInit(t *testing.T) {
	assert.NotNil(t, GetLogger())
	assert.NotPanics(t, func() { GetLogger().Debug("no output expected") })
}

func TestMask(t *testing.T) {
	assert.Equal(t, "a***************m", Mask("alice@example.com"))
	assert.Equal(t, "***", Mask("abc"))
	assert.Equal(t, "", Mask(""))
}
