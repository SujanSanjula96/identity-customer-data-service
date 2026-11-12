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
package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

func updateBody(name string, extra map[string]interface{}) map[string]interface{} {
	body := map[string]interface{}{
		"attribute_name": name,
		"value_type":     constants.StringDataType,
		"merge_strategy": "overwrite",
		"mutability":     constants.MutabilityReadWrite,
	}
	for key, value := range extra {
		body[key] = value
	}
	return body
}

func TestUpdateAttributeChangesItsDefinition(t *testing.T) {
	env := newSchemaEnv(t)
	stored := env.add(t, constants.Traits, trait("traits.tags"))
	id := stored[0].AttributeId

	err := env.svc.UpdateProfileSchemaAttributeById(env.ctx, env.orgID, id, updateBody("traits.tags",
		map[string]interface{}{
			"mutability":   constants.MutabilityWriteOnce,
			"multi_valued": true,
		}), constants.Traits)

	require.NoError(t, err)
	got, err := env.svc.GetProfileSchemaAttributeById(env.ctx, env.orgID, id)
	require.NoError(t, err)
	assert.Equal(t, constants.MutabilityWriteOnce, got.Mutability)
	assert.True(t, got.MultiValued)
}

func TestUpdateAttributeRefusals(t *testing.T) {
	env := newSchemaEnv(t)
	stored := env.add(t, constants.Traits, trait("traits.city"))
	id := stored[0].AttributeId

	tests := []struct {
		name    string
		id      string
		updates map[string]interface{}
		status  int
	}{
		{"no updates", id, map[string]interface{}{}, http.StatusBadRequest},
		{"an attribute that does not exist", "missing", updateBody("traits.city", nil), http.StatusNotFound},
		{"a missing required field", id, map[string]interface{}{"attribute_name": "traits.city"},
			http.StatusBadRequest},
		{"an empty required field", id, updateBody("traits.city", map[string]interface{}{"merge_strategy": ""}),
			http.StatusBadRequest},
		{"a value type that is not allowed", id, updateBody("traits.city",
			map[string]interface{}{"value_type": "blob"}),
			http.StatusBadRequest},
		{"a mutability that is not allowed", id, updateBody("traits.city",
			map[string]interface{}{"mutability": "never"}),
			http.StatusBadRequest},
		{"multi_valued that is not a boolean", id, updateBody("traits.city",
			map[string]interface{}{"multi_valued": "yes"}),
			http.StatusBadRequest},
		{"a name outside the scope format", id, updateBody("city", nil), http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := env.svc.UpdateProfileSchemaAttributeById(env.ctx, env.orgID, tc.id, tc.updates, constants.Traits)

			_ = requireClientErr(t, err, tc.status)
		})
	}
}

func TestUpdatingAttributeIsRefusedInASubOrganization(t *testing.T) {
	env := newSchemaEnv(t)
	_, err := env.db.Exec(`INSERT INTO organizations (org_id, org_handle, primary_org_id, status)
		VALUES ('sub-org', 'sub', $1, 'ACTIVE')`, env.orgID)
	require.NoError(t, err)

	err = env.svc.UpdateProfileSchemaAttributeById(env.ctx, "sub-org", "any", updateBody("traits.city", nil),
		constants.Traits)

	_ = requireClientErr(t, err, http.StatusForbidden)
}

func TestGetAttributesWithFilter(t *testing.T) {
	env := newSchemaEnv(t)
	env.add(t, constants.Traits, trait("traits.city"), trait("traits.country"), trait("traits.age"))

	got, err := GetProfileSchemaAttributesWithFilter(env.ctx, env.orgID, []string{"attribute_name contains c"})
	require.NoError(t, err)
	assert.Len(t, got, 2)

	all, err := GetProfileSchemaAttributesWithFilter(env.ctx, env.orgID, nil)
	require.NoError(t, err)
	assert.Len(t, all, 3)

	_, err = GetProfileSchemaAttributesWithFilter(env.ctx, env.orgID, []string{"attribute_name"})
	_ = requireClientErr(t, err, http.StatusBadRequest)
}
