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

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// TestAsExactMatchingLeavesEverythingElseAlone is what makes the switch reversible: only the
// reading of a rule changes, so turning the setting back on restores what the operator
// configured instead of requiring the rules to be written again.
func TestAsExactMatchingLeavesEverythingElseAlone(t *testing.T) {
	stored := []UnificationRule{
		{
			RuleName:          "email",
			PropertyName:      "identity_attributes.email",
			Priority:          1,
			IsActive:          true,
			AttributeType:     constants.AttributeTypeEmail,
			UnificationMethod: constants.UnificationMethodFuzzy,
			MatchStrength:     constants.EvidenceStrengthHigh,
		},
		{
			RuleName:          "nic",
			PropertyName:      "identity_attributes.nic",
			Priority:          2,
			IsActive:          true,
			AttributeType:     constants.AttributeTypeUniqueID,
			UnificationMethod: constants.UnificationMethodDeterministic,
		},
	}

	coerced := AsExactMatching(stored)

	for i, rule := range coerced {
		if rule.UnificationMethod != constants.UnificationMethodDeterministic {
			t.Errorf("rule %q still reads as %q", rule.RuleName, rule.UnificationMethod)
		}
		if rule.AttributeType != stored[i].AttributeType {
			t.Errorf("rule %q lost its attribute type", rule.RuleName)
		}
		if rule.Priority != stored[i].Priority || rule.RuleName != stored[i].RuleName {
			t.Errorf("rule %q lost identity or ordering", rule.RuleName)
		}
		if rule.MatchStrength != stored[i].MatchStrength {
			t.Errorf("rule %q lost its evidence strength", rule.RuleName)
		}
	}

	// The caller's own slice must be untouched, or a later read would see the coerced value
	// and the setting would stop being reversible within a single process.
	if stored[0].UnificationMethod != constants.UnificationMethodFuzzy {
		t.Error("coercion mutated the stored rules in place")
	}
}
