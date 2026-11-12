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
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

// orgCounter gives every test its own organization, so that nothing cached by organization
// carries over from one test to the next.
var orgCounter atomic.Int64

type schemaEnv struct {
	ctx   context.Context
	db    *sql.DB
	orgID string
	svc   *ProfileSchemaService
}

func newSchemaEnv(t *testing.T) *schemaEnv {
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

	handle := fmt.Sprintf("acme-%d", orgCounter.Add(1))
	env := &schemaEnv{ctx: context.Background(), db: testDB.DB, orgID: setup.TestOrgId(handle),
		svc: &ProfileSchemaService{}}
	require.NoError(t, setup.InsertTestOrganization(testDB.DB, env.orgID, handle))
	return env
}

// trait returns a valid attribute. The handler generates the ID, so the service tests set one.
func trait(name string) model.ProfileSchemaAttribute {
	return model.ProfileSchemaAttribute{
		AttributeId:   "id-" + name,
		AttributeName: name,
		ValueType:     constants.StringDataType,
		MergeStrategy: "overwrite",
		Mutability:    constants.MutabilityReadWrite,
	}
}

func (e *schemaEnv) add(t *testing.T, scope string,
	attrs ...model.ProfileSchemaAttribute) []model.ProfileSchemaAttribute {
	t.Helper()
	stored, err := e.svc.AddProfileSchemaAttributesForScope(e.ctx, attrs, scope, e.orgID)
	require.NoError(t, err)
	return stored
}

func requireClientErr(t *testing.T, err error, status int) *errors2.ClientError {
	t.Helper()
	var clientError *errors2.ClientError
	require.ErrorAs(t, err, &clientError)
	require.Equal(t, status, clientError.StatusCode)
	return clientError
}

func TestAddAttributeResolvesTheDisplayNameWhenOmitted(t *testing.T) {
	env := newSchemaEnv(t)

	stored := env.add(t, constants.Traits, trait("traits.favouriteColor"))

	require.Len(t, stored, 1)
	assert.NotEmpty(t, stored[0].DisplayName)
}

func TestAddedAttributesCanBeReadBackByIDAndName(t *testing.T) {
	env := newSchemaEnv(t)
	stored := env.add(t, constants.Traits, trait("traits.city"))

	byID, err := env.svc.GetProfileSchemaAttributeById(env.ctx, env.orgID, stored[0].AttributeId)
	require.NoError(t, err)
	assert.Equal(t, "traits.city", byID.AttributeName)

	byName, err := env.svc.GetProfileSchemaAttributeByName(env.ctx, "traits.city", env.orgID)
	require.NoError(t, err)
	require.NotNil(t, byName)
	assert.Equal(t, stored[0].AttributeId, byName.AttributeId)
}

func TestAddAttributeValidation(t *testing.T) {
	env := newSchemaEnv(t)
	with := func(mutate func(a *model.ProfileSchemaAttribute)) model.ProfileSchemaAttribute {
		a := trait("traits.city")
		mutate(&a)
		return a
	}
	tests := []struct {
		name  string
		attr  model.ProfileSchemaAttribute
		scope string
	}{
		{"no scope in the name", trait("city"), constants.Traits},
		{"too deep", trait("traits.a.b.c.d.e"), constants.Traits},
		{"unknown scope", trait("secrets.key"), "secrets"},
		{"name outside the scope", trait("traits.city"), constants.IdentityAttributes},
		{"unknown value type", with(func(a *model.ProfileSchemaAttribute) { a.ValueType = "blob" }), constants.Traits},
		{"unknown mutability", with(func(a *model.ProfileSchemaAttribute) { a.Mutability = "sometimes" }),
			constants.Traits},
		{"unknown merge strategy", with(func(a *model.ProfileSchemaAttribute) { a.MergeStrategy = "newest" }),
			constants.Traits},
		{"sub attributes on a simple type", with(func(a *model.ProfileSchemaAttribute) {
			a.SubAttributes = []model.SubAttribute{{AttributeId: "x", AttributeName: "traits.city.name"}}
		}), constants.Traits},
		{"complex without sub attributes", with(func(a *model.ProfileSchemaAttribute) {
			a.ValueType = constants.ComplexDataType
		}), constants.Traits},
		{"display name too long", with(func(a *model.ProfileSchemaAttribute) {
			a.DisplayName = fmt.Sprintf("%0*d", constants.MaxAttributeDisplayNameLength+1, 1)
		}), constants.Traits},
		{"display name with a disallowed character", with(func(a *model.ProfileSchemaAttribute) {
			a.DisplayName = "City!"
		}), constants.Traits},
		{"application data without an application", trait("application_data.theme"), constants.ApplicationData},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.svc.AddProfileSchemaAttributesForScope(env.ctx, []model.ProfileSchemaAttribute{tc.attr},
				tc.scope, env.orgID)

			_ = requireClientErr(t, err, http.StatusBadRequest)
		})
	}
}

func TestAddAttributeRefusesADuplicateName(t *testing.T) {
	env := newSchemaEnv(t)
	env.add(t, constants.Traits, trait("traits.city"))

	_, err := env.svc.AddProfileSchemaAttributesForScope(env.ctx, []model.ProfileSchemaAttribute{trait("traits.city")},
		constants.Traits, env.orgID)

	clientError := requireClientErr(t, err, http.StatusConflict)
	assert.Equal(t, errors2.SCHEMA_ATTRIBUTE_ALREADY_EXISTS.Code, clientError.Code)
}

func TestApplicationDataAttributesAreScopedToTheirApplication(t *testing.T) {
	env := newSchemaEnv(t)
	restore := OverrideValidateApplicationIdentifierForTest(
		func(_ context.Context, appIdentifier string, _ string) (error, bool) {
			return nil, appIdentifier != "unknown-app"
		})
	t.Cleanup(restore)
	theme := func(appID string) model.ProfileSchemaAttribute {
		a := trait("application_data.theme")
		a.AttributeId = "id-theme-" + appID
		a.ApplicationIdentifier = appID
		return a
	}

	env.add(t, constants.ApplicationData, theme("app-1"))

	_, err := env.svc.AddProfileSchemaAttributesForScope(env.ctx, []model.ProfileSchemaAttribute{theme("app-1")},
		constants.ApplicationData, env.orgID)
	_ = requireClientErr(t, err, http.StatusConflict)

	_, err = env.svc.AddProfileSchemaAttributesForScope(env.ctx, []model.ProfileSchemaAttribute{theme("unknown-app")},
		constants.ApplicationData, env.orgID)
	clientError := requireClientErr(t, err, http.StatusBadRequest)
	assert.Equal(t, errors2.INVALID_APP_IDENTIFIER.Code, clientError.Code)

	// The same name under another application is a different attribute.
	assert.NotPanics(t, func() { env.add(t, constants.ApplicationData, theme("app-2")) })

	grouped, err := env.svc.GetProfileSchemaAttributesByScope(env.ctx, env.orgID, constants.ApplicationData)
	require.NoError(t, err)
	byApp, ok := grouped.(map[string][]model.ProfileSchemaAttribute)
	require.True(t, ok)
	assert.Len(t, byApp["app-1"], 1)
	assert.Len(t, byApp["app-2"], 1)
}

func TestComplexAttributeNeedsExistingSubAttributesOneLevelDeeper(t *testing.T) {
	env := newSchemaEnv(t)
	subs := env.add(t, constants.Traits, trait("traits.address.city"))
	address := func(subName, subId string) model.ProfileSchemaAttribute {
		a := trait("traits.address")
		a.ValueType = constants.ComplexDataType
		a.SubAttributes = []model.SubAttribute{{AttributeId: subId, AttributeName: subName}}
		return a
	}

	_, err := env.svc.AddProfileSchemaAttributesForScope(env.ctx,
		[]model.ProfileSchemaAttribute{address("traits.other.city", subs[0].AttributeId)}, constants.Traits, env.orgID)
	_ = requireClientErr(t, err, http.StatusBadRequest)

	_, err = env.svc.AddProfileSchemaAttributesForScope(env.ctx,
		[]model.ProfileSchemaAttribute{address("traits.address.city", "missing-id")}, constants.Traits, env.orgID)
	_ = requireClientErr(t, err, http.StatusBadRequest)

	_, err = env.svc.AddProfileSchemaAttributesForScope(env.ctx,
		[]model.ProfileSchemaAttribute{address("traits.address.zip", subs[0].AttributeId)}, constants.Traits, env.orgID)
	_ = requireClientErr(t, err, http.StatusBadRequest)

	stored := env.add(t, constants.Traits, address("traits.address.city", subs[0].AttributeId))
	assert.Equal(t, constants.ComplexDataType, stored[0].ValueType)
}

func TestASubAttributeCannotBeDeletedWhileItsParentListsIt(t *testing.T) {
	env := newSchemaEnv(t)
	subs := env.add(t, constants.Traits, trait("traits.address.city"))
	address := trait("traits.address")
	address.ValueType = constants.ComplexDataType
	address.SubAttributes = []model.SubAttribute{{AttributeId: subs[0].AttributeId,
		AttributeName: "traits.address.city"}}
	env.add(t, constants.Traits, address)

	err := env.svc.DeleteProfileSchemaAttributeById(env.ctx, env.orgID, subs[0].AttributeId)

	_ = requireClientErr(t, err, http.StatusBadRequest)
}

func TestDeleteAttributeIsIdempotent(t *testing.T) {
	env := newSchemaEnv(t)
	stored := env.add(t, constants.Traits, trait("traits.city"))

	require.NoError(t, env.svc.DeleteProfileSchemaAttributeById(env.ctx, env.orgID, stored[0].AttributeId))
	assert.NoError(t, env.svc.DeleteProfileSchemaAttributeById(env.ctx, env.orgID, stored[0].AttributeId))

	byName, err := env.svc.GetProfileSchemaAttributeByName(env.ctx, "traits.city", env.orgID)
	require.NoError(t, err)
	assert.Nil(t, byName)
}

func TestGetProfileSchemaGroupsAttributesByScope(t *testing.T) {
	env := newSchemaEnv(t)
	restore := OverrideValidateApplicationIdentifierForTest(
		func(_ context.Context, _ string, _ string) (error, bool) { return nil, true })
	t.Cleanup(restore)
	env.add(t, constants.Traits, trait("traits.city"))
	app := trait("application_data.theme")
	app.ApplicationIdentifier = "app-1"
	env.add(t, constants.ApplicationData, app)

	schema, err := env.svc.GetProfileSchema(env.ctx, env.orgID)

	require.NoError(t, err)
	assert.Contains(t, schema, "profile_id")
	assert.Contains(t, schema, "user_id")
	meta, ok := schema["meta"].(map[string]map[string]string)
	require.True(t, ok)
	assert.Contains(t, meta, "created_at")
	traits, ok := schema[constants.Traits].([]model.ProfileSchemaAttribute)
	require.True(t, ok)
	require.Len(t, traits, 1)
	assert.Equal(t, "traits.city", traits[0].AttributeName)
	apps, ok := schema[constants.ApplicationData].(map[string][]model.ProfileSchemaAttribute)
	require.True(t, ok)
	assert.Len(t, apps["app-1"], 1)
	assert.Empty(t, schema[constants.IdentityAttributes])
}

func TestGetAttributesByScopeAndFilter(t *testing.T) {
	env := newSchemaEnv(t)
	env.add(t, constants.Traits, trait("traits.city"), trait("traits.country"), trait("traits.age"))

	tests := []struct {
		name    string
		filters []string
		want    int
	}{
		{"eq", []string{"attribute_name eq traits.city"}, 1},
		{"co", []string{"attribute_name co c"}, 2},
		{"sw", []string{"attribute_name sw traits.co"}, 1},
		{"both conditions", []string{"attribute_name co c", "attribute_name sw traits.ci"}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := env.svc.GetProfileSchemaAttributesByScopeAndFilter(env.ctx, env.orgID, constants.Traits,
				tc.filters)

			require.NoError(t, err)
			attrs, ok := got.([]model.ProfileSchemaAttribute)
			require.True(t, ok)
			assert.Len(t, attrs, tc.want)
		})
	}
}

func TestDeleteAttributesByScopeAndWholeSchema(t *testing.T) {
	env := newSchemaEnv(t)
	env.add(t, constants.Traits, trait("traits.city"), trait("traits.age"))

	require.NoError(t, env.svc.DeleteProfileSchemaAttributesByScope(env.ctx, env.orgID, constants.Traits))
	got, err := env.svc.GetProfileSchemaAttributesByScope(env.ctx, env.orgID, constants.Traits)
	require.NoError(t, err)
	assert.Empty(t, got)

	env.add(t, constants.Traits, trait("traits.city"))
	require.NoError(t, env.svc.DeleteProfileSchema(env.ctx, env.orgID))
	byName, err := env.svc.GetProfileSchemaAttributeByName(env.ctx, "traits.city", env.orgID)
	require.NoError(t, err)
	assert.Nil(t, byName)
}

func TestSchemaWritesAreRefusedInASubOrganization(t *testing.T) {
	env := newSchemaEnv(t)
	_, err := env.db.Exec(`INSERT INTO organizations (org_id, org_handle, primary_org_id, status)
		VALUES ('sub-org', 'sub', $1, 'ACTIVE')`, env.orgID)
	require.NoError(t, err)

	_, err = env.svc.AddProfileSchemaAttributesForScope(env.ctx, []model.ProfileSchemaAttribute{trait("traits.city")},
		constants.Traits, "sub-org")
	_ = requireClientErr(t, err, http.StatusForbidden)

	_ = requireClientErr(t, env.svc.DeleteProfileSchemaAttributeById(env.ctx, "sub-org", "any"), http.StatusForbidden)
	_ = requireClientErr(t, env.svc.DeleteProfileSchemaAttributesByScope(env.ctx, "sub-org", constants.Traits),
		http.StatusForbidden)
	_ = requireClientErr(t, env.svc.DeleteProfileSchema(env.ctx, "sub-org"), http.StatusForbidden)
}
