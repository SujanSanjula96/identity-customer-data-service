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

package authz

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

func setScopes(t *testing.T, requiredScopes map[string][]string) {

	t.Helper()
	if err := log.Init("ERROR"); err != nil {
		t.Fatalf("log init: %v", err)
	}
	config.OverrideCDSRuntime(config.Config{AuthServer: config.AuthServerConfig{RequiredScopes: requiredScopes}})
}

func TestOperationFor(t *testing.T) {

	assert.Equal(t, "profile:create", OperationFor("profile:create", ""))
	assert.Equal(t, "org:profile:create", OperationFor("profile:create", "0a1b2c3d"))
}

func TestValidatePermission_primaryOrgOperation(t *testing.T) {

	setScopes(t, map[string][]string{
		"profile:create":     {"internal_cds_profile_create"},
		"org:profile:create": {"internal_org_cds_profile_create"},
	})
	op := OperationFor("profile:create", "")

	assert.True(t, ValidatePermission("internal_cds_profile_create", op))
	assert.False(t, ValidatePermission("internal_org_cds_profile_create", op),
		"an org scope must not authorize the primary org operation")
}

func TestValidatePermission_orgOperation(t *testing.T) {

	setScopes(t, map[string][]string{
		"profile:create":     {"internal_cds_profile_create"},
		"org:profile:create": {"internal_org_cds_profile_create"},
	})
	op := OperationFor("profile:create", "0a1b2c3d")

	assert.True(t, ValidatePermission("internal_org_cds_profile_create", op))
	assert.False(t, ValidatePermission("internal_cds_profile_create", op),
		"a primary org scope must not authorize the org operation")
}

func TestValidatePermission_sameScopeForBothOperations(t *testing.T) {

	setScopes(t, map[string][]string{
		"profile:view":     {"cds_profile_view"},
		"org:profile:view": {"cds_profile_view"},
	})

	assert.True(t, ValidatePermission("cds_profile_view", OperationFor("profile:view", "")))
	assert.True(t, ValidatePermission("cds_profile_view", OperationFor("profile:view", "0a1b2c3d")))
}

func TestValidatePermission_noOrgOperation(t *testing.T) {

	// An operation of the primary organization only has no `org:` form. No scope authorizes it in a
	// sub organization.
	setScopes(t, map[string][]string{"admin_config:update": {"internal_cds_admin_config_update"}})

	assert.False(t, ValidatePermission("internal_cds_admin_config_update internal_org_cds_admin_config_update",
		OperationFor("admin_config:update", "0a1b2c3d")))
}

func TestValidatePermission_existingPrimaryOrgBehavior(t *testing.T) {

	setScopes(t, map[string][]string{
		"profile:view":     {"internal_cds_profile_view"},
		"profile:link":     {"internal_cds_profile_link", "internal_cds_profile_update"},
		"org:profile:view": {"internal_org_cds_profile_view"},
	})

	assert.True(t, ValidatePermission("openid internal_cds_profile_view", "profile:view"),
		"other scopes in the token do not matter")
	assert.True(t, ValidatePermission("internal_cds_profile_update internal_cds_profile_link", "profile:link"),
		"all required scopes in any order")
	assert.False(t, ValidatePermission("internal_cds_profile_link", "profile:link"),
		"a missing required scope")
	assert.False(t, ValidatePermission("", "profile:view"), "no scopes")
	assert.False(t, ValidatePermission("internal_cds_profile_view", "profile:delete"), "an unknown operation")
	assert.False(t, ValidatePermission("internal_cds_profile_view_all", "profile:view"), "no prefix match")
}
