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

// scoreSoleRule scores one pair of values against a single configured rule, which is the
// configuration the lone-agreement gate governs.
func scoreSoleRule(t *testing.T, attrType, method, a, b string) (float64, string) {
	t.Helper()

	thresholds := model.Thresholds{AutoMergeEnabled: true, AutoMerge: 0.95, ManualReview: 0.75}
	ctx := ScoringContext{OrgHandle: "acme", Thresholds: thresholds}
	rules := []urModel.UnificationRule{testRule("identity_attributes.v", attrType, method, 1)}

	score, _ := ScoreCandidate(
		map[string]interface{}{"identity_attributes.v": a},
		&model.ProfileData{ProfileID: "candidate",
			Attributes: map[string]interface{}{"identity_attributes.v": b}},
		rules, ctx)

	return score, model.Decide(score, thresholds)
}

// TestLoneLowStrengthRuleCannotAutoMerge is the property that stops an organisation whose only
// rule is a tolerant name match from merging two different people unattended.
//
// Being the sole rule makes an attribute top-priority by default rather than by choice, so
// rank must not promote evidence the type marks LOW. Without this, "Ivan Petrov" scored 0.9833
// against "Ivana Petrov" and merged with nobody ever seeing the pair.
func TestLoneLowStrengthRuleCannotAutoMerge(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	lowStrengthPairs := []struct {
		name, attrType, a, b string
	}{
		{"different people, similar names", constants.AttributeTypeName, "Ivan Petrov", "Ivana Petrov"},
		{"gendered pair of one root", constants.AttributeTypeName, "Michael Brown", "Michelle Brown"},
		{"identical names, two people", constants.AttributeTypeName, "Katherine Jones", "Katherine Jones"},
		{"a shared address", constants.AttributeTypeLocation, "12 Galle Road", "12 Galle Road"},
	}

	for _, tt := range lowStrengthPairs {
		t.Run(tt.name, func(t *testing.T) {
			score, decision := scoreSoleRule(t, tt.attrType, constants.UnificationMethodFuzzy, tt.a, tt.b)
			if decision == constants.DecisionAutoMerge {
				t.Errorf("a sole LOW-strength rule merged %q with %q unattended (score %.4f)",
					tt.a, tt.b, score)
			}
		})
	}
}

// TestLoneIdentifyingRuleStillAutoMerges guards the other side of the same condition. Tightening
// the gate must not stop an attribute that does identify a person from carrying a merge alone —
// in particular PRIMITIVE_EXACT, which is what every rule written before typed matching becomes,
// and which merged on its own before this feature existed.
func TestLoneIdentifyingRuleStillAutoMerges(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	identifying := []struct {
		name, attrType, method, a, b string
	}{
		{"legacy untyped rule", constants.AttributeTypePrimitiveExact,
			constants.UnificationMethodDeterministic, "abc-123", "abc-123"},
		{"unique identifier", constants.AttributeTypeUniqueID,
			constants.UnificationMethodDeterministic, "199012345V", "199012345V"},
		{"email address", constants.AttributeTypeEmail,
			constants.UnificationMethodDeterministic, "k.jones@acme.com", "k.jones@acme.com"},
		{"phone written two ways", constants.AttributeTypePhone,
			constants.UnificationMethodFuzzy, "+94771234567", "0771234567"},
		{"medium evidence as the top rule", constants.AttributeTypeFuzzyString,
			constants.UnificationMethodFuzzy, "widget-alpha", "widget-alpha"},
	}

	for _, tt := range identifying {
		t.Run(tt.name, func(t *testing.T) {
			score, decision := scoreSoleRule(t, tt.attrType, tt.method, tt.a, tt.b)
			if decision != constants.DecisionAutoMerge {
				t.Errorf("expected %q and %q to merge on a sole identifying rule, got %s (score %.4f)",
					tt.a, tt.b, decision, score)
			}
		})
	}
}
