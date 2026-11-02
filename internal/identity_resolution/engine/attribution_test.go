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

package engine

import (
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	urModel "github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
)

func namedRule(ruleName, property, attrType, method string, priority int) urModel.UnificationRule {
	rule := testRule(property, attrType, method, priority)
	rule.RuleName = ruleName
	return rule
}

// TestMergeAttributionNamesTheDecidingRule keeps the recorded merge reason honest.
//
// The reason is reconstructed from the score breakdown by PrimaryRuleName, while the score
// itself is chosen by ScoreCandidate. The two make the same choice only if they apply the
// same order, and the rule that decided is not always the first rule that agreed: with
// deterministic matches decisive, an exact match at any priority ends the evaluation, so a
// fuzzy rule ranked above it agreed without deciding anything.
//
// Getting this wrong is quiet. The name is written onto the child profile reference and is
// the only record of why two profiles were combined, so crediting a similarity match where
// an exact identifier was the cause overstates how uncertain the merge was — and would
// mis-train anything later learned from these decisions.
func TestMergeAttributionNamesTheDecidingRule(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	sameName := map[string]interface{}{"traits.name": "Jonathan Smith"}
	typoName := map[string]interface{}{"traits.name": "Jonathon Smith"}

	tests := []struct {
		name     string
		decisive bool
		rules    []urModel.UnificationRule
		incoming map[string]interface{}
		existing map[string]interface{}
		want     string
	}{
		{
			// The case this exists for: a fuzzy rule outranks the exact match that decided.
			name:     "deterministic rule below a fuzzy one is what decided",
			decisive: true,
			rules: []urModel.UnificationRule{
				namedRule("name-rule", "traits.name", constants.AttributeTypeName, constants.UnificationMethodFuzzy, 1),
				namedRule("email-rule", "identity_attributes.email", constants.AttributeTypeEmail,
					constants.UnificationMethodDeterministic, 2),
			},
			incoming: map[string]interface{}{"traits.name": "Jonathan Smith", "identity_attributes.email": "j@acme.com"},
			existing: map[string]interface{}{"traits.name": "Jonathon Smith", "identity_attributes.email": "j@acme.com"},
			want:     "email-rule",
		},
		{
			// With the short-circuit off, the fuzzy rule really is the primary signal.
			name:     "same rules, objections allowed, the top rule speaks",
			decisive: false,
			rules: []urModel.UnificationRule{
				namedRule("name-rule", "traits.name", constants.AttributeTypeName, constants.UnificationMethodFuzzy, 1),
				namedRule("email-rule", "identity_attributes.email", constants.AttributeTypeEmail,
					constants.UnificationMethodDeterministic, 2),
			},
			incoming: map[string]interface{}{"traits.name": "Jonathan Smith", "identity_attributes.email": "j@acme.com"},
			existing: map[string]interface{}{"traits.name": "Jonathon Smith", "identity_attributes.email": "j@acme.com"},
			want:     "name-rule",
		},
		{
			name:     "a unique identifier matching exactly is attributed to itself",
			decisive: true,
			rules: []urModel.UnificationRule{
				namedRule("nic-rule", "identity_attributes.nic", constants.AttributeTypeUniqueID,
					constants.UnificationMethodDeterministic, 1),
				namedRule("name-rule", "traits.name", constants.AttributeTypeName, constants.UnificationMethodFuzzy, 2),
			},
			incoming: map[string]interface{}{"identity_attributes.nic": "199012345V", "traits.name": "Jonathan Smith"},
			existing: map[string]interface{}{"identity_attributes.nic": "199012345V", "traits.name": "Jonathon Smith"},
			want:     "nic-rule",
		},
		{
			name:     "only a fuzzy rule agrees, so it is the reason either way",
			decisive: true,
			rules: []urModel.UnificationRule{
				namedRule("name-rule", "traits.name", constants.AttributeTypeName, constants.UnificationMethodFuzzy, 1),
			},
			incoming: sameName,
			existing: typoName,
			want:     "name-rule",
		},
		{
			name:     "the higher-priority deterministic rule wins between two",
			decisive: true,
			rules: []urModel.UnificationRule{
				namedRule("email-rule", "identity_attributes.email", constants.AttributeTypeEmail,
					constants.UnificationMethodDeterministic, 1),
				namedRule("phone-rule", "traits.phone", constants.AttributeTypePhone,
					constants.UnificationMethodDeterministic, 2),
			},
			incoming: map[string]interface{}{"identity_attributes.email": "j@acme.com", "traits.phone": "0771234567"},
			existing: map[string]interface{}{"identity_attributes.email": "j@acme.com", "traits.phone": "0771234567"},
			want:     "email-rule",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			thresholds := model.Thresholds{
				AutoMergeEnabled:           true,
				AutoMerge:                  0.95,
				ManualReview:               0.75,
				DeterministicMatchDecisive: tt.decisive,
			}
			ctx := ScoringContext{OrgHandle: "acme", Thresholds: thresholds}
			candidate := &model.ProfileData{ProfileID: "candidate", Attributes: tt.existing}

			score, breakdown := ScoreCandidate(tt.incoming, candidate, tt.rules, ctx)

			got, found := urModel.PrimaryRuleName(breakdown, tt.rules,
				thresholds.ManualReview, thresholds.DeterministicMatchDecisive)
			if !found {
				t.Fatalf("no rule attributed for a pair scoring %.4f (breakdown %v)", score, breakdown)
			}
			if got != tt.want {
				t.Errorf("merge attributed to %q, want %q (score %.4f, breakdown %v)",
					got, tt.want, score, breakdown)
			}
		})
	}
}
