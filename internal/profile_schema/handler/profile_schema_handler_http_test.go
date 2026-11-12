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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

var schemaOrgCounter atomic.Int64

// schemaHandlerEnv is an organization with CDS enabled and a stub identity server, so that a
// handler runs with its authentication and authorization unmodified.
type schemaHandlerEnv struct {
	db     *sql.DB
	handle string
	orgID  string
	auth   setup.AuthStub
	h      *ProfileSchemaHandler
}

func newSchemaHandlerEnv(t *testing.T) *schemaHandlerEnv {
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
	auth := setup.InstallAuthStub(t, "profile_schema:create", "profile_schema:view", "profile_schema:update",
		"profile_schema:delete", "profile_schema:share_view", "profile_schema:share_update")

	handle := fmt.Sprintf("acme-%d", schemaOrgCounter.Add(1))
	env := &schemaHandlerEnv{db: testDB.DB, handle: handle, orgID: setup.TestOrgId(handle), auth: auth,
		h: NewProfileSchemaHandler()}
	setup.TestOrg(t, handle)
	_, err = env.db.Exec(`INSERT INTO cds_config (org_id, config, value) VALUES (?, 'cds_enabled', 'true')`, env.orgID)
	require.NoError(t, err)
	return env
}

func (e *schemaHandlerEnv) call(t *testing.T, handler http.HandlerFunc, method, token, target, body string,
	pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/t/"+e.handle+"/cds/api/v1/profile-schema"+target,
		strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), constants.TenantContextKey, e.handle))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("Content-Type", "application/json")
	for name, value := range pathValues {
		request.SetPathValue(name, value)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func attributeBody(names ...string) string {
	items := make([]string, 0, len(names))
	for _, name := range names {
		items = append(items, fmt.Sprintf(`{"attribute_name":%q,"value_type":"string","merge_strategy":"overwrite"}`,
			name))
	}
	return "[" + strings.Join(items, ",") + "]"
}

func (e *schemaHandlerEnv) addTraits(t *testing.T, names ...string) []model.ProfileSchemaAttribute {
	t.Helper()
	recorder := e.call(t, e.h.AddProfileSchemaAttributesForScope, http.MethodPost, e.auth.ValidToken, "/traits",
		attributeBody(names...), map[string]string{"scope": constants.Traits})
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	var attrs []model.ProfileSchemaAttribute
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &attrs))
	return attrs
}

func traitScope() map[string]string { return map[string]string{"scope": constants.Traits} }

func TestAddProfileSchemaAttributesCreatesThemAndGeneratesTheIDs(t *testing.T) {
	env := newSchemaHandlerEnv(t)

	attrs := env.addTraits(t, "traits.city", "traits.age")

	require.Len(t, attrs, 2)
	for _, attr := range attrs {
		assert.NotEmpty(t, attr.AttributeId, "the handler generates the ID")
		assert.Equal(t, constants.MutabilityReadWrite, attr.Mutability, "the mutability defaults to readWrite")
		assert.Empty(t, attr.OrgId, "the organization is not part of the response")
		assert.NotEmpty(t, attr.DisplayName)
	}
}

func TestAddProfileSchemaAttributesRefusals(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	env.addTraits(t, "traits.city")

	tests := []struct {
		name   string
		scope  string
		body   string
		status int
	}{
		{"malformed JSON", "traits", `[{"attribute_name":`, http.StatusBadRequest},
		{"an unknown field", "traits",
			`[{"attribute_name":"traits.x","value_type":"string","merge_strategy":"overwrite","color":"red"}]`,
			http.StatusBadRequest},
		{"an empty list", "traits", `[]`, http.StatusBadRequest},
		{"the identity scope", "identity_attributes", attributeBody("identity_attributes.x"), http.StatusBadRequest},
		{"an unknown scope", "secrets", attributeBody("secrets.x"), http.StatusBadRequest},
		{"an application on a trait", "traits", `[{"attribute_name":"traits.x","value_type":"string",` +
			`"merge_strategy":"overwrite","application_identifier":"app-1"}]`, http.StatusBadRequest},
		{"an invalid attribute", "traits",
			`[{"attribute_name":"traits.x","value_type":"blob","merge_strategy":"overwrite"}]`,
			http.StatusBadRequest},
		{"a duplicate name", "traits", attributeBody("traits.city"), http.StatusConflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := env.call(t, env.h.AddProfileSchemaAttributesForScope, http.MethodPost, env.auth.ValidToken,
				"/"+tc.scope, tc.body, map[string]string{"scope": tc.scope})

			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}
}

func TestProfileSchemaHandlersRequireAuthentication(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	attrs := env.addTraits(t, "traits.city")
	ids := map[string]string{"scope": constants.Traits, "attrID": attrs[0].AttributeId}

	calls := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		body    string
	}{
		{"add", env.h.AddProfileSchemaAttributesForScope, http.MethodPost, attributeBody("traits.x")},
		{"get the schema", env.h.GetProfileSchema, http.MethodGet, ""},
		{"get an attribute", env.h.GetProfileSchemaAttributeById, http.MethodGet, ""},
		{"get a scope", env.h.GetProfileSchemaAttributeForScope, http.MethodGet, ""},
		{"patch", env.h.PatchProfileSchemaAttributeById, http.MethodPatch, `{}`},
		{"delete the schema", env.h.DeleteProfileSchema, http.MethodDelete, ""},
		{"delete an attribute", env.h.DeleteProfileSchemaAttributeById, http.MethodDelete, ""},
		{"delete a scope", env.h.DeleteProfileSchemaAttributeForScope, http.MethodDelete, ""},
	}
	for _, tc := range calls {
		t.Run(tc.name+" without a token", func(t *testing.T) {
			recorder := env.call(t, tc.handler, tc.method, "", "", tc.body, ids)

			assert.Equal(t, http.StatusUnauthorized, recorder.Code)
		})
		t.Run(tc.name+" with the wrong scope", func(t *testing.T) {
			recorder := env.call(t, tc.handler, tc.method, env.auth.OtherToken, "", tc.body, ids)

			assert.Equal(t, http.StatusForbidden, recorder.Code)
		})
	}
}

func TestProfileSchemaHandlersRefuseAnOrganizationWithoutCDS(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	_, err := env.db.Exec(`DELETE FROM cds_config WHERE org_id = ?`, env.orgID)
	require.NoError(t, err)
	ids := map[string]string{"scope": constants.Traits, "attrID": "any"}

	for name, handler := range map[string]http.HandlerFunc{
		"add":              env.h.AddProfileSchemaAttributesForScope,
		"get the schema":   env.h.GetProfileSchema,
		"get an attribute": env.h.GetProfileSchemaAttributeById,
		"get a scope":      env.h.GetProfileSchemaAttributeForScope,
		"patch":            env.h.PatchProfileSchemaAttributeById,
		"delete schema":    env.h.DeleteProfileSchema,
		"delete attribute": env.h.DeleteProfileSchemaAttributeById,
		"delete scope":     env.h.DeleteProfileSchemaAttributeForScope,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := env.call(t, handler, http.MethodPost, env.auth.ValidToken, "", attributeBody("traits.x"), ids)

			assert.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}

func TestGetProfileSchemaReturnsTheCoreAndTheDefinedAttributes(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	env.addTraits(t, "traits.city")

	recorder := env.call(t, env.h.GetProfileSchema, http.MethodGet, env.auth.ValidToken, "", "", nil)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var schema map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &schema))
	for _, key := range []string{"profile_id", "user_id", "meta", "identity_attributes", "traits", "application_data"} {
		assert.Contains(t, schema, key)
	}
	var traits []model.ProfileSchemaAttribute
	require.NoError(t, json.Unmarshal(schema["traits"], &traits))
	require.Len(t, traits, 1)
	assert.Equal(t, "traits.city", traits[0].AttributeName)
}

func TestGetProfileSchemaAttributeById(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	attrs := env.addTraits(t, "traits.city")

	found := env.call(t, env.h.GetProfileSchemaAttributeById, http.MethodGet, env.auth.ValidToken, "", "",
		map[string]string{"scope": constants.Traits, "attrID": attrs[0].AttributeId})
	require.Equal(t, http.StatusOK, found.Code, found.Body.String())
	var attr model.ProfileSchemaAttribute
	require.NoError(t, json.Unmarshal(found.Body.Bytes(), &attr))
	assert.Equal(t, "traits.city", attr.AttributeName)

	missing := env.call(t, env.h.GetProfileSchemaAttributeById, http.MethodGet, env.auth.ValidToken, "", "",
		map[string]string{"scope": constants.Traits, "attrID": "missing"})
	assert.Equal(t, http.StatusNotFound, missing.Code)

	badScope := env.call(t, env.h.GetProfileSchemaAttributeById, http.MethodGet, env.auth.ValidToken, "", "",
		map[string]string{"scope": "secrets", "attrID": attrs[0].AttributeId})
	assert.Equal(t, http.StatusBadRequest, badScope.Code)
}

func TestGetProfileSchemaAttributeForScope(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	env.addTraits(t, "traits.city", "traits.country", "traits.age")

	all := env.call(t, env.h.GetProfileSchemaAttributeForScope, http.MethodGet, env.auth.ValidToken, "", "",
		traitScope())
	require.Equal(t, http.StatusOK, all.Code, all.Body.String())
	var attrs []model.ProfileSchemaAttribute
	require.NoError(t, json.Unmarshal(all.Body.Bytes(), &attrs))
	assert.Len(t, attrs, 3)

	filtered := env.call(t, env.h.GetProfileSchemaAttributeForScope, http.MethodGet, env.auth.ValidToken,
		"?filter=attribute_name+co+c+and+attribute_name+sw+traits.ci", "", traitScope())
	require.Equal(t, http.StatusOK, filtered.Code, filtered.Body.String())
	require.NoError(t, json.Unmarshal(filtered.Body.Bytes(), &attrs))
	assert.Len(t, attrs, 1)

	repeated := env.call(t, env.h.GetProfileSchemaAttributeForScope, http.MethodGet, env.auth.ValidToken,
		"?filter=attribute_name+co+c&filter=attribute_name+co+co", "", traitScope())
	require.Equal(t, http.StatusOK, repeated.Code, repeated.Body.String())
	require.NoError(t, json.Unmarshal(repeated.Body.Bytes(), &attrs))
	assert.Len(t, attrs, 1)

	badFilter := env.call(t, env.h.GetProfileSchemaAttributeForScope, http.MethodGet, env.auth.ValidToken,
		"?filter=attribute_name", "", traitScope())
	assert.Equal(t, http.StatusBadRequest, badFilter.Code)

	badScope := env.call(t, env.h.GetProfileSchemaAttributeForScope, http.MethodGet, env.auth.ValidToken, "", "",
		map[string]string{"scope": "secrets"})
	assert.Equal(t, http.StatusBadRequest, badScope.Code)
}

func TestPatchProfileSchemaAttributeById(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	attrs := env.addTraits(t, "traits.city")
	ids := map[string]string{"scope": constants.Traits, "attrID": attrs[0].AttributeId}
	body := `{"attribute_name":"traits.city","value_type":"string","merge_strategy":"overwrite",` +
		`"mutability":"writeOnce"}`

	recorder := env.call(t, env.h.PatchProfileSchemaAttributeById, http.MethodPatch, env.auth.ValidToken, "", body, ids)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var attr model.ProfileSchemaAttribute
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &attr))
	assert.Equal(t, constants.MutabilityWriteOnce, attr.Mutability)

	refusals := map[string]struct {
		body   string
		scope  string
		status int
	}{
		"malformed JSON":       {`{"mutability":`, constants.Traits, http.StatusBadRequest},
		"no updates":           {`{}`, constants.Traits, http.StatusBadRequest},
		"the identity scope":   {body, constants.IdentityAttributes, http.StatusBadRequest},
		"an unknown scope":     {body, "secrets", http.StatusBadRequest},
		"an unknown attribute": {body, constants.Traits, http.StatusNotFound},
	}
	for name, tc := range refusals {
		t.Run(name, func(t *testing.T) {
			attrID := attrs[0].AttributeId
			if tc.status == http.StatusNotFound {
				attrID = "missing"
			}
			recorder := env.call(t, env.h.PatchProfileSchemaAttributeById, http.MethodPatch, env.auth.ValidToken, "",
				tc.body, map[string]string{"scope": tc.scope, "attrID": attrID})

			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}
}

func TestDeleteProfileSchemaAttributeById(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	attrs := env.addTraits(t, "traits.city")
	ids := map[string]string{"scope": constants.Traits, "attrID": attrs[0].AttributeId}

	first := env.call(t, env.h.DeleteProfileSchemaAttributeById, http.MethodDelete, env.auth.ValidToken, "", "", ids)
	assert.Equal(t, http.StatusNoContent, first.Code)

	// An attribute that is gone is not an error.
	again := env.call(t, env.h.DeleteProfileSchemaAttributeById, http.MethodDelete, env.auth.ValidToken, "", "", ids)
	assert.Equal(t, http.StatusNoContent, again.Code)

	badScope := env.call(t, env.h.DeleteProfileSchemaAttributeById, http.MethodDelete, env.auth.ValidToken, "", "",
		map[string]string{"scope": "secrets", "attrID": "x"})
	assert.Equal(t, http.StatusBadRequest, badScope.Code)
}

func TestDeleteProfileSchemaAttributeForScope(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	env.addTraits(t, "traits.city", "traits.age")

	deleted := env.call(t, env.h.DeleteProfileSchemaAttributeForScope, http.MethodDelete, env.auth.ValidToken, "", "",
		traitScope())
	assert.Equal(t, http.StatusNoContent, deleted.Code)

	remaining := env.call(t, env.h.GetProfileSchemaAttributeForScope, http.MethodGet, env.auth.ValidToken, "", "",
		traitScope())
	require.Equal(t, http.StatusOK, remaining.Code)
	var attrs []model.ProfileSchemaAttribute
	require.NoError(t, json.Unmarshal(remaining.Body.Bytes(), &attrs))
	assert.Empty(t, attrs)

	identity := env.call(t, env.h.DeleteProfileSchemaAttributeForScope, http.MethodDelete, env.auth.ValidToken, "", "",
		map[string]string{"scope": constants.IdentityAttributes})
	assert.Equal(t, http.StatusMethodNotAllowed, identity.Code)

	badScope := env.call(t, env.h.DeleteProfileSchemaAttributeForScope, http.MethodDelete, env.auth.ValidToken, "", "",
		map[string]string{"scope": "secrets"})
	assert.Equal(t, http.StatusBadRequest, badScope.Code)
}

func TestDeleteProfileSchemaRemovesEveryAttribute(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	env.addTraits(t, "traits.city")

	deleted := env.call(t, env.h.DeleteProfileSchema, http.MethodDelete, env.auth.ValidToken, "", "", nil)
	assert.Equal(t, http.StatusNoContent, deleted.Code)

	remaining := env.call(t, env.h.GetProfileSchemaAttributeForScope, http.MethodGet, env.auth.ValidToken, "", "",
		traitScope())
	var attrs []model.ProfileSchemaAttribute
	require.NoError(t, json.Unmarshal(remaining.Body.Bytes(), &attrs))
	assert.Empty(t, attrs)
}
