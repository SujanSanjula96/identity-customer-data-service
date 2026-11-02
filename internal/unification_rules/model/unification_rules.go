/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
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
	"sort"
	"time"

	shareModel "github.com/wso2/identity-customer-data-service/internal/sharing/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// UnificationRule represents rules for merging user profiles
type UnificationRule struct {
	RuleId            string `json:"rule_id" bson:"rule_id" binding:"required"`
	OrgHandle         string `json:"org_handle" bson:"org_handle" binding:"required"`
	RuleName          string `json:"rule_name" bson:"rule_name" binding:"required"`
	PropertyName      string `json:"property_name" bson:"property_name" binding:"required"`
	PropertyId        string `json:"property_id" bson:"property_id" binding:"required"`
	Priority          int    `json:"priority" bson:"priority" binding:"required"`
	IsActive          bool   `json:"is_active" bson:"is_active" binding:"required"`
	AttributeType     string `json:"attribute_type" bson:"attribute_type"`
	UnificationMethod string `json:"unification_method" bson:"unification_method"`
	// MatchStrength is how much an agreement on this attribute supports a merge, and
	// MismatchStrength how much a disagreement opposes one. They are independent: an
	// email match is strong evidence of sameness while an email mismatch is weak evidence
	// of difference, and a date of birth is the other way round.
	MatchStrength    string    `json:"match_strength" bson:"match_strength"`
	MismatchStrength string    `json:"mismatch_strength" bson:"mismatch_strength"`
	CreatedAt        time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt        time.Time `json:"updated_at" bson:"updated_at"`
	// Set only on reads of the effective rules.
	Origin         string `json:"origin,omitempty" bson:"-"`           // OWNED or SHARED
	OwnerOrgHandle string `json:"owner_org_handle,omitempty" bson:"-"` // The owner org of a shared rule
	State          string `json:"state,omitempty" bson:"-"`            // ACTIVE, or why the rule does not run
	Rank           int    `json:"-" bson:"-"`                          // Position in the evaluation order
}

// ApplyDefaults fills in the fields that older rules predate, deriving each from the
// attribute type so a rule stored before a field existed scores identically to a freshly
// created equivalent. Callers that load rules for matching must apply this, otherwise a
// legacy rule silently matches with an empty method and no evidence weights.
func ApplyDefaults(rule UnificationRule, defaultAttributeType, defaultMethod string,
	matchDefaults, mismatchDefaults map[string]string) UnificationRule {

	if rule.AttributeType == "" {
		rule.AttributeType = defaultAttributeType
	}
	if rule.UnificationMethod == "" {
		rule.UnificationMethod = defaultMethod
	}
	if rule.MatchStrength == "" {
		rule.MatchStrength = matchDefaults[rule.AttributeType]
	}
	if rule.MismatchStrength == "" {
		rule.MismatchStrength = mismatchDefaults[rule.AttributeType]
	}
	return rule
}

// ActiveSortedByPriority returns the active rules in evaluation order, with the fields that
// older rules predate filled in.
//
// Priority order is not cosmetic: the first rule that agrees sets the match score and is the
// one recorded as the reason for the merge, so anything that reads rules for matching must
// see them in the same order. Keeping one implementation is what stops the worker, the
// search path and the review path disagreeing about which rule was responsible.
func ActiveSortedByPriority(rules []UnificationRule, defaultAttributeType, defaultMethod string,
	matchDefaults, mismatchDefaults map[string]string) []UnificationRule {

	active := make([]UnificationRule, 0, len(rules))
	for _, rule := range rules {
		// A shared rule runs only in the ACTIVE state. A rule read from the store has no state.
		if !rule.IsActive || (rule.State != "" && rule.State != shareModel.StateActive) {
			continue
		}
		active = append(active, ApplyDefaults(rule, defaultAttributeType, defaultMethod,
			matchDefaults, mismatchDefaults))
	}

	// The rank orders the shared and owned rules of an organization. Rules without a rank keep
	// the priority order.
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].Rank != active[j].Rank {
			return active[i].Rank < active[j].Rank
		}
		return active[i].Priority < active[j].Priority
	})
	return active
}

// AsExactMatching returns the rules with every fuzzy method replaced by deterministic, for a
// server where tolerant matching is switched off.
//
// The stored rule is left alone — only the reading of it changes — so switching the setting
// back on restores what the operator configured rather than requiring the rules to be
// written again.
//
// Applied where rules are read for evaluation, this covers scoring and indexing together:
// the blocker decides whether to emit phonetic and LSH keys from the method it is handed, so
// a coerced rule stops widening the index as well as stopping tolerating typos.
func AsExactMatching(rules []UnificationRule) []UnificationRule {
	exact := make([]UnificationRule, 0, len(rules))
	for _, rule := range rules {
		rule.UnificationMethod = constants.UnificationMethodDeterministic
		exact = append(exact, rule)
	}

	return exact
}

// PrimaryRuleName returns the name of the rule that drove a match: the highest-priority rule
// whose score reaches the agreement bar.
//
// That is the rule the scorer held accountable, so it is what belongs on the merge as its
// reason. Deriving it from the breakdown rather than from the final score matters because
// the final score may have been capped on its way to a review task — comparing against it
// would find no rule at all and fall back to a generic reason.
// PrimaryRuleName names the rule a merge should be attributed to, reconstructed from the
// score breakdown recorded for the pair.
//
// It has to mirror how ScoreCandidate chose, because the rule that decided is not always the
// first rule that agreed. When an organisation keeps deterministic matches decisive, an
// exact match at any priority ends the evaluation, so a fuzzy rule ranked above it agreed
// but did not decide anything. Attributing the merge to that fuzzy rule would record a
// similarity match where an exact identifier was the actual cause — overstating how
// uncertain the merge was, on the one record that answers "why were these combined?".
//
// The order below follows ScoreCandidate's: a unique identifier matching exactly wins
// outright, then a deterministic agreement if the organisation treats those as decisive,
// then the first agreement of any kind.
func PrimaryRuleName(breakdown map[string]float64, rules []UnificationRule,
	agreementThreshold float64, deterministicMatchDecisive bool) (string, bool) {

	agreed := func(rule UnificationRule) bool {
		score, scored := breakdown[rule.PropertyName]
		return scored && score >= agreementThreshold
	}

	// A unique identifier matching exactly is conclusive on its own, but ScoreCandidate only
	// short-circuits on it when it is the primary signal — the first rule to agree. Looking
	// past that rule would attribute to an identifier the scorer never consulted.
	for _, rule := range rules {
		if !agreed(rule) {
			continue
		}
		if rule.AttributeType == constants.AttributeTypeUniqueID && breakdown[rule.PropertyName] >= 1.0 {
			return rule.RuleName, true
		}
		break
	}

	if deterministicMatchDecisive {
		for _, rule := range rules {
			if rule.UnificationMethod != constants.UnificationMethodFuzzy && agreed(rule) {
				return rule.RuleName, true
			}
		}
	}

	for _, rule := range rules {
		if agreed(rule) {
			return rule.RuleName, true
		}
	}
	return "", false
}

// SharingResourceType is the resource type of a unification rule in a sharing policy.
const SharingResourceType = "UNIFICATION_RULE"

// StateInactiveMissingAttribute is the state of a rule whose organization does not see a compatible
// attribute for the property of the rule.
const StateInactiveMissingAttribute = "INACTIVE_MISSING_ATTRIBUTE"
