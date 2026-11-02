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

func testRule(property, attrType, method string, priority int) urModel.UnificationRule {
	return urModel.ApplyDefaults(urModel.UnificationRule{
		PropertyName:      property,
		AttributeType:     attrType,
		UnificationMethod: method,
		Priority:          priority,
		IsActive:          true,
	}, constants.AttributeTypePrimitiveExact, constants.UnificationMethodDeterministic,
		constants.DefaultMatchStrength, constants.DefaultMismatchStrength)
}

func testDecision(score float64, thresholds model.Thresholds) string {
	return model.Decide(score, thresholds)
}

// TestScoreCandidateDecisions pins the decision each documented scenario must reach.
// Scores are deliberately not asserted exactly — the similarity algorithms may be retuned,
// but these verdicts are the contract.
func TestScoreCandidateDecisions(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	thresholds := model.Thresholds{AutoMergeEnabled: true, AutoMerge: 0.95, ManualReview: 0.75}
	plain := ScoringContext{OrgHandle: "acme", Thresholds: thresholds}
	valueSharedWidely := ScoringContext{OrgHandle: "acme", Thresholds: thresholds,
		ValueFrequency: func(_, _, _ string) (int, error) { return constants.RarityCommonMinProfiles * 6, nil }}

	nic := testRule("identity_attributes.nic", constants.AttributeTypeUniqueID, constants.UnificationMethodDeterministic, 1)
	email := testRule("identity_attributes.email", constants.AttributeTypeEmail, constants.UnificationMethodDeterministic, 1)
	name := testRule("traits.name", constants.AttributeTypeName, constants.UnificationMethodFuzzy, 2)
	city := testRule("traits.city", constants.AttributeTypeLocation, constants.UnificationMethodFuzzy, 3)
	dob := testRule("traits.dob", constants.AttributeTypeDate, constants.UnificationMethodDeterministic, 2)

	tests := []struct {
		name     string
		rules    []urModel.UnificationRule
		ctx      ScoringContext
		incoming map[string]interface{}
		existing map[string]interface{}
		want     string
	}{
		{
			// A contradicting rule must not bury an otherwise strong signal, but must stop
			// it merging unattended.
			name:  "high-strength mismatch downgrades rather than dilutes",
			rules: []urModel.UnificationRule{nic, name}, ctx: plain,
			incoming: map[string]interface{}{"identity_attributes.nic": "199012345V", "traits.name": "Jonathan Smith"},
			existing: map[string]interface{}{"identity_attributes.nic": "200198765V", "traits.name": "Jonathon Smith"},
			want:     constants.DecisionManualReview,
		},
		{
			// The same name evidence must not merge harder just because the NIC is absent.
			name:  "missing attribute does not strengthen a match",
			rules: []urModel.UnificationRule{nic, name}, ctx: plain,
			incoming: map[string]interface{}{"identity_attributes.nic": "199012345V", "traits.name": "Jonathan Smith"},
			existing: map[string]interface{}{"traits.name": "Jonathon Smith"},
			want:     constants.DecisionManualReview,
		},
		{
			// Case difference must not hide a unique identifier from either blocking or matching.
			name:  "unique identifier short-circuits regardless of case",
			rules: []urModel.UnificationRule{nic, name}, ctx: plain,
			incoming: map[string]interface{}{"identity_attributes.nic": "199012345V", "traits.name": "Jonathan Smith"},
			existing: map[string]interface{}{"identity_attributes.nic": "199012345v", "traits.name": "Wei Zhang"},
			want:     constants.DecisionAutoMerge,
		},
		{
			name:  "lone weak attribute cannot auto-merge",
			rules: []urModel.UnificationRule{email, name, city}, ctx: plain,
			incoming: map[string]interface{}{"traits.city": "Colombo"},
			existing: map[string]interface{}{"traits.city": "colombo"},
			want:     constants.DecisionManualReview,
		},
		{
			name:  "lone strong attribute may auto-merge",
			rules: []urModel.UnificationRule{email, name, city}, ctx: plain,
			incoming: map[string]interface{}{"identity_attributes.email": "j.smith@acme.com"},
			existing: map[string]interface{}{"identity_attributes.email": "j.smith@acme.com"},
			want:     constants.DecisionAutoMerge,
		},
		{
			// Agreement on a value hundreds of profiles share is coincidence, not identity.
			name:  "widely shared value cannot carry a lone auto-merge",
			rules: []urModel.UnificationRule{email, name, city}, ctx: valueSharedWidely,
			incoming: map[string]interface{}{"identity_attributes.email": "household@acme.com"},
			existing: map[string]interface{}{"identity_attributes.email": "household@acme.com"},
			want:     constants.DecisionManualReview,
		},
		{
			name:  "discriminating disagreement vetoes auto-merge",
			rules: []urModel.UnificationRule{email, dob}, ctx: plain,
			incoming: map[string]interface{}{"identity_attributes.email": "j.smith@acme.com", "traits.dob": "1990-04-02"},
			existing: map[string]interface{}{"identity_attributes.email": "j.smith@acme.com", "traits.dob": "1974-11-30"},
			want:     constants.DecisionManualReview,
		},
		{
			// A role mailbox on both sides is agreement on nothing.
			name:  "placeholder value counts as absent, not as a match",
			rules: []urModel.UnificationRule{email, name}, ctx: plain,
			incoming: map[string]interface{}{"identity_attributes.email": "noreply@example.com", "traits.name": "Ann Lee"},
			existing: map[string]interface{}{"identity_attributes.email": "noreply@example.com", "traits.name": "Bob Ray"},
			want:     constants.DecisionUnique,
		},
		{
			name:  "two independent agreements auto-merge",
			rules: []urModel.UnificationRule{email, name, city}, ctx: plain,
			incoming: map[string]interface{}{"identity_attributes.email": "j.smith@acme.com", "traits.name": "Jonathan Smith"},
			existing: map[string]interface{}{"identity_attributes.email": "j.smith@acme.com", "traits.name": "Jonathan Smith"},
			want:     constants.DecisionAutoMerge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := &model.ProfileData{ProfileID: "candidate", Attributes: tt.existing}
			score, breakdown := ScoreCandidate(tt.incoming, candidate, tt.rules, tt.ctx)

			if got := testDecision(score, thresholds); got != tt.want {
				t.Errorf("decision = %s (score %.4f, breakdown %v), want %s", got, score, breakdown, tt.want)
			}
			if score < 0 || score > 1 {
				t.Errorf("score %.4f outside [0,1]", score)
			}
		})
	}
}

// TestScoreCandidateIsIndependentOfRuleCount guards the dilution regression: adding rules
// that neither profile has data for must not change an existing decision.
func TestScoreCandidateIsIndependentOfRuleCount(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	thresholds := model.Thresholds{AutoMergeEnabled: true, AutoMerge: 0.95, ManualReview: 0.75}
	ctx := ScoringContext{OrgHandle: "acme", Thresholds: thresholds}

	incoming := map[string]interface{}{
		"identity_attributes.email": "j.smith@acme.com",
		"traits.name":               "Jonathan Smith",
	}
	candidate := &model.ProfileData{ProfileID: "candidate", Attributes: incoming}

	base := []urModel.UnificationRule{
		testRule("identity_attributes.email", constants.AttributeTypeEmail, constants.UnificationMethodDeterministic, 1),
		testRule("traits.name", constants.AttributeTypeName, constants.UnificationMethodFuzzy, 2),
	}
	baseScore, _ := ScoreCandidate(incoming, candidate, base, ctx)

	padded := base
	for i := 0; i < 8; i++ {
		padded = append(padded, testRule("traits.unused", constants.AttributeTypeFuzzyString,
			constants.UnificationMethodFuzzy, 10+i))
	}
	paddedScore, _ := ScoreCandidate(incoming, candidate, padded, ctx)

	if baseScore != paddedScore {
		t.Errorf("score changed with unrelated rules: %.4f -> %.4f", baseScore, paddedScore)
	}
}
