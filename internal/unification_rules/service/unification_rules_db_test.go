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

	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaStore "github.com/wso2/identity-customer-data-service/internal/profile_schema/store"
	shareModel "github.com/wso2/identity-customer-data-service/internal/sharing/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

// orgCounter gives every test its own organization. The rule store caches the rules of an
// organization by its ID, so tests that share an ID would see each other's rules.
var orgCounter atomic.Int64

// rulesEnv is an inbuilt database with one primary organization that has a small profile schema.
type rulesEnv struct {
	ctx   context.Context
	db    *sql.DB
	orgID string
}

func newRulesEnv(t *testing.T) *rulesEnv {
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
	env := &rulesEnv{ctx: context.Background(), db: testDB.DB, orgID: setup.TestOrgId(handle)}
	require.NoError(t, setup.InsertTestOrganization(testDB.DB, env.orgID, handle))

	env.addAttribute(t, "traits.city", constants.StringDataType)
	env.addAttribute(t, "traits.phone", constants.StringDataType)
	env.addAttribute(t, "traits.tier", constants.StringDataType)
	env.addAttribute(t, "traits.address", constants.ComplexDataType)
	return env
}

func (e *rulesEnv) addAttribute(t *testing.T, name, valueType string) {
	t.Helper()
	id := "attr-" + name
	err := schemaStore.AddProfileSchemaAttributesForScope(e.ctx, []schemaModel.ProfileSchemaAttribute{{
		AttributeId:   id,
		AttributeName: name,
		Scope:         constants.Traits,
		ValueType:     valueType,
		MergeStrategy: "overwrite",
		Mutability:    constants.MutabilityReadWrite,
	}}, constants.Traits, e.orgID)
	require.NoError(t, err)
}

func (e *rulesEnv) newRule(name, property string, priority int) model.UnificationRule {
	return model.UnificationRule{
		RuleId:            "rule-" + name,
		RuleName:          name,
		PropertyName:      property,
		Priority:          priority,
		IsActive:          true,
		AttributeType:     constants.AttributeTypeUniqueID,
		UnificationMethod: constants.UnificationMethodDeterministic,
	}
}

func (e *rulesEnv) mustAdd(t *testing.T, rule model.UnificationRule) {
	t.Helper()
	require.NoError(t, (&UnificationRuleService{}).AddUnificationRule(e.ctx, rule, e.orgID))
}

func requireClientError(t *testing.T, err error, status int) *errors2.ClientError {
	t.Helper()
	var clientError *errors2.ClientError
	require.ErrorAs(t, err, &clientError)
	require.Equal(t, status, clientError.StatusCode)
	return clientError
}

func TestAddUnificationRuleStoresTheRuleAgainstItsSchemaAttribute(t *testing.T) {
	env := newRulesEnv(t)
	svc := &UnificationRuleService{}

	require.NoError(t, svc.AddUnificationRule(env.ctx, env.newRule("city", "traits.city", 1), env.orgID))

	got, err := svc.GetUnificationRule(env.ctx, "rule-city", env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "traits.city", got.PropertyName)
	assert.Equal(t, "attr-traits.city", got.PropertyId)
	assert.Equal(t, 1, got.Priority)
}

func TestAddUnificationRuleRefusals(t *testing.T) {
	env := newRulesEnv(t)
	svc := &UnificationRuleService{}
	env.mustAdd(t, env.newRule("city", "traits.city", 1))

	tests := []struct {
		name   string
		rule   model.UnificationRule
		status int
	}{
		{"user_id", env.newRule("u", "user_id", 5), http.StatusBadRequest},
		{"identity user_id", env.newRule("u2", "identity_attributes.user_id", 5), http.StatusBadRequest},
		{"application data", env.newRule("a", "application_data.app-1.theme", 5), http.StatusBadRequest},
		{"a property that is not in the schema", env.newRule("x", "traits.nothing", 5), http.StatusBadRequest},
		{"a complex attribute", env.newRule("c", "traits.address", 5), http.StatusBadRequest},
		{"a property that already has a rule", env.newRule("city2", "traits.city", 5), http.StatusConflict},
		{"a priority that is taken", env.newRule("phone", "traits.phone", 1), http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.AddUnificationRule(env.ctx, tc.rule, env.orgID)

			_ = requireClientError(t, err, tc.status)
		})
	}
}

func TestGetUnificationRuleReturnsNotFoundForAnUnknownRule(t *testing.T) {
	env := newRulesEnv(t)

	_, err := (&UnificationRuleService{}).GetUnificationRule(env.ctx, "missing", env.orgID)

	clientError := requireClientError(t, err, http.StatusNotFound)
	assert.Equal(t, errors2.UNIFICATION_RULE_NOT_FOUND.Code, clientError.Code)
}

func TestGetUnificationRulesMarksOwnedRulesAndOrdersThemByPriority(t *testing.T) {
	env := newRulesEnv(t)
	env.mustAdd(t, env.newRule("phone", "traits.phone", 2))
	env.mustAdd(t, env.newRule("city", "traits.city", 1))

	rules, err := (&UnificationRuleService{}).GetUnificationRules(env.ctx, env.orgID)

	require.NoError(t, err)
	require.Len(t, rules, 2)
	assert.Equal(t, "rule-city", rules[0].RuleId)
	assert.Equal(t, "rule-phone", rules[1].RuleId)
	for i, rule := range rules {
		assert.Equal(t, shareModel.OriginOwned, rule.Origin)
		assert.Equal(t, shareModel.StateActive, rule.State)
		assert.Equal(t, i+1, rule.Rank)
	}
}

func TestDeletingTheSchemaAttributeDeletesItsRule(t *testing.T) {
	env := newRulesEnv(t)
	env.mustAdd(t, env.newRule("city", "traits.city", 1))
	require.NoError(t, schemaStore.DeleteProfileSchemaAttributeById(env.ctx, env.orgID, "attr-traits.city"))

	rules, err := (&UnificationRuleService{}).GetUnificationRules(env.ctx, env.orgID)

	require.NoError(t, err)
	assert.Empty(t, rules)
}

func TestGetUnificationRuleForOrgAndOwned(t *testing.T) {
	env := newRulesEnv(t)
	svc := &UnificationRuleService{}
	env.mustAdd(t, env.newRule("city", "traits.city", 1))

	forOrg, err := svc.GetUnificationRuleForOrg(env.ctx, "rule-city", env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "rule-city", forOrg.RuleId)

	owned, err := svc.GetOwnedUnificationRule(env.ctx, "rule-city", env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "rule-city", owned.RuleId)

	_, err = svc.GetUnificationRuleForOrg(env.ctx, "missing", env.orgID)
	_ = requireClientError(t, err, http.StatusNotFound)

	_, err = svc.GetOwnedUnificationRule(env.ctx, "missing", env.orgID)
	_ = requireClientError(t, err, http.StatusNotFound)
}

func TestPatchUnificationRule(t *testing.T) {
	env := newRulesEnv(t)
	svc := &UnificationRuleService{}
	env.mustAdd(t, env.newRule("city", "traits.city", 1))
	env.mustAdd(t, env.newRule("phone", "traits.phone", 2))

	t.Run("changes the stored rule", func(t *testing.T) {
		patch := env.newRule("city-renamed", "", 7)
		patch.IsActive = false

		require.NoError(t, svc.PatchUnificationRule(env.ctx, "rule-city", env.orgID, patch))

		got, err := svc.GetUnificationRule(env.ctx, "rule-city", env.orgID)
		require.NoError(t, err)
		assert.Equal(t, 7, got.Priority)
		assert.False(t, got.IsActive)
	})
	t.Run("refuses a priority another rule has", func(t *testing.T) {
		patch := env.newRule("x", "", 2)

		_ = requireClientError(t, svc.PatchUnificationRule(env.ctx, "rule-city", env.orgID, patch),
			http.StatusBadRequest)
	})
	t.Run("allows a rule to keep its own priority", func(t *testing.T) {
		patch := env.newRule("phone", "", 2)

		assert.NoError(t, svc.PatchUnificationRule(env.ctx, "rule-phone", env.orgID, patch))
	})
	t.Run("refuses user_id", func(t *testing.T) {
		patch := env.newRule("x", "user_id", 9)

		_ = requireClientError(t, svc.PatchUnificationRule(env.ctx, "rule-city", env.orgID, patch),
			http.StatusBadRequest)
	})
	t.Run("reports an unknown rule", func(t *testing.T) {
		patch := env.newRule("x", "", 9)

		_ = requireClientError(t, svc.PatchUnificationRule(env.ctx, "missing", env.orgID, patch), http.StatusNotFound)
	})
}

func TestDeleteUnificationRule(t *testing.T) {
	env := newRulesEnv(t)
	svc := &UnificationRuleService{}
	env.mustAdd(t, env.newRule("city", "traits.city", 1))

	require.NoError(t, svc.DeleteUnificationRule(env.ctx, "rule-city", env.orgID))

	_, err := svc.GetUnificationRule(env.ctx, "rule-city", env.orgID)
	_ = requireClientError(t, err, http.StatusNotFound)
	// Deleting again is not an error: the delete is idempotent.
	assert.NoError(t, svc.DeleteUnificationRule(env.ctx, "rule-city", env.orgID))
}

func TestWritesAreRefusedInASubOrganization(t *testing.T) {
	env := newRulesEnv(t)
	svc := &UnificationRuleService{}
	_, err := env.db.Exec(`INSERT INTO organizations (org_id, org_handle, primary_org_id, status)
		VALUES ('sub-org', 'sub', $1, 'ACTIVE')`, env.orgID)
	require.NoError(t, err)

	err = svc.AddUnificationRule(env.ctx, env.newRule("city", "traits.city", 1), "sub-org")
	_ = requireClientError(t, err, http.StatusForbidden)

	err = svc.PatchUnificationRule(env.ctx, "rule-city", "sub-org", env.newRule("city", "", 1))
	_ = requireClientError(t, err, http.StatusForbidden)

	err = svc.DeleteUnificationRule(env.ctx, "rule-city", "sub-org")
	_ = requireClientError(t, err, http.StatusForbidden)
}

func TestGetUnificationOptionsOffersNoFuzzyMethodWhileFuzzyIsOff(t *testing.T) {
	options := (&UnificationRuleService{}).GetUnificationOptions()

	require.NotEmpty(t, options.AttributeTypes)
	for _, attributeType := range options.AttributeTypes {
		for _, method := range attributeType.AllowedMethods {
			assert.NotEqual(t, constants.UnificationMethodFuzzy, method.Value, attributeType.Value)
		}
	}
}
