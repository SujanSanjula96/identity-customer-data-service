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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
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
