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

import "github.com/wso2/identity-customer-data-service/internal/system/constants"

// Rejection is an administrator's decision that two profiles are different people, together
// with the evidence that decision was made against.
//
// The evidence is what makes the decision durable. A rejection is a statement about
// identity, not about the data at the time — two different people do not become the same
// person because one of them changed a phone number — so an attribute changing is not by
// itself a reason to ask again. Keeping the score and the per-rule breakdown lets a later
// match be compared against what the administrator actually saw.
type Rejection struct {
	// OtherProfileID is the profile on the far side of the pair from the one looked up.
	OtherProfileID string
	// MatchScore is the score at the time of rejection. Zero for a rejection recorded
	// before the evidence was retained, which is treated as "no evidence to compare".
	MatchScore float64
	// ScoreBreakdown is each applicable rule's score at the time of rejection.
	ScoreBreakdown map[string]float64
}

// ShouldReconsider reports whether a fresh evaluation is strong enough to put a previously
// rejected pair back in front of an administrator.
//
// Two things qualify, and nothing else does:
//
//   - the match is materially stronger than the one that was rejected, or
//   - a rule agrees now that did not agree when the pair was rejected, which is new
//     corroboration the administrator never saw — a profile acquiring the other's national
//     ID, say, where the overall score need not move because the primary signal is unchanged.
//
// Everything else leaves the rejection standing. Re-proposing a pair on any change at all
// is what turns a review queue into a treadmill: the administrator dismisses the same two
// profiles repeatedly because an unrelated attribute moved.
func (r Rejection) ShouldReconsider(newScore float64, newBreakdown map[string]float64,
	agreementThreshold float64) bool {

	if newScore >= r.MatchScore+constants.RejectionReconsiderMargin {
		return true
	}

	for property, score := range newBreakdown {
		if score < agreementThreshold {
			continue
		}
		if previous, seen := r.ScoreBreakdown[property]; !seen || previous < agreementThreshold {
			return true
		}
	}

	return false
}
