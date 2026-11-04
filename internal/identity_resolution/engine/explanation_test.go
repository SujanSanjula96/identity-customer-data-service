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
	"reflect"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	urModel "github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
)

// idRule is a rule whose ID and name are both id, so a reason's rule IDs read as rule names.
func idRule(id, property, attrType, method string, priority int) urModel.UnificationRule {
	rule := namedRule(id, property, attrType, method, priority)
	rule.RuleId = id
	return rule
}

// evaluatePair evaluates one pair and checks that the routing score is the one ScoreCandidate
// reports, since the two must never drift apart.
func evaluatePair(t *testing.T, rules []urModel.UnificationRule, ctx ScoringContext,
	incoming, candidate map[string]interface{}) CandidateEvaluation {

	t.Helper()
	profile := &model.ProfileData{ProfileID: "candidate", Attributes: candidate}
	evaluation := EvaluateCandidate(incoming, profile, rules, ctx)
	if score, _ := ScoreCandidate(incoming, profile, rules, ctx); score != evaluation.Score {
		t.Fatalf("ScoreCandidate returned %.4f, EvaluateCandidate %.4f", score, evaluation.Score)
	}
	return evaluation
}

func reasonCodes(evaluation CandidateEvaluation) []string {
	codes := make([]string, 0, len(evaluation.Explanation.Reasons))
	for _, reason := range evaluation.Explanation.Reasons {
		codes = append(codes, reason.Code)
	}
	return codes
}

func reasonFor(evaluation CandidateEvaluation, code string) (model.ReviewReason, bool) {
	for _, reason := range evaluation.Explanation.Reasons {
		if reason.Code == code {
			return reason, true
		}
	}
	return model.ReviewReason{}, false
}

func ruleOutcome(evaluation CandidateEvaluation, ruleID string) model.RuleOutcome {
	for _, rule := range evaluation.Explanation.Rules {
		if rule.RuleID == ruleID {
			return rule
		}
	}
	return model.RuleOutcome{}
}

var defaultTestThresholds = model.Thresholds{
	AutoMergeEnabled:           true,
	AutoMerge:                  1.0,
	ManualReview:               0.75,
	DeterministicMatchDecisive: true,
}

// TestExplanationSameNameDifferentEmail is the pair a reviewer most needs explained: the name
// matches exactly, so the similarity is 1.0, yet it is held — because the email disagrees and
// a name is not enough on its own. The capped 0.99 explains none of that.
func TestExplanationSameNameDifferentEmail(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	rules := []urModel.UnificationRule{
		idRule("email", "identity_attributes.email", constants.AttributeTypeEmail, constants.UnificationMethodDeterministic, 1),
		idRule("name", "identity_attributes.name", constants.AttributeTypeName, constants.UnificationMethodFuzzy, 10),
		idRule("mobile", "identity_attributes.mobile", constants.AttributeTypePhone, constants.UnificationMethodFuzzy, 2),
	}
	evaluation := evaluatePair(t, rules, ScoringContext{OrgHandle: "acme", Thresholds: defaultTestThresholds},
		map[string]interface{}{
			"identity_attributes.email":  "mdelacroix@othermail.io",
			"identity_attributes.name":   "Marcus Delacroix",
			"identity_attributes.mobile": "+94 77 555 0101",
		},
		map[string]interface{}{
			"identity_attributes.email": "marcus.d@cdsfuzz.io",
			"identity_attributes.name":  "Marcus Delacroix",
		})

	if model.Decide(evaluation.Score, defaultTestThresholds) != constants.DecisionManualReview {
		t.Fatalf("expected the pair to be held for review, score %.4f", evaluation.Score)
	}
	if evaluation.Similarity != 1.0 {
		t.Errorf("similarity is %.4f, want 1.0: the name matched exactly, and the cap is policy", evaluation.Similarity)
	}
	if want := []string{model.ReasonConflictingEvidence, model.ReasonWeakSoleEvidence}; !reflect.DeepEqual(reasonCodes(evaluation), want) {
		t.Errorf("reasons %v, want %v", reasonCodes(evaluation), want)
	}
	if reason, _ := reasonFor(evaluation, model.ReasonConflictingEvidence); !reflect.DeepEqual(reason.RuleIDs, []string{"email"}) {
		t.Errorf("conflicting evidence names %v, want the email rule", reason.RuleIDs)
	}

	email := ruleOutcome(evaluation, "email")
	if email.Verdict != model.VerdictDisagree || email.Score == nil || *email.Score != 0 {
		t.Errorf("email outcome %+v, want DISAGREE at 0", email)
	}
	if name := ruleOutcome(evaluation, "name"); !name.Primary || name.Verdict != model.VerdictAgree {
		t.Errorf("name outcome %+v, want the agreeing primary rule", name)
	}
	// The candidate has no mobile, so the rule had nothing to compare. That is reported as
	// such, not as a score of 0, which would read as a disagreement.
	if mobile := ruleOutcome(evaluation, "mobile"); mobile.Verdict != model.VerdictUnknown || mobile.Score != nil {
		t.Errorf("mobile outcome %+v, want UNKNOWN with no score", mobile)
	}
}

// TestExplanationNamesEveryReasonThatHolds checks that a reason is reported whenever its
// condition holds, not only when it is the one that moved the score.
func TestExplanationNamesEveryReasonThatHolds(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}
	ctx := ScoringContext{OrgHandle: "acme", Thresholds: defaultTestThresholds}

	t.Run("a similar name on its own", func(t *testing.T) {
		rules := []urModel.UnificationRule{
			idRule("name", "identity_attributes.name", constants.AttributeTypeName, constants.UnificationMethodFuzzy, 1),
		}
		evaluation := evaluatePair(t, rules, ctx,
			map[string]interface{}{"identity_attributes.name": "Jonathan Albright"},
			map[string]interface{}{"identity_attributes.name": "Jonathon Albrite"})

		if want := []string{model.ReasonBelowAutoMerge, model.ReasonWeakSoleEvidence}; !reflect.DeepEqual(reasonCodes(evaluation), want) {
			t.Errorf("reasons %v, want %v", reasonCodes(evaluation), want)
		}
		// The threshold is recorded as it was, since the organization can change it later.
		if reason, _ := reasonFor(evaluation, model.ReasonBelowAutoMerge); reason.Threshold == nil || *reason.Threshold != 1.0 {
			t.Errorf("the reason does not carry the auto-merge threshold the match fell short of")
		}
		// Nothing capped this pair, so its similarity and routing score are the same.
		if evaluation.Similarity != evaluation.Score {
			t.Errorf("similarity %.4f differs from an uncapped score %.4f", evaluation.Similarity, evaluation.Score)
		}
	})

	t.Run("a discriminating attribute disagrees", func(t *testing.T) {
		rules := []urModel.UnificationRule{
			idRule("name", "identity_attributes.name", constants.AttributeTypeName, constants.UnificationMethodFuzzy, 1),
			idRule("dob", "identity_attributes.dob", constants.AttributeTypeDate, constants.UnificationMethodDeterministic, 2),
		}
		evaluation := evaluatePair(t, rules, ctx,
			map[string]interface{}{"identity_attributes.name": "Katherine Jones", "identity_attributes.dob": "1990-01-01"},
			map[string]interface{}{"identity_attributes.name": "Katherine Jones", "identity_attributes.dob": "1985-06-12"})

		reason, found := reasonFor(evaluation, model.ReasonDiscriminatingMismatch)
		if !found || !reflect.DeepEqual(reason.RuleIDs, []string{"dob"}) {
			t.Errorf("reasons %+v, want a discriminating mismatch on dob", evaluation.Explanation.Reasons)
		}
	})

	t.Run("a common value", func(t *testing.T) {
		rules := []urModel.UnificationRule{
			idRule("email", "identity_attributes.email", constants.AttributeTypeEmail, constants.UnificationMethodFuzzy, 1),
		}
		common := ScoringContext{OrgHandle: "acme", Thresholds: defaultTestThresholds,
			ValueFrequency: func(string, string, string) (int, error) {
				return constants.RarityCommonMinProfiles + 30, nil
			}}
		evaluation := evaluatePair(t, rules, common,
			map[string]interface{}{"identity_attributes.email": "jane.doe@acme.com"},
			map[string]interface{}{"identity_attributes.email": "jane.doe@acme.com"})

		reason, found := reasonFor(evaluation, model.ReasonCommonValue)
		if !found || reason.SharedByAtLeast != constants.RarityCommonMinProfiles {
			t.Errorf("reasons %+v, want a common value shared by at least %d",
				evaluation.Explanation.Reasons, constants.RarityCommonMinProfiles)
		}
	})
}

// TestExplanationOfAConclusiveMatch covers the matches that end evaluation early: they carry
// no reason unless the organization will not merge them, and the rule that decided is the
// one marked primary, even when a fuzzy rule ranked above it agreed first.
func TestExplanationOfAConclusiveMatch(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	rules := []urModel.UnificationRule{
		idRule("name", "identity_attributes.name", constants.AttributeTypeName, constants.UnificationMethodFuzzy, 1),
		idRule("email", "identity_attributes.email", constants.AttributeTypeEmail, constants.UnificationMethodDeterministic, 2),
	}
	incoming := map[string]interface{}{"identity_attributes.name": "Jon Smith", "identity_attributes.email": "jon@acme.com"}
	candidate := map[string]interface{}{"identity_attributes.name": "John Smith", "identity_attributes.email": "jon@acme.com"}

	merged := evaluatePair(t, rules, ScoringContext{OrgHandle: "acme", Thresholds: defaultTestThresholds}, incoming, candidate)
	if model.Decide(merged.Score, defaultTestThresholds) != constants.DecisionAutoMerge {
		t.Fatalf("expected the exact email to merge the pair, score %.4f", merged.Score)
	}
	if len(merged.Explanation.Reasons) != 0 {
		t.Errorf("a merged pair carries reasons %v", reasonCodes(merged))
	}
	if !ruleOutcome(merged, "email").Primary || ruleOutcome(merged, "name").Primary {
		t.Errorf("the exact email decided the pair but is not the primary rule: %+v", merged.Explanation.Rules)
	}

	manual := defaultTestThresholds
	manual.AutoMergeEnabled = false
	held := evaluatePair(t, rules, ScoringContext{OrgHandle: "acme", Thresholds: manual}, incoming, candidate)
	if want := []string{model.ReasonAutoMergeDisabled}; !reflect.DeepEqual(reasonCodes(held), want) {
		t.Errorf("reasons %v, want %v", reasonCodes(held), want)
	}
}
