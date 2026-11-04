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

// legacyRule is a rule as it exists for a tenant that predates typed matching: no
// attribute_type, no unification_method, no strengths — every field the feature added is
// empty and seeded at load time.
func legacyRule(property string, priority int) urModel.UnificationRule {
	return urModel.ApplyDefaults(urModel.UnificationRule{
		PropertyName: property,
		Priority:     priority,
		IsActive:     true,
	}, constants.AttributeTypePrimitiveExact, constants.UnificationMethodDeterministic,
		constants.DefaultMatchStrength, constants.DefaultMismatchStrength)
}

// TestLegacyDeterministicRulesStillMerge is the upgrade contract.
//
// Before typed matching, unification walked the rules in priority order and merged on the
// first exact match, whatever the other rules held. A tenant upgrading with only
// deterministic rules must keep seeing exactly that: any single rule matching exactly still
// merges, regardless of which priority it sits at or how many other rules are configured.
func TestLegacyDeterministicRulesStillMerge(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	thresholds := model.Thresholds{AutoMergeEnabled: true, AutoMerge: 0.95, ManualReview: 0.75}
	ctx := ScoringContext{OrgHandle: "legacy", Thresholds: thresholds}

	rules := []urModel.UnificationRule{
		legacyRule("identity_attributes.email", 1),
		legacyRule("traits.phone", 2),
		legacyRule("identity_attributes.nic", 3),
	}

	// Each rule in turn is the only attribute the two profiles share.
	for _, property := range []string{
		"identity_attributes.email",
		"traits.phone",
		"identity_attributes.nic",
	} {
		t.Run(property, func(t *testing.T) {
			shared := map[string]interface{}{property: "shared-value-123"}
			candidate := &model.ProfileData{ProfileID: "candidate", Attributes: shared}

			score, breakdown := ScoreCandidate(shared, candidate, rules, ctx)
			if decision := model.Decide(score, thresholds); decision != constants.DecisionAutoMerge {
				t.Errorf("exact match on %s gave %s (score %.4f, breakdown %v); "+
					"before typed matching this merged", property, decision, score, breakdown)
			}
		})
	}
}

// TestLegacyRuleWithOneRuleConfigured covers the smallest legacy setup.
func TestLegacyRuleWithOneRuleConfigured(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	thresholds := model.Thresholds{AutoMergeEnabled: true, AutoMerge: 0.95, ManualReview: 0.75}
	ctx := ScoringContext{OrgHandle: "legacy", Thresholds: thresholds}

	rules := []urModel.UnificationRule{legacyRule("identity_attributes.email", 1)}
	shared := map[string]interface{}{"identity_attributes.email": "a@acme.com"}
	candidate := &model.ProfileData{ProfileID: "candidate", Attributes: shared}

	score, _ := ScoreCandidate(shared, candidate, rules, ctx)
	if decision := model.Decide(score, thresholds); decision != constants.DecisionAutoMerge {
		t.Errorf("single legacy rule matching exactly gave %s, want %s", decision, constants.DecisionAutoMerge)
	}
}

// TestLegacyRulesDoNotMergeOnMismatch checks the other half of the contract: exact matching
// stays exact, so values that merely resemble each other must not merge.
func TestLegacyRulesDoNotMergeOnMismatch(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	thresholds := model.Thresholds{AutoMergeEnabled: true, AutoMerge: 0.95, ManualReview: 0.75}
	ctx := ScoringContext{OrgHandle: "legacy", Thresholds: thresholds}

	rules := []urModel.UnificationRule{legacyRule("identity_attributes.email", 1)}

	incoming := map[string]interface{}{"identity_attributes.email": "a.smith@acme.com"}
	candidate := &model.ProfileData{ProfileID: "candidate",
		Attributes: map[string]interface{}{"identity_attributes.email": "a.smyth@acme.com"}}

	score, _ := ScoreCandidate(incoming, candidate, rules, ctx)
	if decision := model.Decide(score, thresholds); decision == constants.DecisionAutoMerge {
		t.Errorf("a deterministic rule merged two different values (score %.4f)", score)
	}
}

// TestTwoLegacyRulesWithConflictingEvidence pins what deterministic_match_decisive decides.
//
// Before typed matching, the first rule to match merged the pair outright whatever the other
// rules held. With the setting on — the default, and what every organisation predating it
// gets — that is still exactly what happens. With it off, the other applicable rules may
// object: when most of them disagree, the merge is downgraded to a review task. With exactly
// two rules a single disagreement is already a majority, so the two columns differ on the
// most ordinary legacy setup there is.
func TestTwoLegacyRulesWithConflictingEvidence(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	rules := []urModel.UnificationRule{
		legacyRule("identity_attributes.email", 1),
		legacyRule("traits.phone", 2),
	}

	tests := []struct {
		name         string
		incoming     map[string]interface{}
		existing     map[string]interface{}
		wantDecisive string
		wantOpen     string
	}{
		{
			name:         "both attributes agree",
			incoming:     map[string]interface{}{"identity_attributes.email": "a@acme.com", "traits.phone": "0771234567"},
			existing:     map[string]interface{}{"identity_attributes.email": "a@acme.com", "traits.phone": "0771234567"},
			wantDecisive: constants.DecisionAutoMerge,
			wantOpen:     constants.DecisionAutoMerge,
		},
		{
			// Same email, different phone. Decisive merges as before; open asks.
			name:         "top rule agrees, the other contradicts",
			incoming:     map[string]interface{}{"identity_attributes.email": "a@acme.com", "traits.phone": "0771234567"},
			existing:     map[string]interface{}{"identity_attributes.email": "a@acme.com", "traits.phone": "0777654321"},
			wantDecisive: constants.DecisionAutoMerge,
			wantOpen:     constants.DecisionManualReview,
		},
		{
			name:         "lower rule agrees, the top one contradicts",
			incoming:     map[string]interface{}{"identity_attributes.email": "a@acme.com", "traits.phone": "0771234567"},
			existing:     map[string]interface{}{"identity_attributes.email": "b@acme.com", "traits.phone": "0771234567"},
			wantDecisive: constants.DecisionAutoMerge,
			wantOpen:     constants.DecisionManualReview,
		},
		{
			// A missing value is not a disagreement, so it must not block the merge either way.
			name:         "top rule agrees, the other has no value to compare",
			incoming:     map[string]interface{}{"identity_attributes.email": "a@acme.com", "traits.phone": "0771234567"},
			existing:     map[string]interface{}{"identity_attributes.email": "a@acme.com"},
			wantDecisive: constants.DecisionAutoMerge,
			wantOpen:     constants.DecisionAutoMerge,
		},
		{
			name:         "lower rule agrees, the top one has no value to compare",
			incoming:     map[string]interface{}{"identity_attributes.email": "a@acme.com", "traits.phone": "0771234567"},
			existing:     map[string]interface{}{"traits.phone": "0771234567"},
			wantDecisive: constants.DecisionAutoMerge,
			wantOpen:     constants.DecisionAutoMerge,
		},
		{
			// Decisive only short-circuits an agreement; it never manufactures one.
			name:         "neither agrees",
			incoming:     map[string]interface{}{"identity_attributes.email": "a@acme.com", "traits.phone": "0771234567"},
			existing:     map[string]interface{}{"identity_attributes.email": "b@acme.com", "traits.phone": "0777654321"},
			wantDecisive: constants.DecisionUnique,
			wantOpen:     constants.DecisionUnique,
		},
	}

	for _, decisive := range []bool{true, false} {
		modeName := "open to objection"
		if decisive {
			modeName = "decisive (default)"
		}
		thresholds := model.Thresholds{AutoMergeEnabled: true, AutoMerge: 0.95, ManualReview: 0.75,
			DeterministicMatchDecisive: decisive}
		ctx := ScoringContext{OrgHandle: "legacy", Thresholds: thresholds}

		for _, tt := range tests {
			want := tt.wantOpen
			if decisive {
				want = tt.wantDecisive
			}
			t.Run(modeName+"/"+tt.name, func(t *testing.T) {
				candidate := &model.ProfileData{ProfileID: "candidate", Attributes: tt.existing}
				score, breakdown := ScoreCandidate(tt.incoming, candidate, rules, ctx)
				if got := model.Decide(score, thresholds); got != want {
					t.Errorf("decision = %s (score %.4f, breakdown %v), want %s",
						got, score, breakdown, want)
				}
			})
		}
	}
}

// TestDeterministicMatchDecisiveLeavesFuzzyRulesOpen checks the setting's boundary: it
// only ever vouches for an exact match. A fuzzy agreement still has to survive the other
// rules' objections, however the organisation has the setting.
func TestDeterministicMatchDecisiveLeavesFuzzyRulesOpen(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	thresholds := model.Thresholds{AutoMergeEnabled: true, AutoMerge: 0.95, ManualReview: 0.75,
		DeterministicMatchDecisive: true}
	ctx := ScoringContext{OrgHandle: "acme", Thresholds: thresholds}

	name := testRule("traits.name", constants.AttributeTypeName, constants.UnificationMethodFuzzy, 1)
	dob := testRule("traits.dob", constants.AttributeTypeDate, constants.UnificationMethodDeterministic, 2)
	email := testRule("identity_attributes.email", constants.AttributeTypeEmail, constants.UnificationMethodDeterministic, 3)

	t.Run("fuzzy agreement with a deterministic disagreement still asks", func(t *testing.T) {
		incoming := map[string]interface{}{"traits.name": "Jonathan Smith", "traits.dob": "1990-01-02"}
		existing := map[string]interface{}{"traits.name": "Jonathon Smith", "traits.dob": "1991-07-09"}
		candidate := &model.ProfileData{ProfileID: "candidate", Attributes: existing}

		score, breakdown := ScoreCandidate(incoming, candidate, []urModel.UnificationRule{name, dob}, ctx)
		if got := model.Decide(score, thresholds); got != constants.DecisionManualReview {
			t.Errorf("decision = %s (score %.4f, breakdown %v), want %s",
				got, score, breakdown, constants.DecisionManualReview)
		}
	})

	t.Run("a deterministic agreement below a fuzzy one is still decisive", func(t *testing.T) {
		// Name agrees first and would otherwise be the primary signal; dob then disagrees
		// and would veto. The exact email match at the lowest priority merged before typed
		// matching regardless of where it sat, so it must still carry the pair.
		incoming := map[string]interface{}{"traits.name": "Jonathan Smith", "traits.dob": "1990-01-02",
			"identity_attributes.email": "j.smith@acme.com"}
		existing := map[string]interface{}{"traits.name": "Jonathon Smith", "traits.dob": "1991-07-09",
			"identity_attributes.email": "j.smith@acme.com"}
		candidate := &model.ProfileData{ProfileID: "candidate", Attributes: existing}

		score, breakdown := ScoreCandidate(incoming, candidate, []urModel.UnificationRule{name, dob, email}, ctx)
		if got := model.Decide(score, thresholds); got != constants.DecisionAutoMerge {
			t.Errorf("decision = %s (score %.4f, breakdown %v), want %s",
				got, score, breakdown, constants.DecisionAutoMerge)
		}
	})
}
