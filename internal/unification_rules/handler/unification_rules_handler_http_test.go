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

	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaStore "github.com/wso2/identity-customer-data-service/internal/profile_schema/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

var handlerOrgCounter atomic.Int64

// handlerEnv is an organization with CDS enabled, a small profile schema and a stub identity
// server, so that a handler runs with its authentication and authorization unmodified.
type handlerEnv struct {
	db     *sql.DB
	handle string
	orgID  string
	auth   setup.AuthStub
	h      *UnificationRulesHandler
}

func newHandlerEnv(t *testing.T) *handlerEnv {
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
	auth := setup.InstallAuthStub(t, "unification_rules:create", "unification_rules:view",
		"unification_rules:update", "unification_rules:delete", "unification_rules:share_view",
		"unification_rules:share_update")

	handle := fmt.Sprintf("acme-%d", handlerOrgCounter.Add(1))
	env := &handlerEnv{db: testDB.DB, handle: handle, orgID: setup.TestOrgId(handle), auth: auth,
		h: NewUnificationRulesHandler()}
	setup.TestOrg(t, handle)
	env.enableCDS(t)
	for _, name := range []string{"traits.city", "traits.phone", "traits.tier", "traits.nick"} {
		env.addAttribute(t, name)
	}
	return env
}

func (e *handlerEnv) enableCDS(t *testing.T) {
	t.Helper()
	_, err := e.db.Exec(`INSERT INTO cds_config (org_id, config, value) VALUES (?, 'cds_enabled', 'true')`, e.orgID)
	require.NoError(t, err)
}

func (e *handlerEnv) addAttribute(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, schemaStore.AddProfileSchemaAttributesForScope(context.Background(),
		[]schemaModel.ProfileSchemaAttribute{{
			AttributeId:   "attr-" + name,
			AttributeName: name,
			Scope:         constants.Traits,
			ValueType:     constants.StringDataType,
			MergeStrategy: "overwrite",
			Mutability:    constants.MutabilityReadWrite,
		}}, constants.Traits, e.orgID))
}

// call runs a handler on a request for the organization, with a bearer token.
func (e *handlerEnv) call(t *testing.T, handler http.HandlerFunc, method, token, body string,
	pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/t/"+e.handle+"/cds/api/v1/unification-rules", strings.NewReader(body))
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

func ruleBody(name, property string, priority int) string {
	return fmt.Sprintf(`{"rule_name":%q,"property_name":%q,"priority":%d,"is_active":false,`+
		`"attribute_type":"PRIMITIVE_EXACT","unification_method":"deterministic"}`, name, property, priority)
}

func decodeRule(t *testing.T, recorder *httptest.ResponseRecorder) model.UnificationRuleAPIResponse {
	t.Helper()
	var rule model.UnificationRuleAPIResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &rule), recorder.Body.String())
	return rule
}

func (e *handlerEnv) addRule(t *testing.T, name, property string, priority int) model.UnificationRuleAPIResponse {
	t.Helper()
	recorder := e.call(t, e.h.AddUnificationRule, http.MethodPost, e.auth.ValidToken,
		ruleBody(name, property, priority), nil)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	return decodeRule(t, recorder)
}

func errorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body), recorder.Body.String())
	return body.Code
}

func TestAddUnificationRuleCreatesTheRule(t *testing.T) {
	env := newHandlerEnv(t)

	recorder := env.call(t, env.h.AddUnificationRule, http.MethodPost, env.auth.ValidToken,
		ruleBody("City", "traits.city", 1), nil)

	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	rule := decodeRule(t, recorder)
	assert.NotEmpty(t, rule.RuleId)
	assert.Equal(t, "City", rule.RuleName)
	assert.Equal(t, "traits.city", rule.PropertyName)
	assert.Equal(t, constants.UnificationMethodDeterministic, rule.UnificationMethod)
	// The override is off, so the evidence strengths are not part of the response.
	assert.Empty(t, rule.MatchStrength)
	assert.Empty(t, rule.MismatchStrength)
}

func TestAddUnificationRuleDefaultsTheMethodAndTheAttributeType(t *testing.T) {
	env := newHandlerEnv(t)
	body := `{"rule_name":"City","property_name":"traits.city","priority":1,"is_active":false}`

	recorder := env.call(t, env.h.AddUnificationRule, http.MethodPost, env.auth.ValidToken, body, nil)

	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	rule := decodeRule(t, recorder)
	assert.Equal(t, constants.UnificationMethodDeterministic, rule.UnificationMethod)
	assert.Equal(t, constants.AttributeTypePrimitiveExact, rule.AttributeType)
}

func TestAddUnificationRuleRefusals(t *testing.T) {
	env := newHandlerEnv(t)
	env.addRule(t, "City", "traits.city", 1)

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"malformed JSON", `{"rule_name":`, http.StatusBadRequest},
		{"an unknown field",
			`{"rule_name":"x","property_name":"traits.phone","priority":2,"is_active":false,"color":"red"}`,
			http.StatusBadRequest},
		{"an invalid attribute type",
			`{"rule_name":"x","property_name":"traits.phone","priority":2,"is_active":false,` +
				`"attribute_type":"NOPE"}`, http.StatusBadRequest},
		{"an invalid method", `{"rule_name":"x","property_name":"traits.phone","priority":2,"is_active":false,` +
			`"unification_method":"magic"}`, http.StatusBadRequest},
		{"fuzzy matching while it is switched off", `{"rule_name":"x","property_name":"traits.phone","priority":2,` +
			`"is_active":false,"attribute_type":"NAME","unification_method":"fuzzy"}`, http.StatusBadRequest},
		{"fuzzy without an attribute type", `{"rule_name":"x","property_name":"traits.phone","priority":2,` +
			`"is_active":false,"unification_method":"fuzzy"}`, http.StatusBadRequest},
		{"an evidence strength while the override is off", `{"rule_name":"x","property_name":"traits.phone",` +
			`"priority":2,"is_active":false,"match_strength":"HIGH"}`, http.StatusBadRequest},
		{"a property that is not in the schema", ruleBody("x", "traits.nothing", 2), http.StatusBadRequest},
		{"a priority that is taken", ruleBody("x", "traits.phone", 1), http.StatusBadRequest},
		{"a property that has a rule", ruleBody("x", "traits.city", 2), http.StatusConflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := env.call(t, env.h.AddUnificationRule, http.MethodPost, env.auth.ValidToken, tc.body, nil)

			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}
}

func TestUnificationRuleHandlersRequireAuthentication(t *testing.T) {
	env := newHandlerEnv(t)
	rule := env.addRule(t, "City", "traits.city", 1)
	ids := map[string]string{"ruleId": rule.RuleId}

	calls := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		body    string
	}{
		{"add", env.h.AddUnificationRule, http.MethodPost, ruleBody("x", "traits.phone", 2)},
		{"list", env.h.GetUnificationRules, http.MethodGet, ""},
		{"get", env.h.GetUnificationRule, http.MethodGet, ""},
		{"patch", env.h.PatchUnificationRule, http.MethodPatch, `{"priority":3}`},
		{"delete", env.h.DeleteUnificationRule, http.MethodDelete, ""},
		{"options", env.h.GetUnificationOptions, http.MethodGet, ""},
	}
	for _, tc := range calls {
		t.Run(tc.name+" without a token", func(t *testing.T) {
			recorder := env.call(t, tc.handler, tc.method, "", tc.body, ids)

			assert.Equal(t, http.StatusUnauthorized, recorder.Code)
		})
		t.Run(tc.name+" with an unknown token", func(t *testing.T) {
			recorder := env.call(t, tc.handler, tc.method, "not-a-real-token", tc.body, ids)

			assert.Equal(t, http.StatusUnauthorized, recorder.Code)
		})
		t.Run(tc.name+" with the wrong scope", func(t *testing.T) {
			recorder := env.call(t, tc.handler, tc.method, env.auth.OtherToken, tc.body, ids)

			assert.Equal(t, http.StatusForbidden, recorder.Code)
		})
	}
}

func TestUnificationRuleHandlersRefuseAnOrganizationWithoutCDS(t *testing.T) {
	env := newHandlerEnv(t)
	_, err := env.db.Exec(`DELETE FROM cds_config WHERE org_id = ?`, env.orgID)
	require.NoError(t, err)
	ids := map[string]string{"ruleId": "any"}

	for name, handler := range map[string]http.HandlerFunc{
		"add":    env.h.AddUnificationRule,
		"list":   env.h.GetUnificationRules,
		"get":    env.h.GetUnificationRule,
		"patch":  env.h.PatchUnificationRule,
		"delete": env.h.DeleteUnificationRule,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := env.call(t, handler, http.MethodPost, env.auth.ValidToken, ruleBody("x", "traits.city", 1), ids)

			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			assert.Equal(t, "CDS-16001", errorCode(t, recorder))
		})
	}
}

func TestGetUnificationRulesListsTheRulesByPriority(t *testing.T) {
	env := newHandlerEnv(t)
	env.addRule(t, "Phone", "traits.phone", 2)
	env.addRule(t, "City", "traits.city", 1)

	recorder := env.call(t, env.h.GetUnificationRules, http.MethodGet, env.auth.ValidToken, "", nil)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var rules []model.UnificationRuleAPIResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &rules))
	require.Len(t, rules, 2)
	assert.Equal(t, "City", rules[0].RuleName)
	assert.Equal(t, "Phone", rules[1].RuleName)
}

func TestGetUnificationRulesOfAnOrganizationWithNoneIsAnEmptyList(t *testing.T) {
	env := newHandlerEnv(t)

	recorder := env.call(t, env.h.GetUnificationRules, http.MethodGet, env.auth.ValidToken, "", nil)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `[]`, recorder.Body.String())
}

func TestGetUnificationRule(t *testing.T) {
	env := newHandlerEnv(t)
	rule := env.addRule(t, "City", "traits.city", 1)

	found := env.call(t, env.h.GetUnificationRule, http.MethodGet, env.auth.ValidToken, "",
		map[string]string{"ruleId": rule.RuleId})
	require.Equal(t, http.StatusOK, found.Code, found.Body.String())
	assert.Equal(t, rule.RuleId, decodeRule(t, found).RuleId)

	missing := env.call(t, env.h.GetUnificationRule, http.MethodGet, env.auth.ValidToken, "",
		map[string]string{"ruleId": "missing"})
	assert.Equal(t, http.StatusNotFound, missing.Code)

	noID := env.call(t, env.h.GetUnificationRule, http.MethodGet, env.auth.ValidToken, "", nil)
	assert.Equal(t, http.StatusNotFound, noID.Code)
}

func TestPatchUnificationRule(t *testing.T) {
	env := newHandlerEnv(t)
	rule := env.addRule(t, "City", "traits.city", 1)
	env.addRule(t, "Phone", "traits.phone", 2)
	ids := map[string]string{"ruleId": rule.RuleId}

	t.Run("changes only the fields that are sent", func(t *testing.T) {
		recorder := env.call(t, env.h.PatchUnificationRule, http.MethodPatch, env.auth.ValidToken,
			`{"rule_name":"Town","priority":5}`, ids)

		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		patched := decodeRule(t, recorder)
		assert.Equal(t, "Town", patched.RuleName)
		assert.Equal(t, 5, patched.Priority)
		assert.Equal(t, "traits.city", patched.PropertyName)
	})
	refusals := map[string]string{
		"an unknown field":          `{"color":"red"}`,
		"malformed JSON":            `{"priority":`,
		"an invalid attribute type": `{"attribute_type":"NOPE"}`,
		"an invalid method":         `{"unification_method":"magic"}`,
		"fuzzy while it is off":     `{"unification_method":"fuzzy","attribute_type":"NAME"}`,
		"a priority that is taken":  `{"priority":2}`,
		"an evidence strength":      `{"match_strength":"HIGH"}`,
	}
	for name, body := range refusals {
		t.Run(name, func(t *testing.T) {
			recorder := env.call(t, env.h.PatchUnificationRule, http.MethodPatch, env.auth.ValidToken, body, ids)

			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}
	t.Run("an unknown rule", func(t *testing.T) {
		recorder := env.call(t, env.h.PatchUnificationRule, http.MethodPatch, env.auth.ValidToken, `{"priority":9}`,
			map[string]string{"ruleId": "missing"})

		assert.Equal(t, http.StatusNotFound, recorder.Code)
	})
	t.Run("no rule in the path", func(t *testing.T) {
		recorder := env.call(t, env.h.PatchUnificationRule, http.MethodPatch, env.auth.ValidToken, `{"priority":9}`,
			nil)

		assert.Equal(t, http.StatusBadRequest, recorder.Code)
	})
}

func TestDeleteUnificationRule(t *testing.T) {
	env := newHandlerEnv(t)
	rule := env.addRule(t, "City", "traits.city", 1)
	ids := map[string]string{"ruleId": rule.RuleId}

	first := env.call(t, env.h.DeleteUnificationRule, http.MethodDelete, env.auth.ValidToken, "", ids)
	assert.Equal(t, http.StatusNoContent, first.Code)

	// A rule that is gone is not an error.
	again := env.call(t, env.h.DeleteUnificationRule, http.MethodDelete, env.auth.ValidToken, "", ids)
	assert.Equal(t, http.StatusNoContent, again.Code)

	get := env.call(t, env.h.GetUnificationRule, http.MethodGet, env.auth.ValidToken, "", ids)
	assert.Equal(t, http.StatusNotFound, get.Code)

	noID := env.call(t, env.h.DeleteUnificationRule, http.MethodDelete, env.auth.ValidToken, "", nil)
	assert.Equal(t, http.StatusBadRequest, noID.Code)
}

func TestGetUnificationOptionsListsTheAttributeTypes(t *testing.T) {
	env := newHandlerEnv(t)

	recorder := env.call(t, env.h.GetUnificationOptions, http.MethodGet, env.auth.ValidToken, "", nil)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var options model.UnificationOptionsResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &options))
	assert.NotEmpty(t, options.AttributeTypes)
}
