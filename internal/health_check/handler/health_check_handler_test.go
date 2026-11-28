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

package handler

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/log/rollingfile"
)

// closedPort returns a local TCP port that nothing listens on.
func closedPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return port
}

// TestReadinessFailureReturnsOnlyStatus runs the readiness check against a database that refuses the
// connection. The response has only the status, and the application log has the reason.
func TestReadinessFailureReturnsOnlyStatus(t *testing.T) {
	port := closedPort(t)
	require.NoError(t, config.InitializeCDSRuntime(t.TempDir(), &config.Config{
		DataSource: config.DataSourceConfig{
			Type:     "postgres",
			Hostname: "127.0.0.1",
			Port:     port,
			Name:     "cds_ready_db",
			Username: "cds_ready_user",
			Password: "cds_ready_password",
			SSLMode:  "disable",
		},
	}))

	logPath := filepath.Join(t.TempDir(), "app.log")
	require.NoError(t, log.Configure(log.Options{
		Format: log.FormatJSON,
		Output: log.OutputOptions{File: &rollingfile.Config{Path: logPath}},
	}))
	t.Cleanup(func() { _ = log.Init("INFO") })

	w := httptest.NewRecorder()
	NewHealthHandler().HandleReadiness(w, httptest.NewRequest(http.MethodGet, "/cds/api/v1/ready", nil))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, map[string]string{"status": "not ready"}, body)
	for _, detail := range []string{strconv.Itoa(port), "127.0.0.1", "cds_ready", "database", "refused"} {
		assert.NotContains(t, w.Body.String(), detail)
	}

	logged, err := os.ReadFile(logPath) // #nosec G304 -- the test reads a file in its temporary directory.
	require.NoError(t, err)
	assert.Contains(t, string(logged), "Readiness check failed")
	assert.Contains(t, string(logged), strconv.Itoa(port))
}

// TestHealthReturnsOnlyStatus checks that the health response has only the status.
func TestHealthReturnsOnlyStatus(t *testing.T) {
	w := httptest.NewRecorder()
	NewHealthHandler().HandleHealth(w, httptest.NewRequest(http.MethodGet, "/cds/api/v1/health", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, map[string]string{"status": "healthy"}, body)
}
