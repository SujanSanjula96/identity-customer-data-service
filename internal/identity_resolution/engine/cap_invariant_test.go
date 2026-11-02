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

// TestCappedMatchStillReachesReview is the invariant the scorer documents: an objection only
// ever downgrades an automatic merge to a review task, and never suppresses a match outright.
//
// The cap is auto_merge − ScorePenaltyOffset, so it only lands above the review threshold
// while the two thresholds are far enough apart. With them equal the capped score fell below
// the review threshold and the pair was dropped with no task raised, which is why admin
// config now validates the two against each other. These are the threshold combinations that
// validation admits, and every one of them must still produce a review task.
func TestCappedMatchStillReachesReview(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	// Name agrees outright; date of birth is present on both sides and differs, and DATE
	// carries a HIGH mismatch strength, so the veto fires and the score is capped.
	//
	// The names are identical rather than merely similar. What is under test is the cap, so
	// the primary score should not depend on how the name comparator happens to score a near
	// miss — this test previously pinned 0.9714 and broke when that comparison changed.
	rules := []urModel.UnificationRule{
		testRule("traits.name", constants.AttributeTypeName, constants.UnificationMethodFuzzy, 1),
		testRule("traits.dob", constants.AttributeTypeDate, constants.UnificationMethodDeterministic, 2),
	}
	incoming := map[string]interface{}{"traits.name": "Jonathan Smith", "traits.dob": "1990-01-02"}
	existing := map[string]interface{}{"traits.name": "Jonathan Smith", "traits.dob": "1991-07-09"}

	// Every combination admitted by validateThresholds whose review threshold is also below
	// the primary rule's score. That second condition is the test's own precondition rather
	// than a property of the engine: a match scoring under the review threshold is not
	// review-worthy to begin with, so capping cannot be what suppressed it.
	const primaryScore = 1.0

	combinations := []struct{ autoMerge, manualReview float64 }{
		{0.95, 0.75},
		{0.95, 0.94},
		{0.95, 0.31},
		{0.80, 0.79},
		{0.76, 0.75},
		{0.97, 0.96},
	}

	for _, c := range combinations {
		if c.manualReview >= primaryScore {
			t.Fatalf("test setup: review threshold %.2f is above the primary score %.4f, so this "+
				"pair would not reach review even without an objection", c.manualReview, primaryScore)
		}
		thresholds := model.Thresholds{
			AutoMergeEnabled: true,
			AutoMerge:        c.autoMerge,
			ManualReview:     c.manualReview,
			// Off, so the deterministic rule's disagreement is actually weighed rather than
			// the evaluation ending early on a deterministic agreement.
			DeterministicMatchDecisive: false,
		}
		ctx := ScoringContext{OrgHandle: "acme", Thresholds: thresholds}
		candidate := &model.ProfileData{ProfileID: "candidate", Attributes: existing}

		score, breakdown := ScoreCandidate(incoming, candidate, rules, ctx)

		if decision := model.Decide(score, thresholds); decision != constants.DecisionManualReview {
			t.Errorf("auto_merge=%.2f review=%.2f: capped match gave %s (score %.4f, breakdown %v); "+
				"an objection must downgrade a merge to a review task, never suppress it",
				c.autoMerge, c.manualReview, decision, score, breakdown)
		}
	}
}
