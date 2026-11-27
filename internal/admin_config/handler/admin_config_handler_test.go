/*
 * Copyright (c) 2026, WSO2 LLC. (http://www.wso2.com).
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
package handler

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/admin_config/model"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/security"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

const (
	adminUser     = "admin"
	adminPassword = "admin-secret"
)

var adminHandlerOrgCounter atomic.Int64

type adminHandlerEnv struct {
	db     *sql.DB
	handle string
	orgID  string
	auth   setup.AuthStub
	h      *AdminConfigHandler
}

func newAdminHandlerEnv(t *testing.T) *adminHandlerEnv {
	t.Helper()
	require.NoError(t, log.Init("ERROR"))
	setup.EnableOrganizationManagement()
	testDB, err := setup.SetupTestSQLite()
	require.NoError(t, err)
	provider.SetTestDB(testDB.DB, database.TypeSQLite)
	t.Cleanup(func() {
		provider.SetTestDB(nil, "")
		testDB.Terminate()
	})
	auth := setup.InstallAuthStub(t, "admin_config:view", "admin_config:update")
	// The stub restores the previous configuration when the test ends, so adding the admin
	// credentials to the current one needs no cleanup.
	conf := config.GetCDSRuntime().Config
	conf.AuthServer.AdminUsername = adminUser
	conf.AuthServer.AdminPassword = adminPassword
	config.OverrideCDSRuntime(conf)

	handle := fmt.Sprintf("acme-%d", adminHandlerOrgCounter.Add(1))
	env := &adminHandlerEnv{db: testDB.DB, handle: handle, orgID: setup.TestOrgID(handle), auth: auth,
		h: NewAdminConfigHandler()}
	setup.TestOrg(t, handle)
	return env
}

func (e *adminHandlerEnv) call(t *testing.T, handler http.HandlerFunc, method, authorization,
	body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/t/"+e.handle+"/cds/api/v1/config", strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), constants.TenantContextKey, e.handle))
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func (e *adminHandlerEnv) bearer() string { return "Bearer " + e.auth.ValidToken }

func basic(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

func decodeConfig(t *testing.T, recorder *httptest.ResponseRecorder) model.AdminConfigAPI {
	t.Helper()
	var config model.AdminConfigAPI
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &config), recorder.Body.String())
	return config
}

func TestGetAdminConfigOfAnOrganizationThatNeverSetAnything(t *testing.T) {
	env := newAdminHandlerEnv(t)

	recorder := env.call(t, env.h.GetAdminConfig, http.MethodGet, env.bearer(), "")

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	got := decodeConfig(t, recorder)
	assert.False(t, got.CDSEnabled)
	// Merging is on until an organization turns it off, and so is the decisive deterministic match.
	assert.True(t, got.AutoMergeEnabled)
	assert.True(t, got.DeterministicMatchDecisive)
	assert.Empty(t, got.SystemApplications)
	assert.Empty(t, got.CORSAllowedOrigins)
}

func TestUpdateAdminConfigChangesOnlyTheFieldsThatAreSent(t *testing.T) {
	env := newAdminHandlerEnv(t)

	first := env.call(t, env.h.UpdateAdminConfig, http.MethodPatch, env.bearer(),
		`{"auto_merge_enabled":true,"auto_merge_threshold":0.9,"manual_review_threshold":0.7,`+
			`"deterministic_match_decisive":true,"system_applications":["app-1"],`+
			`"cors_allowed_origins":["https://app.example.com","https://app.example.com"]}`)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	updated := decodeConfig(t, first)
	assert.True(t, updated.AutoMergeEnabled)
	assert.InDelta(t, 0.9, updated.AutoMergeThreshold, 1e-9)
	assert.InDelta(t, 0.7, updated.ManualReviewThreshold, 1e-9)
	assert.True(t, updated.DeterministicMatchDecisive)
	assert.Equal(t, []string{"app-1"}, updated.SystemApplications)
	assert.Equal(t, []string{"https://app.example.com"}, updated.CORSAllowedOrigins, "duplicates are dropped")

	second := env.call(t, env.h.UpdateAdminConfig, http.MethodPatch, env.bearer(), `{"auto_merge_enabled":false}`)
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	got := decodeConfig(t, env.call(t, env.h.GetAdminConfig, http.MethodGet, env.bearer(), ""))
	assert.False(t, got.AutoMergeEnabled)
	assert.InDelta(t, 0.9, got.AutoMergeThreshold, 1e-9, "a field that was not sent keeps its value")
	assert.Equal(t, []string{"app-1"}, got.SystemApplications)
}

func TestUpdateAdminConfigRefusals(t *testing.T) {
	env := newAdminHandlerEnv(t)
	tests := map[string]string{
		"malformed JSON":                         `{"auto_merge_enabled":`,
		"an unknown field":                       `{"color":"red"}`,
		"a threshold above one":                  `{"auto_merge_threshold":1.5}`,
		"a negative threshold":                   `{"manual_review_threshold":-0.1}`,
		"a review threshold that is too low":     `{"manual_review_threshold":0.2}`,
		"a review threshold above the merge one": `{"auto_merge_threshold":0.8,"manual_review_threshold":0.8}`,
		"an origin with a wildcard":              `{"cors_allowed_origins":["https://*.example.com"]}`,
		"an origin with a path":                  `{"cors_allowed_origins":["https://app.example.com/x"]}`,
		"an origin without a scheme":             `{"cors_allowed_origins":["app.example.com"]}`,
		"an empty origin":                        `{"cors_allowed_origins":[" "]}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := env.call(t, env.h.UpdateAdminConfig, http.MethodPatch, env.bearer(), body)

			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}
}

func TestUpdateAdminConfigDoesNotTakeTheAdminCredentials(t *testing.T) {
	env := newAdminHandlerEnv(t)

	recorder := env.call(t, env.h.UpdateAdminConfig, http.MethodPatch, basic(adminUser, adminPassword),
		`{"system_applications":["app-1"]}`)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code, recorder.Body.String())
	got := decodeConfig(t, env.call(t, env.h.GetAdminConfig, http.MethodGet, env.bearer(), ""))
	assert.Empty(t, got.SystemApplications)
}

func TestSyncAdminConfigChangesOnlyCDSEnabled(t *testing.T) {
	env := newAdminHandlerEnv(t)
	first := env.call(t, env.h.UpdateAdminConfig, http.MethodPatch, env.bearer(),
		`{"system_applications":["app-1"],"auto_merge_enabled":false}`)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())

	recorder := env.call(t, env.h.SyncAdminConfig, http.MethodPost, basic(adminUser, adminPassword),
		`{"cds_enabled":false}`)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	got := decodeConfig(t, env.call(t, env.h.GetAdminConfig, http.MethodGet, env.bearer(), ""))
	assert.False(t, got.CDSEnabled)
	assert.Equal(t, []string{"app-1"}, got.SystemApplications, "a sync keeps the other settings")
	assert.False(t, got.AutoMergeEnabled, "a sync keeps the other settings")
}

func TestSyncAdminConfigRefusals(t *testing.T) {
	env := newAdminHandlerEnv(t)
	tests := map[string]string{
		"malformed JSON":            `{"cds_enabled":`,
		"no cds_enabled":            `{}`,
		"another configuration key": `{"cds_enabled":false,"system_applications":["app-1"]}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := env.call(t, env.h.SyncAdminConfig, http.MethodPost, basic(adminUser, adminPassword), body)

			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}
}

func TestAdminConfigAuthentication(t *testing.T) {
	env := newAdminHandlerEnv(t)

	tests := []struct {
		name          string
		handler       http.HandlerFunc
		method        string
		authorization string
		status        int
	}{
		{"get without a token", env.h.GetAdminConfig, http.MethodGet, "", http.StatusUnauthorized},
		{"get with the wrong scope", env.h.GetAdminConfig, http.MethodGet, "Bearer " + env.auth.OtherToken,
			http.StatusForbidden},
		{"get does not take the admin credentials", env.h.GetAdminConfig, http.MethodGet,
			basic(adminUser, adminPassword), http.StatusUnauthorized},
		{"patch without a token", env.h.UpdateAdminConfig, http.MethodPatch, "", http.StatusUnauthorized},
		{"patch with the wrong scope", env.h.UpdateAdminConfig, http.MethodPatch, "Bearer " + env.auth.OtherToken,
			http.StatusForbidden},
		{"patch does not take the admin credentials", env.h.UpdateAdminConfig, http.MethodPatch,
			basic(adminUser, adminPassword), http.StatusUnauthorized},
		{"sync without credentials", env.h.SyncAdminConfig, http.MethodPost, "", http.StatusUnauthorized},
		{"sync does not take a token", env.h.SyncAdminConfig, http.MethodPost, env.bearer(),
			http.StatusUnauthorized},
		{"sync with the wrong password", env.h.SyncAdminConfig, http.MethodPost, basic(adminUser, "nope"),
			http.StatusUnauthorized},
		{"sync with the wrong user", env.h.SyncAdminConfig, http.MethodPost, basic("other", adminPassword),
			http.StatusUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := env.call(t, tc.handler, tc.method, tc.authorization, `{}`)

			assert.Equal(t, tc.status, recorder.Code)
		})
	}
}

// With a sync listener, the config sync takes the admin credentials on the sync listener only.
func TestSyncAdminConfigAuthenticationByListener(t *testing.T) {
	env := newAdminHandlerEnv(t)
	conf := config.GetCDSRuntime().Config
	conf.Sync = config.SyncConfig{Addr: config.AddrConfig{Port: 8901}}
	config.OverrideCDSRuntime(conf)

	for name, tc := range map[string]struct {
		syncListener  bool
		authorization string
		status        int
	}{
		"the admin credentials on the sync listener": {
			syncListener: true, authorization: basic(adminUser, adminPassword), status: http.StatusOK,
		},
		"the admin credentials on the public listener": {
			authorization: basic(adminUser, adminPassword), status: http.StatusUnauthorized,
		},
		"a token on the sync listener": {
			syncListener: true, authorization: env.bearer(), status: http.StatusUnauthorized,
		},
	} {
		t.Run(name, func(t *testing.T) {
			handler := http.HandlerFunc(env.h.SyncAdminConfig)
			if tc.syncListener {
				handler = security.SyncListener(handler).ServeHTTP
			}

			recorder := env.call(t, handler, http.MethodPost, tc.authorization, `{"cds_enabled":false}`)

			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}
}
