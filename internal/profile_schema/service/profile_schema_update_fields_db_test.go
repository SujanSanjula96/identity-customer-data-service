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
package service

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

func TestUpdateAttributeKeepsTheFieldsThatAreNotGiven(t *testing.T) {
	env := newSchemaEnv(t)
	attr := trait("traits.city")
	attr.DisplayName = "Home city"
	attr.MultiValued = true
	attr.CanonicalValues = []model.CanonicalValue{{Value: "CMB", Label: "Colombo"}}
	id := env.add(t, constants.Traits, attr)[0].AttributeID

	err := env.svc.UpdateProfileSchemaAttributeByID(env.ctx, env.orgID, id, updateBody("traits.city",
		func(u *model.ProfileSchemaAttributeUpdate) {
			u.Mutability = ptr(constants.MutabilityWriteOnce)
		}), constants.Traits)

	require.NoError(t, err)
	got, err := env.svc.GetProfileSchemaAttributeByID(env.ctx, env.orgID, id)
	require.NoError(t, err)
	assert.Equal(t, constants.MutabilityWriteOnce, got.Mutability)
	assert.Equal(t, "Home city", got.DisplayName)
	assert.True(t, got.MultiValued)
	assert.Equal(t, attr.CanonicalValues, got.CanonicalValues)
}

func TestUpdateAttributeChangesEveryUpdatableField(t *testing.T) {
	env := newSchemaEnv(t)
	id := env.add(t, constants.Traits, trait("traits.city"))[0].AttributeID

	err := env.svc.UpdateProfileSchemaAttributeByID(env.ctx, env.orgID, id, updateBody("traits.city",
		func(u *model.ProfileSchemaAttributeUpdate) {
			u.DisplayName = ptr("Home city")
			u.Mutability = ptr(constants.MutabilityWriteOnce)
			u.MultiValued = ptr(true)
			u.CanonicalValues = &[]model.CanonicalValue{{Value: "CMB", Label: "Colombo"}}
		}), constants.Traits)

	require.NoError(t, err)
	got, err := env.svc.GetProfileSchemaAttributeByID(env.ctx, env.orgID, id)
	require.NoError(t, err)
	assert.Equal(t, "Home city", got.DisplayName)
	assert.Equal(t, constants.MutabilityWriteOnce, got.Mutability)
	assert.True(t, got.MultiValued)
	assert.Equal(t, []model.CanonicalValue{{Value: "CMB", Label: "Colombo"}}, got.CanonicalValues)
}

// The scope of the path must be the scope of the attribute, so that a path of one scope cannot change an
// attribute of another scope.
func TestUpdateAttributeRefusesAnAttributeOfAnotherScope(t *testing.T) {
	env := newSchemaEnv(t)
	_, err := env.db.Exec(`INSERT INTO profile_schema (attribute_id, scope, attribute_name, display_name, value_type,
		merge_strategy, application_identifier, mutability, org_id)
		VALUES ('id-email', 'identity_attributes', 'identity_attributes.email', 'Email', 'string', 'overwrite', '',
		'readOnly', $1)`, env.orgID)
	require.NoError(t, err)

	err = env.svc.UpdateProfileSchemaAttributeByID(env.ctx, env.orgID, "id-email",
		updateBody("identity_attributes.email"), constants.Traits)

	_ = requireClientErr(t, err, http.StatusNotFound)
	var mutability string
	require.NoError(t, env.db.QueryRow(`SELECT mutability FROM profile_schema WHERE attribute_id = 'id-email'`).
		Scan(&mutability))
	assert.Equal(t, "readOnly", mutability)
}

func TestUpdateAttributeRefusesFieldValuesThatAreNotValid(t *testing.T) {
	env := newSchemaEnv(t)
	attr := trait("traits.city")
	attr.DisplayName = "City"
	id := env.add(t, constants.Traits, attr)[0].AttributeID

	tests := []struct {
		name   string
		change func(*model.ProfileSchemaAttributeUpdate)
	}{
		{"an application identifier outside application data", func(u *model.ProfileSchemaAttributeUpdate) {
			u.ApplicationIdentifier = ptr("some-app")
		}},
		{"a display name with characters that are not allowed", func(u *model.ProfileSchemaAttributeUpdate) {
			u.DisplayName = ptr("City <b>")
		}},
		{"a display name that is too long", func(u *model.ProfileSchemaAttributeUpdate) {
			u.DisplayName = ptr(strings.Repeat("a", constants.MaxAttributeDisplayNameLength+1))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := env.svc.UpdateProfileSchemaAttributeByID(env.ctx, env.orgID, id,
				updateBody("traits.city", tc.change), constants.Traits)

			_ = requireClientErr(t, err, http.StatusBadRequest)
			got, err := env.svc.GetProfileSchemaAttributeByID(env.ctx, env.orgID, id)
			require.NoError(t, err)
			assert.Equal(t, "City", got.DisplayName)
			assert.Empty(t, got.ApplicationIdentifier)
		})
	}
}

func TestUpdateApplicationDataAttributeChangesItsApplicationIdentifier(t *testing.T) {
	env := newSchemaEnv(t)
	t.Cleanup(OverrideValidateApplicationIdentifierForTest(func(context.Context, string, string) (error, bool) {
		return nil, true
	}))
	attr := trait("application_data.color")
	attr.ApplicationIdentifier = "app-1"
	id := env.add(t, constants.ApplicationData, attr)[0].AttributeID

	err := env.svc.UpdateProfileSchemaAttributeByID(env.ctx, env.orgID, id, updateBody("application_data.color"),
		constants.ApplicationData)
	require.NoError(t, err)
	got, err := env.svc.GetProfileSchemaAttributeByID(env.ctx, env.orgID, id)
	require.NoError(t, err)
	assert.Equal(t, "app-1", got.ApplicationIdentifier, "an absent application identifier keeps its value")

	err = env.svc.UpdateProfileSchemaAttributeByID(env.ctx, env.orgID, id, updateBody("application_data.color",
		func(u *model.ProfileSchemaAttributeUpdate) {
			u.ApplicationIdentifier = ptr("app-2")
		}), constants.ApplicationData)
	require.NoError(t, err)
	got, err = env.svc.GetProfileSchemaAttributeByID(env.ctx, env.orgID, id)
	require.NoError(t, err)
	assert.Equal(t, "app-2", got.ApplicationIdentifier)
}
