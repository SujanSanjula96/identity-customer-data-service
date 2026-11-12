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
package model

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

func scoredRule(name, attributeType, method string) UnificationRule {
	return UnificationRule{
		RuleName:          name,
		PropertyName:      "traits." + name,
		AttributeType:     attributeType,
		UnificationMethod: method,
	}
}

func breakdownOf(scores map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for name, score := range scores {
		out["traits."+name] = score
	}
	return out
}

func TestPrimaryRuleNameIsEmptyWhenNothingAgreed(t *testing.T) {
	rules := []UnificationRule{scoredRule("email", constants.AttributeTypeUniqueID,
		constants.UnificationMethodDeterministic)}

	name, ok := PrimaryRuleName(breakdownOf(map[string]float64{"email": 0.4}), rules, 0.8, true)

	assert.False(t, ok)
	assert.Empty(t, name)
}

func TestPrimaryRuleNameIgnoresRulesThatWereNotScored(t *testing.T) {
	rules := []UnificationRule{scoredRule("email", constants.AttributeTypeUniqueID,
		constants.UnificationMethodDeterministic)}

	_, ok := PrimaryRuleName(map[string]float64{}, rules, 0.8, true)

	assert.False(t, ok)
}

func TestPrimaryRuleNameUniqueIdentifierMatchingExactlyWins(t *testing.T) {
	rules := []UnificationRule{
		scoredRule("email", constants.AttributeTypeUniqueID, constants.UnificationMethodDeterministic),
		scoredRule("name", "", constants.UnificationMethodFuzzy),
	}

	name, ok := PrimaryRuleName(breakdownOf(map[string]float64{"email": 1.0, "name": 0.95}), rules, 0.8, false)

	assert.True(t, ok)
	assert.Equal(t, "email", name)
}

func TestPrimaryRuleNameUniqueIdentifierBelowOneIsNotConclusive(t *testing.T) {
	rules := []UnificationRule{scoredRule("email", constants.AttributeTypeUniqueID, constants.UnificationMethodFuzzy)}

	name, ok := PrimaryRuleName(breakdownOf(map[string]float64{"email": 0.9}), rules, 0.8, false)

	assert.True(t, ok)
	assert.Equal(t, "email", name)
}

func TestPrimaryRuleNameDeterministicBeatsAnEarlierFuzzyRuleWhenDecisive(t *testing.T) {
	// The fuzzy rule is ranked first and agreed, but an exact match elsewhere is decisive,
	// so the merge is attributed to the exact rule.
	rules := []UnificationRule{
		scoredRule("name", "", constants.UnificationMethodFuzzy),
		scoredRule("phone", "", constants.UnificationMethodDeterministic),
	}
	breakdown := breakdownOf(map[string]float64{"name": 0.9, "phone": 1.0})

	decisive, ok := PrimaryRuleName(breakdown, rules, 0.8, true)
	assert.True(t, ok)
	assert.Equal(t, "phone", decisive)

	notDecisive, ok := PrimaryRuleName(breakdown, rules, 0.8, false)
	assert.True(t, ok)
	assert.Equal(t, "name", notDecisive)
}

func TestPrimaryRuleNameFallsBackToTheFirstAgreement(t *testing.T) {
	rules := []UnificationRule{
		scoredRule("name", "", constants.UnificationMethodFuzzy),
		scoredRule("city", "", constants.UnificationMethodFuzzy),
	}

	name, ok := PrimaryRuleName(breakdownOf(map[string]float64{"name": 0.7, "city": 0.9}), rules, 0.8, true)

	assert.True(t, ok)
	assert.Equal(t, "city", name)
}

func TestToAPIResponseLeavesOutEvidenceStrengths(t *testing.T) {
	rule := UnificationRule{
		RuleId:            "r-1",
		RuleName:          "Email",
		PropertyName:      "identity_attributes.email",
		Priority:          2,
		IsActive:          true,
		AttributeType:     constants.AttributeTypeUniqueID,
		UnificationMethod: constants.UnificationMethodDeterministic,
		MatchStrength:     "HIGH",
		MismatchStrength:  "LOW",
		Origin:            "SHARED",
	}

	got := ToAPIResponse(rule)

	assert.Equal(t, UnificationRuleAPIResponse{
		RuleId:            "r-1",
		RuleName:          "Email",
		PropertyName:      "identity_attributes.email",
		Priority:          2,
		IsActive:          true,
		AttributeType:     constants.AttributeTypeUniqueID,
		UnificationMethod: constants.UnificationMethodDeterministic,
		Origin:            "SHARED",
	}, got)
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	assert.Equal(t, constants.SyncProfileOnUpdate, config.ProfileUnificationTrigger.TriggerType)
	modes := map[string]string{}
	for _, mode := range config.ProfileUnificationMode {
		modes[mode.MergeType] = mode.Rule
	}
	assert.Equal(t, map[string]string{
		constants.TempProfile_TempProfile_Merge: constants.MergeOnTrigger,
		constants.PermProfile_PermProfile_Merge: constants.MergeOnTrigger,
		constants.TempProfile_PermProfile_Merge: constants.MergeOnTrigger,
	}, modes)
}
