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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/organization/model"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

const (
	orgAdminUser     = "admin"
	orgAdminPassword = "admin-secret"
)

var orgHandlerCounter atomic.Int64

type orgHandlerEnv struct {
	db     *sql.DB
	handle string
	orgID  string
	auth   setup.AuthStub
	h      *OrganizationHandler
}

func newOrgHandlerEnv(t *testing.T) *orgHandlerEnv {
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
	auth := setup.InstallAuthStub(t, "organization:view", "organization:update")
	conf := config.GetCDSRuntime().Config
	conf.AuthServer.AdminUsername = orgAdminUser
	conf.AuthServer.AdminPassword = orgAdminPassword
	config.OverrideCDSRuntime(conf)

	handle := fmt.Sprintf("acme-%d", orgHandlerCounter.Add(1))
	env := &orgHandlerEnv{db: testDB.DB, handle: handle, orgID: setup.TestOrgId(handle), auth: auth,
		h: NewOrganizationHandler()}
	setup.TestOrg(t, handle)
	_, err = env.db.Exec(`INSERT INTO cds_config (org_id, config, value) VALUES (?, 'cds_enabled', 'true')`, env.orgID)
	require.NoError(t, err)
	return env
}

func (e *orgHandlerEnv) call(t *testing.T, handler http.HandlerFunc, method, authorization, body string,
	pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/t/"+e.handle+"/cds/api/v1/organizations", strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), constants.TenantContextKey, e.handle))
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	request.Header.Set("Content-Type", "application/json")
	for name, value := range pathValues {
		request.SetPathValue(name, value)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func (e *orgHandlerEnv) bearer() string { return "Bearer " + e.auth.ValidToken }

func adminBasic(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

func (e *orgHandlerEnv) status(t *testing.T) model.TreeSyncStatus {
	t.Helper()
	recorder := e.call(t, e.h.GetTreeSync, http.MethodGet, e.bearer(), "", nil)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var status model.TreeSyncStatus
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &status))
	return status
}

// syncTree starts the sync and waits until it ends, so that no background work outlives the test.
func (e *orgHandlerEnv) syncTree(t *testing.T) {
	t.Helper()
	started := e.call(t, e.h.StartTreeSync, http.MethodPost, e.bearer(), "", nil)
	require.Equal(t, http.StatusAccepted, started.Code, started.Body.String())
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state := e.status(t).State
		if state == model.TreeSyncSynced {
			return
		}
		require.NotEqual(t, model.TreeSyncFailed, state, "the sync failed")
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the sync did not finish")
}

func TestTreeSyncStartsAndEnds(t *testing.T) {
	env := newOrgHandlerEnv(t)

	assert.Equal(t, model.TreeSyncNotSynced, env.status(t).State)

	env.syncTree(t)

	status := env.status(t)
	assert.Equal(t, model.TreeSyncSynced, status.State)
	assert.NotNil(t, status.LastSyncedAt)
}

func TestSyncOrganizationEvents(t *testing.T) {
	env := newOrgHandlerEnv(t)
	basic := adminBasic(orgAdminUser, orgAdminPassword)
	send := func(authorization, body string) *httptest.ResponseRecorder {
		return env.call(t, env.h.SyncOrganization, http.MethodPost, authorization, body, nil)
	}

	// Until the tree is synced the events are ignored.
	assert.Equal(t, http.StatusNoContent, send(basic, `{"event":"ORG_CREATED","org_id":"child-1"}`).Code)

	env.syncTree(t)
	assert.Equal(t, http.StatusNoContent, send(basic, `{"event":"ORG_CREATED","org_id":"child-1"}`).Code)
	assert.Equal(t, http.StatusNoContent, send(basic, `{"event":"ORG_UPDATED","org_id":"child-1"}`).Code)
	assert.Equal(t, http.StatusNoContent, send(basic, `{"event":"ORG_DELETED","org_id":"child-1"}`).Code)

	assert.Equal(t, http.StatusBadRequest, send(basic, `{"event":"ORG_RENAMED","org_id":"child-1"}`).Code)
	assert.Equal(t, http.StatusBadRequest, send(basic, `{"event":"ORG_CREATED"}`).Code)
	assert.Equal(t, http.StatusBadRequest, send(basic, `{"org_id":"child-1"}`).Code)
	assert.Equal(t, http.StatusBadRequest, send(basic, `{"event":`).Code)

	assert.Equal(t, http.StatusUnauthorized, send("", `{"event":"ORG_CREATED","org_id":"child-1"}`).Code)
	assert.Equal(t, http.StatusUnauthorized,
		send(adminBasic(orgAdminUser, "wrong"), `{"event":"ORG_CREATED","org_id":"child-1"}`).Code)
	assert.Equal(t, http.StatusUnauthorized, send(env.bearer(), `{"event":"ORG_CREATED","org_id":"child-1"}`).Code,
		"the sync takes the admin credentials, not a token")
}

func TestSyncOrganizationNeedsCDSEnabled(t *testing.T) {
	env := newOrgHandlerEnv(t)
	_, err := env.db.Exec(`DELETE FROM cds_config WHERE org_id = ?`, env.orgID)
	require.NoError(t, err)

	recorder := env.call(t, env.h.SyncOrganization, http.MethodPost, adminBasic(orgAdminUser, orgAdminPassword),
		`{"event":"ORG_CREATED","org_id":"child-1"}`, nil)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestOrganizationAccessLifecycle(t *testing.T) {
	env := newOrgHandlerEnv(t)
	env.syncTree(t)

	empty := env.call(t, env.h.ListOrgAccess, http.MethodGet, env.bearer(), "", nil)
	require.Equal(t, http.StatusOK, empty.Code, empty.Body.String())
	assert.Contains(t, empty.Body.String(), `"total_results":0`)

	created := env.call(t, env.h.CreateOrgAccess, http.MethodPost, env.bearer(), `{"child_orgs":{"mode":"all"}}`, nil)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var policy struct {
		ID        string `json:"id"`
		ChildOrgs struct {
			Mode string `json:"mode"`
		} `json:"child_orgs"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &policy))
	require.NotEmpty(t, policy.ID)
	assert.Equal(t, "all", policy.ChildOrgs.Mode)
	ids := map[string]string{"orgPolicyId": policy.ID}

	again := env.call(t, env.h.CreateOrgAccess, http.MethodPost, env.bearer(), `{"child_orgs":{"mode":"all"}}`, nil)
	assert.Equal(t, http.StatusConflict, again.Code, "an organization has one access policy")

	list := env.call(t, env.h.ListOrgAccess, http.MethodGet, env.bearer(), "", nil)
	require.Equal(t, http.StatusOK, list.Code)
	assert.Contains(t, list.Body.String(), `"total_results":1`)

	got := env.call(t, env.h.GetOrgAccess, http.MethodGet, env.bearer(), "", ids)
	require.Equal(t, http.StatusOK, got.Code, got.Body.String())
	assert.Contains(t, got.Body.String(), policy.ID)

	updated := env.call(t, env.h.UpdateOrgAccess, http.MethodPut, env.bearer(), `{"child_orgs":{"mode":"all"}}`, ids)
	assert.Equal(t, http.StatusOK, updated.Code, updated.Body.String())

	removed := env.call(t, env.h.DeleteOrgAccess, http.MethodDelete, env.bearer(), "", ids)
	assert.Equal(t, http.StatusNoContent, removed.Code)
	missing := env.call(t, env.h.GetOrgAccess, http.MethodGet, env.bearer(), "", ids)
	assert.Equal(t, http.StatusNotFound, missing.Code)
}

func TestOrganizationAccessRefusals(t *testing.T) {
	env := newOrgHandlerEnv(t)

	unsynced := env.call(t, env.h.CreateOrgAccess, http.MethodPost, env.bearer(), `{"child_orgs":{"mode":"all"}}`, nil)
	assert.Equal(t, http.StatusBadRequest, unsynced.Code, "the tree has to be synced first")

	env.syncTree(t)
	tests := map[string]string{
		"malformed JSON":            `{"child_orgs":`,
		"an unknown field":          `{"color":"red"}`,
		"no child_orgs":             `{}`,
		"no mode":                   `{"child_orgs":{}}`,
		"an unknown mode":           `{"child_orgs":{"mode":"some"}}`,
		"all with organizations":    `{"child_orgs":{"mode":"all","orgs":[{"org_id":"x","reach":"org"}]}}`,
		"selected without any":      `{"child_orgs":{"mode":"selected"}}`,
		"selected with no org_id":   `{"child_orgs":{"mode":"selected","orgs":[{"reach":"org"}]}}`,
		"selected with a bad reach": `{"child_orgs":{"mode":"selected","orgs":[{"org_id":"x","reach":"far"}]}}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := env.call(t, env.h.CreateOrgAccess, http.MethodPost, env.bearer(), body, nil)

			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}

	ids := map[string]string{"orgPolicyId": "missing"}
	assert.Equal(t, http.StatusNotFound, env.call(t, env.h.GetOrgAccess, http.MethodGet, env.bearer(), "", ids).Code)
	assert.Equal(t, http.StatusNotFound,
		env.call(t, env.h.UpdateOrgAccess, http.MethodPut, env.bearer(), `{"child_orgs":{"mode":"all"}}`, ids).Code)
	assert.Equal(t, http.StatusNotFound, env.call(t, env.h.DeleteOrgAccess, http.MethodDelete, env.bearer(), "",
		ids).Code)
}

func TestOrganizationHandlersRequireAuthenticationAndCDS(t *testing.T) {
	env := newOrgHandlerEnv(t)
	calls := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		body    string
	}{
		{"start the sync", env.h.StartTreeSync, http.MethodPost, ""},
		{"get the sync", env.h.GetTreeSync, http.MethodGet, ""},
		{"create access", env.h.CreateOrgAccess, http.MethodPost, `{"child_orgs":{"mode":"all"}}`},
		{"list access", env.h.ListOrgAccess, http.MethodGet, ""},
		{"get access", env.h.GetOrgAccess, http.MethodGet, ""},
		{"update access", env.h.UpdateOrgAccess, http.MethodPut, `{"child_orgs":{"mode":"all"}}`},
		{"delete access", env.h.DeleteOrgAccess, http.MethodDelete, ""},
	}
	ids := map[string]string{"orgPolicyId": "any"}
	for _, tc := range calls {
		t.Run(tc.name+" without a token", func(t *testing.T) {
			assert.Equal(t, http.StatusUnauthorized, env.call(t, tc.handler, tc.method, "", tc.body, ids).Code)
		})
		t.Run(tc.name+" with the wrong scope", func(t *testing.T) {
			recorder := env.call(t, tc.handler, tc.method, "Bearer "+env.auth.OtherToken, tc.body, ids)

			assert.Equal(t, http.StatusForbidden, recorder.Code)
		})
	}

	_, err := env.db.Exec(`DELETE FROM cds_config WHERE org_id = ?`, env.orgID)
	require.NoError(t, err)
	for _, tc := range calls {
		t.Run(tc.name+" without CDS", func(t *testing.T) {
			assert.Equal(t, http.StatusBadRequest, env.call(t, tc.handler, tc.method, env.bearer(), tc.body, ids).Code)
		})
	}
}
