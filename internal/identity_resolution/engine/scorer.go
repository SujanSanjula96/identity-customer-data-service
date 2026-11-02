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
	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	urModel "github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
)

// bestMatchScore returns the highest score across all pairs of (input element) ×
// (candidate element), and whether any pair was comparable at all. A multi-value attribute
// counts as a match if any one element matches, and counts as unknown only when every
// element pair was unknown.
func bestMatchScore(vals1, vals2 []string, attrType, mode string) (float64, string, bool) {
	best := 0.0
	bestValue := ""
	comparable := false
	for _, v1 := range vals1 {
		for _, v2 := range vals2 {
			s, verdict := MatchAttribute(v1, v2, attrType, mode)
			if verdict == model.VerdictUnknown {
				continue
			}
			if !comparable || s > best {
				best = s
				bestValue = v1
			}
			comparable = true
		}
	}
	return best, bestValue, comparable
}

// evaluateRules applies every rule to the pair and returns one evaluation per rule, in
// priority order. Rules where either side has no value come back as VerdictUnknown.
func evaluateRules(
	inputAttrs map[string]interface{},
	candidate *model.ProfileData,
	rules []urModel.UnificationRule,
	thresholds model.Thresholds,
) []model.RuleEvaluation {
	evaluations := make([]model.RuleEvaluation, 0, len(rules))

	for rank, rule := range rules {
		// Each rule is matched in its own mode, so a deterministic rule stays exact while
		// a fuzzy rule tolerates typos within the same evaluation.
		deterministic := rule.UnificationMethod != constants.UnificationMethodFuzzy

		eval := model.RuleEvaluation{
			PropertyName:     rule.PropertyName,
			AttributeType:    rule.AttributeType,
			Deterministic:    deterministic,
			MatchStrength:    rule.MatchStrength,
			MismatchStrength: rule.MismatchStrength,
			Rank:             rank,
			Verdict:          model.VerdictUnknown,
		}

		vals1 := getStringValues(inputAttrs, rule.PropertyName)
		vals2 := candidate.GetAllAttributeValues(rule.PropertyName)
		if len(vals1) == 0 || len(vals2) == 0 {
			// Absence of data is not evidence — leave it Unknown and score nothing.
			evaluations = append(evaluations, eval)
			continue
		}

		effectiveMode := constants.UnificationModeStrict
		if !deterministic {
			effectiveMode = constants.UnificationModeSmart
		}

		// For multi-value attributes, the best element pair represents the rule.
		score, matchedValue, comparable := bestMatchScore(vals1, vals2, rule.AttributeType, effectiveMode)
		if !comparable {
			// Values were present but identify nobody — placeholders, role mailboxes.
			evaluations = append(evaluations, eval)
			continue
		}
		eval.Score = score
		eval.MatchedValue = matchedValue
		eval.Verdict = model.ClassifyVerdict(score, thresholds.ManualReview,
			constants.ScoreContradictionThreshold)

		evaluations = append(evaluations, eval)
	}

	return evaluations
}

// ScoreCandidate decides how strongly a candidate profile matches the incoming attributes.
//
// The score is not an average. Averaging made a rule's weight depend on how many other
// rules happened to be configured, so adding a rule weakened every existing match, and a
// rule that disagreed pulled a strong match down by exactly as much as a rule with no data
// at all — which meant two sparse profiles scored higher than two well-populated ones.
// Instead one rule is selected to speak for the match and the rest can only object:
//
//  1. Evaluate every rule in its own mode. Rules where either side has no value are
//     Unknown and take no part in what follows.
//  2. Walk the rules in priority order; the first one that agrees becomes the primary
//     signal and its score is the result. Lower-priority rules cannot dilute it.
//  3. A UNIQUE_ID agreeing exactly is conclusive on its own and returns immediately. So
//     is any deterministic rule agreeing, when the organisation has
//     deterministic_match_decisive on — which is the default, and how unification worked
//     before typed matching. Turning it off lets the steps below object.
//  4. A disagreement on a discriminating attribute (national ID, date of birth) vetoes
//     auto-merge — two present, clearly different values of that kind mean different
//     people, whatever else matches.
//  5. If most of the other applicable rules disagree, the evidence is conflicting enough
//     to want a human, so auto-merge is capped to manual review.
//  6. Auto-merge on a single agreeing rule is allowed only when that rule is the
//     operator's highest-priority one. Any other lone agreement — a shared city, a common
//     given name — is capped to manual review.
//
// Caps only ever downgrade AUTO_MERGE to MANUAL_REVIEW. They never suppress a match
// entirely: the primary signal still stands.
//
// The returned breakdown carries each applicable rule's raw score, keyed by property name.
func ScoreCandidate(
	inputAttrs map[string]interface{},
	candidate *model.ProfileData,
	rules []urModel.UnificationRule,
	ctx ScoringContext,
) (float64, map[string]float64) {
	thresholds := ctx.Thresholds
	logger := log.GetLogger()

	breakdown := make(map[string]float64)

	if len(rules) == 0 {
		logger.Warn("Scorer: no rules — returning score 0.0",
			log.String("candidateID", candidate.ProfileID))
		return 0.0, breakdown
	}

	evaluations := evaluateRules(inputAttrs, candidate, rules, thresholds)

	var primary *model.RuleEvaluation
	agreeingCount := 0
	applicableCount := 0
	bestApplicableScore := 0.0

	for i := range evaluations {
		eval := &evaluations[i]
		if !eval.Applicable() {
			continue
		}

		applicableCount++
		breakdown[eval.PropertyName] = eval.Score
		if eval.Score > bestApplicableScore {
			bestApplicableScore = eval.Score
		}
		if eval.Verdict == model.VerdictAgree {
			agreeingCount++
			// Rules arrive in priority order, so the first agreement is the strongest
			// signal the operator configured for this pair.
			if primary == nil {
				primary = eval
			}
		}
	}

	if applicableCount == 0 {
		return 0.0, breakdown
	}

	// Nothing reached the agreement bar. Report the best evidence seen so callers that
	// search with a lower threshold still get a usable ordering; it is below the
	// manual-review threshold by construction, so it cannot trigger a merge or a task.
	if primary == nil {
		return clampScore(bestApplicableScore), breakdown
	}

	finalScore := primary.Score

	// A unique identifier that matches exactly is conclusive on its own.
	if primary.AttributeType == constants.AttributeTypeUniqueID && primary.Score >= 1.0 {
		return 1.0, breakdown
	}

	// Before typed matching, any deterministic rule matching merged the pair, whatever
	// position it held and whatever the other rules said. An organisation that keeps
	// deterministic_match_decisive on keeps exactly that — so the check is over every
	// agreeing rule, not just the primary: a fuzzy rule ranked above an exact match must
	// not demote it to something the other rules can object to.
	if thresholds.DeterministicMatchDecisive {
		for i := range evaluations {
			eval := &evaluations[i]
			if eval.Deterministic && eval.Verdict == model.VerdictAgree {
				logger.Debug("Scorer: deterministic rule agrees and is decisive for this org",
					log.String("candidateID", candidate.ProfileID),
					log.String("property", eval.PropertyName))
				return 1.0, breakdown
			}
		}
	}

	cap := thresholds.AutoMerge - constants.ScorePenaltyOffset

	// Veto: a discriminating attribute that disagrees outweighs whatever agreed.
	for i := range evaluations {
		eval := &evaluations[i]
		if eval == primary || eval.Verdict != model.VerdictDisagree {
			continue
		}
		if eval.MismatchStrength == constants.EvidenceStrengthHigh {
			logger.Debug("Scorer: discriminating attribute disagrees — capping below auto-merge",
				log.String("candidateID", candidate.ProfileID),
				log.String("property", eval.PropertyName))
			if finalScore > cap {
				finalScore = cap
			}
			break
		}
	}

	// Conflicting evidence: most of the other applicable rules actively disagree.
	secondaryCount := applicableCount - 1
	contradictingCount := 0
	for i := range evaluations {
		eval := &evaluations[i]
		if eval != primary && eval.Verdict == model.VerdictDisagree {
			contradictingCount++
		}
	}
	if secondaryCount > 0 && contradictingCount*2 > secondaryCount && finalScore > cap {
		finalScore = cap
	}

	// A lone agreement may carry an auto-merge only when the attribute identifies a person
	// on its own, and only when the matched value is not one many profiles in the tenant
	// already share. A shared corporate address agrees on nothing.
	//
	// Being the operator's highest-priority rule counts as identifying — but it cannot
	// promote evidence the type itself calls weak. Priority is a statement about which
	// signal to prefer among several; with a single configured rule everything is rank 0,
	// so that statement was never made. Without this, an organisation whose only rule is a
	// tolerant name match merges "Ivan Petrov" into "Ivana Petrov" unattended, which is
	// precisely what match_strength LOW exists to prevent.
	if agreeingCount < constants.MinAgreeingRulesForAutoMerge && finalScore > cap {
		identifying := primary.MatchStrength == constants.EvidenceStrengthHigh ||
			(primary.Rank == 0 && primary.MatchStrength != constants.EvidenceStrengthLow)
		if !identifying || ctx.isCommonValue(primary) {
			finalScore = cap
		}
	}

	return clampScore(finalScore), breakdown
}

func clampScore(score float64) float64 {
	if score > 1.0 {
		return 1.0
	}
	if score < 0.0 {
		return 0.0
	}
	return score
}

// ScoringContext carries what the scorer needs beyond the pair itself.
//
// ValueFrequency is injected rather than called directly so the engine stays free of
// storage dependencies and remains testable without a database. When it is nil, rarity is
// simply not consulted and every value is treated as ordinary — scoring degrades to the
// same decisions minus the rarity demotion, never to a wrong one.
type ScoringContext struct {
	OrgHandle      string
	Thresholds     model.Thresholds
	ValueFrequency func(orgHandle, attributeName, keyValue string) (int, error)
}

// isCommonValue reports whether the value that produced this match is already shared by
// enough profiles in the tenant that agreeing on it is coincidence rather than evidence.
func (ctx ScoringContext) isCommonValue(eval *model.RuleEvaluation) bool {
	if ctx.ValueFrequency == nil || eval == nil || eval.MatchedValue == "" {
		return false
	}

	key := ExactBlockingKey(eval.AttributeType, eval.MatchedValue)
	if key == "" {
		return false
	}

	count, err := ctx.ValueFrequency(ctx.OrgHandle, eval.PropertyName, key)
	if err != nil {
		// A frequency we cannot read must not silently license an auto-merge, but it also
		// must not block one — treat the value as ordinary and let the other gates decide.
		log.GetLogger().Warn("Scorer: value frequency unavailable, treating value as ordinary",
			log.String("property", eval.PropertyName), log.Error(err))
		return false
	}

	return count >= constants.RarityCommonMinProfiles
}
