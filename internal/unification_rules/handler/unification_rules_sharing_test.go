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

	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	sharingModel "github.com/wso2/identity-customer-data-service/internal/sharing/model"
	sharingStore "github.com/wso2/identity-customer-data-service/internal/sharing/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
)

const ruleChildOrgID = "child-org-id"

// withChild stores a sub organization that the access policy of the organization reaches.
func (e *handlerEnv) withChild(t *testing.T) {
	t.Helper()
	_, err := e.db.Exec(`INSERT INTO organizations (org_id, org_handle, parent_org_id, primary_org_id, status)
		VALUES ($1, 'child', $2, $2, 'ACTIVE')`, ruleChildOrgID, e.orgID)
	require.NoError(t, err)
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
func (e *handlerEnv) callAsChild(t *testing.T, handler http.HandlerFunc, method, body string,
	pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	token := e.auth.NewJWT(t, map[string]interface{}{"org_id": ruleChildOrgID})
	request := httptest.NewRequest(method, "/t/"+e.handle+"/o/"+ruleChildOrgID+"/cds/api/v1/unification-rules",
		strings.NewReader(body))
	ctx := context.WithValue(request.Context(), constants.TenantContextKey, "child")
	ctx = context.WithValue(ctx, constants.OrgIdContextKey, ruleChildOrgID)
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

// shareAttribute shares a trait of the organization with all its child organizations.
func (e *handlerEnv) shareAttribute(t *testing.T, attributeID string) {
	t.Helper()
	ctx := context.Background()
	org, err := orgService.GetOrganizationById(ctx, e.orgID)
	require.NoError(t, err)
	require.NotNil(t, org)
	_, err = schemaService.GetProfileSchemaService().CreateSharingPolicy(ctx, *org, constants.Traits, attributeID,
		sharingModel.PolicyRequest{ChildOrgs: &sharingModel.ChildOrgs{Mode: sharingModel.ChildOrgsModeAll}})
	require.NoError(t, err)
}

func rulePolicyIDs(ruleID, policyID string) map[string]string {
	return map[string]string{"ruleId": ruleID, "policyId": policyID}
}

func TestRuleSharingPolicyLifecycle(t *testing.T) {
	env := newHandlerEnv(t)
	env.withChild(t)
	rule := env.addRule(t, "City", "traits.city", 1)
	ruleScope := map[string]string{"ruleId": rule.RuleId}

	created := env.call(t, env.h.CreateSharingPolicy, http.MethodPost, env.auth.ValidToken,
		`{"child_orgs":{"mode":"all"}}`, ruleScope)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var policy struct {
		ID           string `json:"id"`
		ResourceType string `json:"resource_type"`
		ResourceID   string `json:"resource_id"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &policy))
	require.NotEmpty(t, policy.ID)
	assert.Equal(t, "UNIFICATION_RULE", policy.ResourceType)
	assert.Equal(t, rule.RuleId, policy.ResourceID)
	ids := rulePolicyIDs(rule.RuleId, policy.ID)

	again := env.call(t, env.h.CreateSharingPolicy, http.MethodPost, env.auth.ValidToken,
		`{"child_orgs":{"mode":"all"}}`, ruleScope)
	assert.Equal(t, http.StatusConflict, again.Code)

	list := env.call(t, env.h.ListSharingPolicies, http.MethodGet, env.auth.ValidToken, "", ruleScope)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assert.Contains(t, list.Body.String(), `"total_results":1`)

	got := env.call(t, env.h.GetSharingPolicy, http.MethodGet, env.auth.ValidToken, "", ids)
	require.Equal(t, http.StatusOK, got.Code, got.Body.String())

	selected := `{"child_orgs":{"mode":"selected","orgs":[{"org_id":"` + ruleChildOrgID + `","reach":"subtree"}]}}`
	updated := env.call(t, env.h.UpdateSharingPolicy, http.MethodPut, env.auth.ValidToken, selected, ids)
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	assert.Contains(t, updated.Body.String(), "subtree")

	removed := env.call(t, env.h.DeleteSharingPolicy, http.MethodDelete, env.auth.ValidToken, "", ids)
	assert.Equal(t, http.StatusNoContent, removed.Code)
	missing := env.call(t, env.h.GetSharingPolicy, http.MethodGet, env.auth.ValidToken, "", ids)
	assert.Equal(t, http.StatusNotFound, missing.Code)
}

func TestRuleSharingPolicyRefusals(t *testing.T) {
	env := newHandlerEnv(t)
	env.withChild(t)
	rule := env.addRule(t, "City", "traits.city", 1)
	ruleScope := map[string]string{"ruleId": rule.RuleId}

	tests := []struct {
		name   string
		body   string
		scope  map[string]string
		status int
	}{
		{"malformed JSON", `{"child_orgs":`, ruleScope, http.StatusBadRequest},
		{"no child_orgs", `{}`, ruleScope, http.StatusBadRequest},
		{"an unknown mode", `{"child_orgs":{"mode":"some"}}`, ruleScope, http.StatusBadRequest},
		{"an organization that is not a child",
			`{"child_orgs":{"mode":"selected","orgs":[{"org_id":"stranger","reach":"org"}]}}`,
			ruleScope, http.StatusBadRequest},
		{"a rule that does not exist", `{"child_orgs":{"mode":"all"}}`, map[string]string{"ruleId": "missing"},
			http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := env.call(t, env.h.CreateSharingPolicy, http.MethodPost, env.auth.ValidToken, tc.body, tc.scope)

			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}

	unknown := rulePolicyIDs(rule.RuleId, "missing")
	assert.Equal(t, http.StatusNotFound, env.call(t, env.h.GetSharingPolicy, http.MethodGet, env.auth.ValidToken, "",
		unknown).Code)
	assert.Equal(t, http.StatusNotFound, env.call(t, env.h.UpdateSharingPolicy, http.MethodPut, env.auth.ValidToken,
		`{"child_orgs":{"mode":"all"}}`, unknown).Code)
	assert.Equal(t, http.StatusNotFound,
		env.call(t, env.h.DeleteSharingPolicy, http.MethodDelete, env.auth.ValidToken, "", unknown).Code)
}

func TestRuleSharingHandlersRequireAuthentication(t *testing.T) {
	env := newHandlerEnv(t)
	ids := rulePolicyIDs("any", "any")
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
			assert.Equal(t, http.StatusUnauthorized, env.call(t, tc.handler, tc.method, "", tc.body, ids).Code)
		})
		t.Run(tc.name+" with the wrong scope", func(t *testing.T) {
			assert.Equal(t, http.StatusForbidden, env.call(t, tc.handler, tc.method, env.auth.OtherToken, tc.body,
				ids).Code)
		})
	}
}

func TestASharedRuleIsReadOnlyInTheChild(t *testing.T) {
	env := newHandlerEnv(t)
	env.withChild(t)
	rule := env.addRule(t, "City", "traits.city", 1)
	ids := map[string]string{"ruleId": rule.RuleId}
	created := env.call(t, env.h.CreateSharingPolicy, http.MethodPost, env.auth.ValidToken,
		`{"child_orgs":{"mode":"all"}}`, ids)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())

	// A shared rule runs in the child only when the child also sees the attribute it is on.
	env.shareAttribute(t, "attr-traits.city")

	get := env.callAsChild(t, env.h.GetUnificationRule, http.MethodGet, "", ids)
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	var shared model.UnificationRuleAPIResponse
	require.NoError(t, json.Unmarshal(get.Body.Bytes(), &shared))
	assert.Equal(t, rule.RuleId, shared.RuleId)
	assert.Equal(t, "SHARED", shared.Origin)

	list := env.callAsChild(t, env.h.GetUnificationRules, http.MethodGet, "", nil)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var rules []model.UnificationRuleAPIResponse
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &rules))
	require.Len(t, rules, 1)
	assert.Equal(t, "SHARED", rules[0].Origin)

	assert.Equal(t, http.StatusForbidden,
		env.callAsChild(t, env.h.PatchUnificationRule, http.MethodPatch, `{"priority":9}`, ids).Code)
	assert.Equal(t, http.StatusForbidden, env.callAsChild(t, env.h.DeleteUnificationRule, http.MethodDelete, "",
		ids).Code)
	assert.Equal(t, http.StatusForbidden,
		env.callAsChild(t, env.h.AddUnificationRule, http.MethodPost, ruleBody("Own", "traits.phone", 5), nil).Code)
}

func TestARuleThatIsNotSharedIsInvisibleToTheChild(t *testing.T) {
	env := newHandlerEnv(t)
	env.withChild(t)
	rule := env.addRule(t, "City", "traits.city", 1)

	get := env.callAsChild(t, env.h.GetUnificationRule, http.MethodGet, "", map[string]string{"ruleId": rule.RuleId})
	assert.Equal(t, http.StatusNotFound, get.Code)

	list := env.callAsChild(t, env.h.GetUnificationRules, http.MethodGet, "", nil)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assert.JSONEq(t, `[]`, list.Body.String())
}
