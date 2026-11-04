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
	"testing"

	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	shareModel "github.com/wso2/identity-customer-data-service/internal/sharing/model"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
)

func attribute(id, name, valueType string) schemaModel.ProfileSchemaAttribute {
	return schemaModel.ProfileSchemaAttribute{AttributeId: id, AttributeName: name, ValueType: valueType}
}

func rule(id, owner, propertyName, propertyId string) model.UnificationRule {
	return model.UnificationRule{RuleId: id, OrgHandle: owner, PropertyName: propertyName, PropertyId: propertyId}
}

func shared(id, owner string, depth int) shareModel.SharedResource {
	return shareModel.SharedResource{ResourceId: id, OwnerOrgHandle: owner, OwnerDepth: depth}
}

func statesOf(rules []sharedRule) map[string]string {
	states := map[string]string{}
	for _, r := range rules {
		states[r.Rule.RuleId] = r.State
	}
	return states
}

func TestResolveSharedRuleStates(t *testing.T) {

	properties := []schemaModel.ProfileSchemaAttribute{
		attribute("primary-email", "identity_attributes.email", "string"),
		attribute("primary-loyalty", "traits.loyalty_id", "string"),
		attribute("primary-phone", "traits.phone", "string"),
		attribute("primary-tier", "traits.tier", "string"),
	}
	rules := []model.UnificationRule{
		rule("email-rule", "r", "identity_attributes.email", "primary-email"),
		rule("loyalty-rule", "r", "traits.loyalty_id", "primary-loyalty"),
		rule("phone-rule", "r", "traits.phone", "primary-phone"),
		rule("tier-rule", "r", "traits.tier", "primary-tier"),
	}
	refs := []shareModel.SharedResource{shared("email-rule", "r", 2), shared("loyalty-rule", "r", 2),
		shared("phone-rule", "r", 2), shared("tier-rule", "r", 2)}
	owned := []model.UnificationRule{rule("local-loyalty", "a1", "traits.loyalty_id", "a1-loyalty")}
	// The organization sees an email attribute and a loyalty attribute. Its phone attribute has another
	// value type, and it does not see a tier attribute.
	visible := []schemaModel.ProfileSchemaAttribute{
		attribute("primary-email", "identity_attributes.email", "string"),
		attribute("a1-loyalty", "traits.loyalty_id", "string"),
		attribute("a1-phone", "traits.phone", "integer"),
	}

	got := statesOf(resolveSharedRuleStates(refs, rules, owned, visible, properties))
	expected := map[string]string{
		"email-rule":   shareModel.StateActive,
		"loyalty-rule": shareModel.StateConflicted,
		"phone-rule":   model.StateInactiveMissingAttribute,
		"tier-rule":    model.StateInactiveMissingAttribute,
	}
	for id, want := range expected {
		if got[id] != want {
			t.Errorf("rule %s: expected %s, got %s", id, want, got[id])
		}
	}
}

// Two shared rules on one property: the rule of the farthest owner wins, as in the run order.
func TestResolveSharedRuleStatesFarthestOwnerWins(t *testing.T) {

	properties := []schemaModel.ProfileSchemaAttribute{attribute("primary-email", "identity_attributes.email",
		"string")}
	rules := []model.UnificationRule{
		rule("primary-rule", "r", "identity_attributes.email", "primary-email"),
		rule("a-rule", "a", "identity_attributes.email", "primary-email"),
	}
	refs := []shareModel.SharedResource{shared("a-rule", "a", 1), shared("primary-rule", "r", 2)}

	got := statesOf(resolveSharedRuleStates(refs, rules, nil, properties, properties))
	if got["primary-rule"] != shareModel.StateActive || got["a-rule"] != shareModel.StateConflicted {
		t.Fatalf("expected the rule of the primary organization to win, got %v", got)
	}
}
