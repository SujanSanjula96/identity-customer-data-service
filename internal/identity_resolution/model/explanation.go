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

// Reason codes say why a pair went to an administrator rather than being merged. They are
// codes, not sentences, so a client can word them in its own language; the rules a reason
// names are in the explanation alongside it, with everything needed to describe them.
const (
	// ReasonBelowAutoMerge — the rule that matched did not score high enough to merge on.
	ReasonBelowAutoMerge = "BELOW_AUTO_MERGE_THRESHOLD"
	// ReasonDiscriminatingMismatch — a rule whose disagreement means different people
	// (mismatch strength HIGH) disagrees.
	ReasonDiscriminatingMismatch = "DISCRIMINATING_MISMATCH"
	// ReasonConflictingEvidence — most of the other rules that had values disagree.
	ReasonConflictingEvidence = "CONFLICTING_EVIDENCE"
	// ReasonWeakSoleEvidence — one rule agrees, and its evidence is too weak to merge on
	// alone (match strength LOW, or not HIGH and not the highest-priority rule).
	ReasonWeakSoleEvidence = "WEAK_SOLE_EVIDENCE"
	// ReasonCommonValue — one rule agrees, on a value many profiles in the organization share.
	ReasonCommonValue = "COMMON_VALUE"
	// ReasonAutoMergeDisabled — the pair scored for a merge, but the organization has
	// automatic merging turned off.
	ReasonAutoMergeDisabled = "AUTO_MERGE_DISABLED"
	// ReasonRejectionReconsidered — an administrator rejected the pair before, and new
	// evidence brought it back. A returning pair is never merged without review.
	ReasonRejectionReconsidered = "REJECTION_RECONSIDERED"
	// ReasonAutoMergeFailed — the pair scored for a merge, but the merge could not be made.
	ReasonAutoMergeFailed = "AUTO_MERGE_FAILED"
	// ReasonMergedWithAnother — the pair scored for a merge, but the profile was merged with
	// another candidate in the same evaluation, and one evaluation merges once.
	ReasonMergedWithAnother = "MERGED_WITH_ANOTHER_PROFILE"
)

// MatchExplanation is the evidence behind a review task: what every rule found, and why the
// pair was held for review. It is recorded when the task is raised, so it describes the
// decision as it was made even after the rules, the settings or the profiles change.
type MatchExplanation struct {
	Rules   []RuleOutcome  `json:"rules"`
	Reasons []ReviewReason `json:"reasons"`
}

// RuleOutcome is what one rule found for the pair. Score is absent when the rule had
// nothing to compare — either profile lacked the value — which the verdict says as UNKNOWN.
type RuleOutcome struct {
	RuleID            string   `json:"rule_id"`
	RuleName          string   `json:"rule_name"`
	PropertyName      string   `json:"property_name"`
	AttributeType     string   `json:"attribute_type"`
	UnificationMethod string   `json:"unification_method"`
	Priority          int      `json:"priority"`
	MatchStrength     string   `json:"match_strength"`
	MismatchStrength  string   `json:"mismatch_strength"`
	Score             *float64 `json:"score,omitempty"`
	Verdict           Verdict  `json:"verdict"`
	// Primary marks the rule that spoke for the match: the highest-priority rule that agreed.
	Primary bool `json:"primary,omitempty"`
}

// ReviewReason is one reason the pair was held for review, with the rules it is about.
type ReviewReason struct {
	Code    string   `json:"code"`
	RuleIDs []string `json:"rule_ids,omitempty"`
	// Threshold is set on BELOW_AUTO_MERGE_THRESHOLD: the auto-merge threshold the primary
	// rule fell short of. It is the value at the time, which the organization may since have
	// changed.
	Threshold *float64 `json:"threshold,omitempty"`
	// SharedByAtLeast is set on COMMON_VALUE: how many profiles share a value for it to count
	// as common.
	SharedByAtLeast int `json:"shared_by_at_least,omitempty"`
	// PreviousSimilarity is set on REJECTION_RECONSIDERED: the similarity the administrator
	// rejected the pair at.
	PreviousSimilarity *float64 `json:"previous_similarity,omitempty"`
}

// Explain builds the explanation for a set of rule evaluations. The reasons are supplied by
// the caller, which knows why the pair was held.
func Explain(evaluations []RuleEvaluation, primary *RuleEvaluation, reasons []ReviewReason) MatchExplanation {

	rules := make([]RuleOutcome, 0, len(evaluations))
	for i := range evaluations {
		eval := &evaluations[i]
		outcome := RuleOutcome{
			RuleID:            eval.RuleID,
			RuleName:          eval.RuleName,
			PropertyName:      eval.PropertyName,
			AttributeType:     eval.AttributeType,
			UnificationMethod: eval.UnificationMethod,
			Priority:          eval.Priority,
			MatchStrength:     eval.MatchStrength,
			MismatchStrength:  eval.MismatchStrength,
			Verdict:           eval.Verdict,
			Primary:           eval == primary,
		}
		if eval.Applicable() {
			score := eval.Score
			outcome.Score = &score
		}
		rules = append(rules, outcome)
	}
	if reasons == nil {
		reasons = []ReviewReason{}
	}

	return MatchExplanation{Rules: rules, Reasons: reasons}
}

// RuleIDsFor maps property names to the IDs of the rules on them, for reasons a caller
// derives from a score breakdown, which is keyed by property.
func (e MatchExplanation) RuleIDsFor(properties []string) []string {
	ids := make([]string, 0, len(properties))
	for _, property := range properties {
		for _, rule := range e.Rules {
			if rule.PropertyName == property {
				ids = append(ids, rule.RuleID)
				break
			}
		}
	}
	return ids
}

// WithReason returns a copy of the explanation with one more reason. The copy keeps a
// shared explanation safe to extend for one task without changing it for another.
func (e MatchExplanation) WithReason(reason ReviewReason) MatchExplanation {
	reasons := make([]ReviewReason, 0, len(e.Reasons)+1)
	reasons = append(reasons, e.Reasons...)
	e.Reasons = append(reasons, reason)
	return e
}
