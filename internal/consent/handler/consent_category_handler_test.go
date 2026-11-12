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

	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaStore "github.com/wso2/identity-customer-data-service/internal/profile_schema/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

var consentHandlerOrgCounter atomic.Int64

type consentHandlerEnv struct {
	db     *sql.DB
	handle string
	orgID  string
	auth   setup.AuthStub
	h      *ConsentCategoryHandler
}

func newConsentHandlerEnv(t *testing.T) *consentHandlerEnv {
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
	auth := setup.InstallAuthStub(t, "consent_category:view", "consent_category:create",
		"consent_category:update", "consent_category:delete")

	handle := fmt.Sprintf("acme-%d", consentHandlerOrgCounter.Add(1))
	env := &consentHandlerEnv{db: testDB.DB, handle: handle, orgID: setup.TestOrgId(handle), auth: auth,
		h: NewConsentCategoryHandler()}
	setup.TestOrg(t, handle)
	require.NoError(t, schemaStore.AddProfileSchemaAttributesForScope(context.Background(),
		[]schemaModel.ProfileSchemaAttribute{{
			AttributeId:   "attr-age",
			AttributeName: "traits.age",
			Scope:         constants.Traits,
			ValueType:     constants.IntegerDataType,
			MergeStrategy: "overwrite",
			Mutability:    constants.MutabilityReadWrite,
		}}, constants.Traits, env.orgID))
	return env
}

func (e *consentHandlerEnv) call(t *testing.T, handler http.HandlerFunc, method, token, body string,
	pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/t/"+e.handle+"/cds/api/v1/consent-categories", strings.NewReader(body))
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

func categoryID(id string) map[string]string { return map[string]string{"categoryId": id} }

func (e *consentHandlerEnv) add(t *testing.T, name string) consentModel.ConsentCategoryResponse {
	t.Helper()
	body := fmt.Sprintf(`{"category_name":%q,"purpose":"profiling","attributes":[{"attribute_name":"traits.age"}]}`,
		name)
	recorder := e.call(t, e.h.AddConsentCategory, http.MethodPost, e.auth.ValidToken, body, nil)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	var created consentModel.ConsentCategoryResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &created))
	return created
}

func TestAddConsentCategory(t *testing.T) {
	env := newConsentHandlerEnv(t)

	created := env.add(t, "Analytics")

	assert.NotEmpty(t, created.CategoryIdentifier)
	assert.Equal(t, "Analytics", created.CategoryName)
	assert.Equal(t, "profiling", created.Purpose)
	require.Len(t, created.Attributes, 1)
	assert.Equal(t, "traits.age", created.Attributes[0].AttributeName)
	assert.False(t, created.IsMandatory)
}

func TestAddConsentCategoryRefusals(t *testing.T) {
	env := newConsentHandlerEnv(t)
	env.add(t, "Analytics")
	tests := map[string]struct {
		body   string
		status int
	}{
		"malformed JSON":     {`{"category_name":`, http.StatusBadRequest},
		"no purpose":         {`{"category_name":"X"}`, http.StatusBadRequest},
		"an unknown purpose": {`{"category_name":"X","purpose":"marketing"}`, http.StatusBadRequest},
		"an unknown attribute": {`{"category_name":"X","purpose":"profiling",` +
			`"attributes":[{"attribute_name":"traits.no"}]}`, http.StatusBadRequest},
		"a name that is taken": {`{"category_name":"Analytics","purpose":"profiling"}`, http.StatusConflict},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := env.call(t, env.h.AddConsentCategory, http.MethodPost, env.auth.ValidToken, tc.body, nil)

			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}
}

func TestGetAllConsentCategories(t *testing.T) {
	env := newConsentHandlerEnv(t)

	empty := env.call(t, env.h.GetAllConsentCategories, http.MethodGet, env.auth.ValidToken, "", nil)
	require.Equal(t, http.StatusOK, empty.Code)
	assert.JSONEq(t, `[]`, empty.Body.String())

	env.add(t, "Analytics")
	env.add(t, "Offers")
	all := env.call(t, env.h.GetAllConsentCategories, http.MethodGet, env.auth.ValidToken, "", nil)
	require.Equal(t, http.StatusOK, all.Code, all.Body.String())
	var categories []consentModel.ConsentCategoryResponse
	require.NoError(t, json.Unmarshal(all.Body.Bytes(), &categories))
	assert.Len(t, categories, 2)
}

func TestGetConsentCategory(t *testing.T) {
	env := newConsentHandlerEnv(t)
	created := env.add(t, "Analytics")

	found := env.call(t, env.h.GetConsentCategory, http.MethodGet, env.auth.ValidToken, "",
		categoryID(created.CategoryIdentifier))
	require.Equal(t, http.StatusOK, found.Code, found.Body.String())
	var got consentModel.ConsentCategoryResponse
	require.NoError(t, json.Unmarshal(found.Body.Bytes(), &got))
	assert.Equal(t, created.CategoryIdentifier, got.CategoryIdentifier)

	missing := env.call(t, env.h.GetConsentCategory, http.MethodGet, env.auth.ValidToken, "", categoryID("missing"))
	assert.Equal(t, http.StatusNotFound, missing.Code)

	noID := env.call(t, env.h.GetConsentCategory, http.MethodGet, env.auth.ValidToken, "", nil)
	assert.Equal(t, http.StatusBadRequest, noID.Code)
}

func TestUpdateConsentCategory(t *testing.T) {
	env := newConsentHandlerEnv(t)
	created := env.add(t, "Analytics")
	ids := categoryID(created.CategoryIdentifier)

	updated := env.call(t, env.h.UpdateConsentCategory, http.MethodPut, env.auth.ValidToken,
		`{"category_name":"Analytics v2","purpose":"personalization"}`, ids)
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	var got consentModel.ConsentCategoryResponse
	require.NoError(t, json.Unmarshal(updated.Body.Bytes(), &got))
	assert.Equal(t, "Analytics v2", got.CategoryName)
	assert.Equal(t, created.CategoryIdentifier, got.CategoryIdentifier)

	tests := map[string]struct {
		body   string
		ids    map[string]string
		status int
	}{
		"malformed JSON":  {`{"category_name":`, ids, http.StatusBadRequest},
		"a bad purpose":   {`{"category_name":"X","purpose":"nope"}`, ids, http.StatusBadRequest},
		"an unknown ID":   {`{"category_name":"X","purpose":"profiling"}`, categoryID("missing"), http.StatusNotFound},
		"no ID in a path": {`{"category_name":"X","purpose":"profiling"}`, nil, http.StatusBadRequest},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := env.call(t, env.h.UpdateConsentCategory, http.MethodPut, env.auth.ValidToken, tc.body, tc.ids)

			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}
}

func TestDeleteConsentCategory(t *testing.T) {
	env := newConsentHandlerEnv(t)
	created := env.add(t, "Analytics")
	ids := categoryID(created.CategoryIdentifier)

	first := env.call(t, env.h.DeleteConsentCategory, http.MethodDelete, env.auth.ValidToken, "", ids)
	assert.Equal(t, http.StatusNoContent, first.Code)
	again := env.call(t, env.h.DeleteConsentCategory, http.MethodDelete, env.auth.ValidToken, "", ids)
	assert.Equal(t, http.StatusNoContent, again.Code)

	get := env.call(t, env.h.GetConsentCategory, http.MethodGet, env.auth.ValidToken, "", ids)
	assert.Equal(t, http.StatusNotFound, get.Code)

	noID := env.call(t, env.h.DeleteConsentCategory, http.MethodDelete, env.auth.ValidToken, "", nil)
	assert.Equal(t, http.StatusBadRequest, noID.Code)
}

func TestConsentCategoryHandlersRequireAuthentication(t *testing.T) {
	env := newConsentHandlerEnv(t)
	created := env.add(t, "Analytics")
	ids := categoryID(created.CategoryIdentifier)
	calls := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		body    string
	}{
		{"list", env.h.GetAllConsentCategories, http.MethodGet, ""},
		{"add", env.h.AddConsentCategory, http.MethodPost, `{"category_name":"X","purpose":"profiling"}`},
		{"get", env.h.GetConsentCategory, http.MethodGet, ""},
		{"update", env.h.UpdateConsentCategory, http.MethodPut, `{"category_name":"X","purpose":"profiling"}`},
		{"delete", env.h.DeleteConsentCategory, http.MethodDelete, ""},
	}
	for _, tc := range calls {
		t.Run(tc.name+" without a token", func(t *testing.T) {
			assert.Equal(t, http.StatusUnauthorized, env.call(t, tc.handler, tc.method, "", tc.body, ids).Code)
		})
		t.Run(tc.name+" with the wrong scope", func(t *testing.T) {
			assert.Equal(t, http.StatusForbidden, env.call(t, tc.handler, tc.method, env.auth.OtherToken, tc.body,
				ids).Code)
		})
	}
}

func TestTheMandatoryCategoryIsMarkedAndProtected(t *testing.T) {
	env := newConsentHandlerEnv(t)
	_, err := env.db.Exec(`INSERT INTO consent_categories (category_name, category_identifier, org_id, purpose,
		destinations, is_mandatory) VALUES ('Identity Data', 'mandatory-1', ?, 'profiling', '[]', 1)`, env.orgID)
	require.NoError(t, err)
	ids := categoryID("mandatory-1")

	get := env.call(t, env.h.GetConsentCategory, http.MethodGet, env.auth.ValidToken, "", ids)
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	var got consentModel.ConsentCategoryResponse
	require.NoError(t, json.Unmarshal(get.Body.Bytes(), &got))
	assert.True(t, got.IsMandatory)

	update := env.call(t, env.h.UpdateConsentCategory, http.MethodPut, env.auth.ValidToken,
		`{"category_name":"X","purpose":"profiling"}`, ids)
	assert.Equal(t, http.StatusForbidden, update.Code)
	remove := env.call(t, env.h.DeleteConsentCategory, http.MethodDelete, env.auth.ValidToken, "", ids)
	assert.Equal(t, http.StatusForbidden, remove.Code)
}
