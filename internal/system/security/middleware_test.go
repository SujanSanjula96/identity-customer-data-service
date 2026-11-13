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

package security

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

const subOrgId = "6b1f0d2e-4c1a-4f0e-9a51-2d0c3f7a9b10"

func setUp(t *testing.T) {

	t.Helper()
	require.NoError(t, log.Init("ERROR"))
	config.OverrideCDSRuntime(config.Config{AuthServer: config.AuthServerConfig{RequiredScopes: map[string][]string{
		"profile:create":      {"internal_cds_profile_create"},
		"org:profile:create":  {"internal_org_cds_profile_create"},
		"profile:view":        {"cds_profile_view"},
		"org:profile:view":    {"cds_profile_view"},
		"admin_config:update": {"internal_cds_admin_config_update"},
	}}})
}

// primaryOrgRequest returns a request for the primary organization carbon.super.
func primaryOrgRequest() *http.Request {

	r := httptest.NewRequest(http.MethodPost, "/t/carbon.super/cds/api/v1/profiles", nil)
	return r.WithContext(context.WithValue(r.Context(), constants.TenantContextKey, "carbon.super"))
}

// subOrgRequest returns a request for a sub organization of carbon.super.
func subOrgRequest() *http.Request {

	r := httptest.NewRequest(http.MethodPost, "/t/carbon.super/o/"+subOrgId+"/cds/api/v1/profiles", nil)
	ctx := context.WithValue(r.Context(), constants.TenantContextKey, "region1")
	ctx = context.WithValue(ctx, constants.OrgIdContextKey, subOrgId)
	return r.WithContext(ctx)
}

func claims(scope string) map[string]interface{} {
	return map[string]interface{}{"scope": scope}
}

func statusOf(t *testing.T, err error) (int, string) {

	t.Helper()
	clientErr, ok := err.(*errors.ClientError)
	require.True(t, ok, "expected a client error, got %v", err)
	return clientErr.StatusCode, clientErr.Code
}

func TestAuthorize_primaryOrgRequest(t *testing.T) {

	setUp(t)

	assert.NoError(t, authorize(primaryOrgRequest(), claims("internal_cds_profile_create"), "profile:create"))

	status, _ := statusOf(t, authorize(primaryOrgRequest(), claims("internal_org_cds_profile_create"),
		"profile:create"))
	assert.Equal(t, http.StatusForbidden, status, "an org-only scope must not authorize a primary org request")
}

func TestAuthorize_subOrgRequest(t *testing.T) {

	setUp(t)

	assert.NoError(t, authorize(subOrgRequest(), claims("internal_org_cds_profile_create"), "profile:create"))

	status, _ := statusOf(t, authorize(subOrgRequest(), claims("internal_cds_profile_create"), "profile:create"))
	assert.Equal(t, http.StatusForbidden, status, "a primary org scope must not authorize a sub org request")
}

func TestAuthorize_sameScopeForBothOperations(t *testing.T) {

	setUp(t)

	assert.NoError(t, authorize(primaryOrgRequest(), claims("cds_profile_view"), "profile:view"))
	assert.NoError(t, authorize(subOrgRequest(), claims("cds_profile_view"), "profile:view"))
}

func TestAuthorize_existingPrimaryOrgBehavior(t *testing.T) {

	setUp(t)

	assert.NoError(t, authorize(primaryOrgRequest(), claims("openid internal_cds_profile_create"), "profile:create"))

	status, _ := statusOf(t, authorize(primaryOrgRequest(), claims("internal_cds_profile_view"), "profile:create"))
	assert.Equal(t, http.StatusForbidden, status, "a scope of another operation")

	status, _ = statusOf(t, authorize(primaryOrgRequest(), map[string]interface{}{}, "profile:create"))
	assert.Equal(t, http.StatusForbidden, status, "no scope claim")
}

func TestAuthorizePrimary(t *testing.T) {

	setUp(t)

	assert.NoError(t, authorizePrimary(primaryOrgRequest(), claims("internal_cds_admin_config_update"),
		"admin_config:update"))

	status, _ := statusOf(t, authorizePrimary(primaryOrgRequest(), claims("internal_org_cds_admin_config_update"),
		"admin_config:update"))
	assert.Equal(t, http.StatusForbidden, status, "an org scope must not authorize an operation of the primary org")

	status, code := statusOf(t, authorizePrimary(subOrgRequest(),
		claims("internal_cds_admin_config_update internal_org_cds_admin_config_update"), "admin_config:update"))
	assert.Equal(t, http.StatusBadRequest, status, "an operation of the primary org in a sub org")
	assert.Equal(t, errors.SUB_ORG_CONFIG_NOT_ALLOWED.Code, code)
}

// newIntrospectionServer starts a stub identity server with system app grant enabled, and returns the
// path of every introspection request. Like the identity server, it reports a token as active only at
// the introspection endpoint of the primary organization acme.
func newIntrospectionServer(t *testing.T) *[]string {
	t.Helper()
	require.NoError(t, log.Init("ERROR"))
	var paths []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		require.NoError(t, r.ParseForm())
		body := map[string]interface{}{"active": false}
		if r.URL.Path == "/t/acme/oauth2/introspect" && r.PostForm.Get("token") == "switched-token" {
			body = map[string]interface{}{"active": true, "client_id": constants.CONSOLE_APP, "org_id": subOrgId}
		} else if r.URL.Path == "/t/acme/oauth2/introspect" && r.PostForm.Get("token") == "primary-token" {
			body = map[string]interface{}{"active": true, "client_id": constants.CONSOLE_APP, "org_handle": "acme"}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)

	certDir := t.TempDir()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	require.NoError(t, os.WriteFile(filepath.Join(certDir, "is.pem"), certPEM, 0o600))
	host := strings.TrimPrefix(server.URL, "https://")
	config.OverrideCDSRuntime(config.Config{
		AuthServer: config.AuthServerConfig{
			Host:                    host,
			ADUISHostname:           host,
			IntrospectionEndPoint:   "/oauth2/introspect",
			IsSystemAppGrantEnabled: true,
		},
		TLS: config.TLSConfig{CertDir: certDir, TrustStore: "is.pem"},
	})
	return &paths
}

// authenticatePath sends a request with the token through the tenant dispatcher and returns the
// result of authenticate.
func authenticatePath(t *testing.T, path, token string) error {
	t.Helper()
	defer func(resolver func(context.Context, string, string) (string, error)) {
		utils.SubOrgResolver = resolver
	}(utils.SubOrgResolver)
	utils.SubOrgResolver = func(_ context.Context, primaryHandle, orgId string) (string, error) {
		if primaryHandle == "acme" && orgId == subOrgId {
			return "region1", nil
		}
		return "", errors.NewClientError(errors.ORGANIZATION_NOT_FOUND, http.StatusNotFound)
	}

	mux := http.NewServeMux()
	var authErr error
	handled := false
	utils.MountTenantDispatcher(mux, func(w http.ResponseWriter, r *http.Request) {
		handled = true
		_, authErr = authenticate(r)
	})
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	mux.ServeHTTP(httptest.NewRecorder(), r)
	require.True(t, handled, "the dispatcher did not forward the request")
	return authErr
}

func TestAuthenticate_subOrgPathIntrospectsAtThePrimaryOrg(t *testing.T) {
	paths := newIntrospectionServer(t)

	err := authenticatePath(t, "/t/acme/o/"+subOrgId+"/cds/api/v1/profiles", "switched-token")

	require.NoError(t, err)
	assert.Equal(t, []string{"/t/acme/oauth2/introspect"}, *paths)
}

func TestAuthenticate_subOrgPathRejectsATokenOfAnotherOrg(t *testing.T) {
	newIntrospectionServer(t)

	err := authenticatePath(t, "/t/acme/o/"+subOrgId+"/cds/api/v1/profiles", "primary-token")

	require.Error(t, err)
	status, _ := statusOf(t, err)
	assert.Equal(t, http.StatusUnauthorized, status)
}

func TestAuthenticate_primaryOrgPathIntrospectsAtThePrimaryOrg(t *testing.T) {
	paths := newIntrospectionServer(t)

	err := authenticatePath(t, "/t/acme/cds/api/v1/profiles", "primary-token")

	require.NoError(t, err)
	assert.Equal(t, []string{"/t/acme/oauth2/introspect"}, *paths)
}
