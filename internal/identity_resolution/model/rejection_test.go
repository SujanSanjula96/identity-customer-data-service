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

import "testing"

// TestShouldReconsider covers the judgement that decides whether an administrator sees a
// pair they already dismissed. Getting it wrong in one direction makes the review queue a
// treadmill; in the other it buries genuinely new evidence.
func TestShouldReconsider(t *testing.T) {
	const agreementThreshold = 0.75

	rejected := Rejection{
		OtherProfileID: "other",
		MatchScore:     0.80,
		ScoreBreakdown: map[string]float64{
			"identity_attributes.email": 0.80,
			"traits.name":               0.20,
		},
	}

	tests := []struct {
		name         string
		newScore     float64
		newBreakdown map[string]float64
		want         bool
	}{
		{
			// The case the whole design exists for: an unrelated edit nudges nothing that
			// matters, and the administrator must not be asked again.
			name:     "same evidence",
			newScore: 0.80,
			newBreakdown: map[string]float64{
				"identity_attributes.email": 0.80,
				"traits.name":               0.20,
			},
			want: false,
		},
		{
			name:     "score drifts up slightly",
			newScore: 0.83,
			newBreakdown: map[string]float64{
				"identity_attributes.email": 0.83,
				"traits.name":               0.20,
			},
			want: false,
		},
		{
			name:     "score materially higher",
			newScore: 0.88,
			newBreakdown: map[string]float64{
				"identity_attributes.email": 0.88,
				"traits.name":               0.20,
			},
			want: true,
		},
		{
			// A rule agrees that did not before. The overall score need not move, because
			// the waterfall's primary signal is unchanged — but this is corroboration the
			// administrator never saw.
			name:     "a rule newly agrees without moving the score",
			newScore: 0.80,
			newBreakdown: map[string]float64{
				"identity_attributes.email": 0.80,
				"traits.name":               0.95,
			},
			want: true,
		},
		{
			name:     "an attribute appears that was not compared before",
			newScore: 0.80,
			newBreakdown: map[string]float64{
				"identity_attributes.email": 0.80,
				"traits.name":               0.20,
				"identity_attributes.nic":   1.00,
			},
			want: true,
		},
		{
			name:     "a new attribute appears but does not agree",
			newScore: 0.80,
			newBreakdown: map[string]float64{
				"identity_attributes.email": 0.80,
				"traits.name":               0.20,
				"traits.city":               0.40,
			},
			want: false,
		},
		{
			name:     "score falls",
			newScore: 0.60,
			newBreakdown: map[string]float64{
				"identity_attributes.email": 0.60,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rejected.ShouldReconsider(tt.newScore, tt.newBreakdown, agreementThreshold); got != tt.want {
				t.Errorf("ShouldReconsider = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestShouldReconsiderWithoutStoredEvidence covers a rejection recorded before the evidence
// was retained: its score reads as zero, so any real match clears the margin and the pair is
// proposed once more rather than being suppressed forever on a decision nothing can explain.
func TestShouldReconsiderWithoutStoredEvidence(t *testing.T) {
	bare := Rejection{OtherProfileID: "other"}

	if !bare.ShouldReconsider(0.80, map[string]float64{"identity_attributes.email": 0.80}, 0.75) {
		t.Error("a rejection with no recorded evidence should not suppress a real match indefinitely")
	}
	if bare.ShouldReconsider(0.0, map[string]float64{}, 0.75) {
		t.Error("no evidence either side should not reopen the pair")
	}
}
