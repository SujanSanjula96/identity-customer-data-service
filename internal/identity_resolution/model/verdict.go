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

// Verdict is one rule's reading of a candidate pair.
//
// A similarity score alone cannot express the difference between "these values are
// clearly different people" and "I have nothing to compare". Collapsing both onto 0.0
// is what lets a missing attribute look like agreement and a contradicting attribute
// dilute an otherwise strong match, so the two are kept apart here.
type Verdict string

const (
	// VerdictAgree — both values present and similar enough to support the match.
	VerdictAgree Verdict = "AGREE"

	// VerdictInconclusive — both values present, similarity in the band that neither
	// supports nor opposes the match. Counts towards neither side.
	VerdictInconclusive Verdict = "INCONCLUSIVE"

	// VerdictDisagree — both values present and clearly different.
	VerdictDisagree Verdict = "DISAGREE"

	// VerdictUnknown — at least one side has no value. Carries no information in either
	// direction and must never influence the score.
	VerdictUnknown Verdict = "UNKNOWN"
)

// RuleEvaluation is the outcome of applying one unification rule to a candidate pair.
type RuleEvaluation struct {
	PropertyName  string
	AttributeType string
	// Deterministic is true when the rule matches on exact equality rather than similarity.
	Deterministic bool
	// MatchStrength and MismatchStrength are the rule's evidence weights for agreement
	// and disagreement respectively.
	MatchStrength    string
	MismatchStrength string
	// MatchedValue is the input value that produced Score, kept so the value's rarity
	// within the tenant can be judged.
	MatchedValue string
	// Priority rank within the active rule set; 0 is the operator's highest-priority rule.
	Rank    int
	Score   float64
	Verdict Verdict
}

// Applicable reports whether both profiles had a value for this rule.
func (e RuleEvaluation) Applicable() bool {
	return e.Verdict != VerdictUnknown
}

// ClassifyVerdict maps a similarity score to a verdict, given the org's manual-review
// threshold as the bar for agreement.
func ClassifyVerdict(score, manualReviewThreshold, contradictionThreshold float64) Verdict {
	if score >= manualReviewThreshold {
		return VerdictAgree
	}
	if score <= contradictionThreshold {
		return VerdictDisagree
	}
	return VerdictInconclusive
}
