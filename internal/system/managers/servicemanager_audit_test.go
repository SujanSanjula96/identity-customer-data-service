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

package managers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wso2/identity-customer-data-service/internal/system/audit"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/httpmw"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// A request with no token goes through the request middleware and the registered routes. The audit
// log must record the denied attempt, with the correlation ID, the organization, and the target
// that the request named.
func TestDeniedRequestIsAudited(t *testing.T) {
	config.OverrideCDSRuntime(config.Config{})
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	require.NoError(t, audit.Init(audit.Options{
		Enabled: true,
		Output:  log.OutputOptions{File: log.FileOutput(auditPath)},
	}))
	defer func() { _ = audit.Init(audit.Options{}) }()

	mux := http.NewServeMux()
	require.NoError(t, NewServiceManager(mux).RegisterServices())
	handler := httpmw.RequestContext(mux)

	request := httptest.NewRequest(http.MethodDelete, "/t/carbon.super/cds/api/v1/profiles/p-1", nil)
	request.Header.Set(httpmw.CorrelationIDHeader, "corr-42")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusUnauthorized, response.Code)
	content, err := os.ReadFile(auditPath) // #nosec G304 -- the test reads a file in its temporary directory.
	require.NoError(t, err)
	lines := bytes.Split(bytes.TrimSpace(content), []byte("\n"))
	require.Len(t, lines, 1)

	var record map[string]any
	require.NoError(t, json.Unmarshal(lines[0], &record))
	assert.Equal(t, "profile.delete", record["event"])
	assert.Equal(t, "corr-42", record["correlation_id"])
	assert.Equal(t, map[string]any{"handle": "carbon.super"}, record["org"])
	assert.Equal(t, map[string]any{"type": "anonymous"}, record["actor"])
	assert.Equal(t, map[string]any{"type": "profile", "id": "p-1"}, record["target"])
	outcome := record["outcome"].(map[string]any)
	assert.Equal(t, "denied", outcome["result"])
	assert.Equal(t, "unauthenticated", outcome["reason"])
}
