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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	sharingModel "github.com/wso2/identity-customer-data-service/internal/sharing/model"
	sharingStore "github.com/wso2/identity-customer-data-service/internal/sharing/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

const childOrgID = "child-org-id"

// withChild stores a sub organization of the organization of the environment, with the operations
// that its sharing handlers authorize.
func (e *schemaHandlerEnv) withChild(t *testing.T) {
	t.Helper()
	_, err := e.db.Exec(`INSERT INTO organizations (org_id, org_handle, parent_org_id, primary_org_id, status)
		VALUES ($1, 'child', $2, $2, 'ACTIVE')`, childOrgID, e.orgID)
	require.NoError(t, err)
	// CDS is enabled in a sub organization once the access policy of its primary organization reaches it.
	require.NoError(t, sharingStore.CreatePolicy(context.Background(), sharingModel.Policy{
		PolicyId:        "access-policy",
		ResourceType:    sharingModel.ResourceOrganizationAccess,
		ResourceId:      sharingModel.OrganizationAccessResourceId,
		OwningOrgId:     e.orgID,
		InitiatingOrgId: e.orgID,
		Targets:         []sharingModel.Target{{Scope: sharingModel.ScopeAllChildren, OrgId: e.orgID}},
	}))
}

// callAsChild runs a handler on a request in the sub organization, with a token of that organization.
func (e *schemaHandlerEnv) callAsChild(t *testing.T, handler http.HandlerFunc, method, body string,
	pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	token := e.auth.NewJWT(t, map[string]interface{}{"org_id": childOrgID})
	request := httptest.NewRequest(method, "/t/"+e.handle+"/o/"+childOrgID+"/cds/api/v1/profile-schema",
		strings.NewReader(body))
	ctx := context.WithValue(request.Context(), constants.TenantContextKey, "child")
	ctx = context.WithValue(ctx, constants.OrgIdContextKey, childOrgID)
	request = request.WithContext(ctx)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	for name, value := range pathValues {
		request.SetPathValue(name, value)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func policyIDs(attributeID, policyID string) map[string]string {
	return map[string]string{"scope": constants.Traits, "attrID": attributeID, "policyId": policyID}
}

func TestSchemaAttributeSharingPolicyLifecycle(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	env.withChild(t)
	attrs := env.addTraits(t, "traits.city")
	scope := map[string]string{"scope": constants.Traits, "attrID": attrs[0].AttributeId}

	created := env.call(t, env.h.CreateSharingPolicy, http.MethodPost, env.auth.ValidToken, "",
		`{"child_orgs":{"mode":"all"}}`, scope)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var policy struct {
		ID           string `json:"id"`
		ResourceType string `json:"resource_type"`
		ResourceID   string `json:"resource_id"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &policy))
	require.NotEmpty(t, policy.ID)
	assert.Equal(t, "SCHEMA_ATTRIBUTE", policy.ResourceType)
	assert.Equal(t, attrs[0].AttributeId, policy.ResourceID)
	ids := policyIDs(attrs[0].AttributeId, policy.ID)

	again := env.call(t, env.h.CreateSharingPolicy, http.MethodPost, env.auth.ValidToken, "",
		`{"child_orgs":{"mode":"all"}}`, scope)
	assert.Equal(t, http.StatusConflict, again.Code, "an attribute has one policy for an organization")

	list := env.call(t, env.h.ListSharingPolicies, http.MethodGet, env.auth.ValidToken, "", "", scope)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assert.Contains(t, list.Body.String(), `"total_results":1`)

	got := env.call(t, env.h.GetSharingPolicy, http.MethodGet, env.auth.ValidToken, "", "", ids)
	require.Equal(t, http.StatusOK, got.Code, got.Body.String())
	assert.Contains(t, got.Body.String(), policy.ID)

	selected := `{"child_orgs":{"mode":"selected","orgs":[{"org_id":"` + childOrgID + `","reach":"org"}]}}`
	updated := env.call(t, env.h.UpdateSharingPolicy, http.MethodPut, env.auth.ValidToken, "", selected, ids)
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	assert.Contains(t, updated.Body.String(), childOrgID)

	removed := env.call(t, env.h.DeleteSharingPolicy, http.MethodDelete, env.auth.ValidToken, "", "", ids)
	assert.Equal(t, http.StatusNoContent, removed.Code)
	missing := env.call(t, env.h.GetSharingPolicy, http.MethodGet, env.auth.ValidToken, "", "", ids)
	assert.Equal(t, http.StatusNotFound, missing.Code)
}

func TestSchemaAttributeSharingPolicyRefusals(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	env.withChild(t)
	attrs := env.addTraits(t, "traits.city")
	scope := map[string]string{"scope": constants.Traits, "attrID": attrs[0].AttributeId}

	tests := []struct {
		name   string
		body   string
		scope  map[string]string
		status int
	}{
		{"malformed JSON", `{"child_orgs":`, scope, http.StatusBadRequest},
		{"an unknown field", `{"color":"red"}`, scope, http.StatusBadRequest},
		{"no child_orgs", `{}`, scope, http.StatusBadRequest},
		{"an organization that is not a child",
			`{"child_orgs":{"mode":"selected","orgs":[{"org_id":"stranger","reach":"org"}]}}`,
			scope, http.StatusBadRequest},
		{"an attribute that does not exist", `{"child_orgs":{"mode":"all"}}`,
			map[string]string{"scope": constants.Traits, "attrID": "missing"}, http.StatusNotFound},
		{"an attribute of another scope", `{"child_orgs":{"mode":"all"}}`,
			map[string]string{"scope": constants.IdentityAttributes, "attrID": attrs[0].AttributeId},
			http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := env.call(t, env.h.CreateSharingPolicy, http.MethodPost, env.auth.ValidToken, "", tc.body,
				tc.scope)

			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}

	unknownPolicy := policyIDs(attrs[0].AttributeId, "missing")
	assert.Equal(t, http.StatusNotFound,
		env.call(t, env.h.GetSharingPolicy, http.MethodGet, env.auth.ValidToken, "", "", unknownPolicy).Code)
	assert.Equal(t, http.StatusNotFound, env.call(t, env.h.UpdateSharingPolicy, http.MethodPut, env.auth.ValidToken, "",
		`{"child_orgs":{"mode":"all"}}`, unknownPolicy).Code)
	assert.Equal(t, http.StatusNotFound,
		env.call(t, env.h.DeleteSharingPolicy, http.MethodDelete, env.auth.ValidToken, "", "", unknownPolicy).Code)
}

func TestSchemaSharingHandlersRequireAuthenticationAndCDS(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	ids := policyIDs("any", "any")
	calls := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		body    string
	}{
		{"create", env.h.CreateSharingPolicy, http.MethodPost, `{"child_orgs":{"mode":"all"}}`},
		{"list", env.h.ListSharingPolicies, http.MethodGet, ""},
		{"get", env.h.GetSharingPolicy, http.MethodGet, ""},
		{"update", env.h.UpdateSharingPolicy, http.MethodPut, `{"child_orgs":{"mode":"all"}}`},
		{"delete", env.h.DeleteSharingPolicy, http.MethodDelete, ""},
	}
	for _, tc := range calls {
		t.Run(tc.name+" without a token", func(t *testing.T) {
			assert.Equal(t, http.StatusUnauthorized, env.call(t, tc.handler, tc.method, "", "", tc.body, ids).Code)
		})
	}
}

func TestASharedAttributeIsVisibleToTheChildButCannotBeChangedThere(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	env.withChild(t)
	attrs := env.addTraits(t, "traits.city", "traits.age")
	shared := attrs[0]
	scope := map[string]string{"scope": constants.Traits, "attrID": shared.AttributeId}
	created := env.call(t, env.h.CreateSharingPolicy, http.MethodPost, env.auth.ValidToken, "",
		`{"child_orgs":{"mode":"all"}}`, scope)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())

	// The child sees the shared attribute, marked as shared.
	view := env.callAsChild(t, env.h.GetProfileSchema, http.MethodGet, "", nil)
	require.Equal(t, http.StatusOK, view.Code, view.Body.String())
	var schema struct {
		Traits []model.ProfileSchemaAttribute `json:"traits"`
	}
	require.NoError(t, json.Unmarshal(view.Body.Bytes(), &schema))
	require.Len(t, schema.Traits, 1, "only the attribute that was shared")
	assert.Equal(t, "traits.city", schema.Traits[0].AttributeName)
	assert.Equal(t, "SHARED", schema.Traits[0].Origin)

	byScope := env.callAsChild(t, env.h.GetProfileSchemaAttributeForScope, http.MethodGet, "",
		map[string]string{"scope": constants.Traits})
	require.Equal(t, http.StatusOK, byScope.Code, byScope.Body.String())
	var inScope []model.ProfileSchemaAttribute
	require.NoError(t, json.Unmarshal(byScope.Body.Bytes(), &inScope))
	require.Len(t, inScope, 1)

	byID := env.callAsChild(t, env.h.GetProfileSchemaAttributeById, http.MethodGet, "", scope)
	assert.Equal(t, http.StatusOK, byID.Code, byID.Body.String())

	// It is read-only there, and the child cannot write the schema at all.
	remove := env.callAsChild(t, env.h.DeleteProfileSchemaAttributeById, http.MethodDelete, "", scope)
	assert.Equal(t, http.StatusForbidden, remove.Code)
	add := env.callAsChild(t, env.h.AddProfileSchemaAttributesForScope, http.MethodPost, attributeBody("traits.mine"),
		map[string]string{"scope": constants.Traits})
	assert.Equal(t, http.StatusForbidden, add.Code)
	share := env.callAsChild(t, env.h.CreateSharingPolicy, http.MethodPost, `{"child_orgs":{"mode":"all"}}`, scope)
	assert.Equal(t, http.StatusForbidden, share.Code)
}

func TestAnAttributeThatIsNotSharedIsInvisibleToTheChild(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	env.withChild(t)
	attrs := env.addTraits(t, "traits.city")

	view := env.callAsChild(t, env.h.GetProfileSchemaAttributeForScope, http.MethodGet, "",
		map[string]string{"scope": constants.Traits})
	require.Equal(t, http.StatusOK, view.Code, view.Body.String())
	var inScope []model.ProfileSchemaAttribute
	require.NoError(t, json.Unmarshal(view.Body.Bytes(), &inScope))
	assert.Empty(t, inScope)

	byID := env.callAsChild(t, env.h.GetProfileSchemaAttributeById, http.MethodGet, "",
		map[string]string{"scope": constants.Traits, "attrID": attrs[0].AttributeId})
	assert.Equal(t, http.StatusNotFound, byID.Code)
}

func TestSchemaSharingRefusesAttributesThatCannotBeShared(t *testing.T) {
	env := newSchemaHandlerEnv(t)
	env.withChild(t)
	env.addTraits(t, "traits.address.city")
	subAttribute := env.call(t, env.h.GetProfileSchemaAttributeForScope, http.MethodGet, env.auth.ValidToken, "", "",
		traitScope())
	require.Equal(t, http.StatusOK, subAttribute.Code)
	var inScope []model.ProfileSchemaAttribute
	require.NoError(t, json.Unmarshal(subAttribute.Body.Bytes(), &inScope))
	require.Len(t, inScope, 1)

	// A sub-attribute, which has two dots in its name, is not shared.
	recorder := env.call(t, env.h.CreateSharingPolicy, http.MethodPost, env.auth.ValidToken, "",
		`{"child_orgs":{"mode":"all"}}`, map[string]string{"scope": constants.Traits, "attrID": inScope[0].AttributeId})

	assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
}
