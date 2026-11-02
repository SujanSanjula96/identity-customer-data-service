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
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

func nameScore(t *testing.T, a, b string) float64 {
	t.Helper()
	score, _ := MatchAttribute(a, b, constants.AttributeTypeName, constants.UnificationMethodFuzzy)

	return score
}

// TestNameMatchingSurnameCannotInflateADifferingGivenName is the property this comparison
// provides: a name scores no better than its weakest part, so agreement on the surname cannot
// raise a pair above what the given names justify.
//
// Comparing the whole name as one string did exactly that — "Ivan Petrov" scored 0.9833
// against "Ivana Petrov" because 11 of 12 characters agreed and Jaro-Winkler rewarded the
// shared opening on top, while the given names alone are worth 0.96.
//
// Note what this does not claim. Reducing the score is not on its own enough to stop an
// unattended merge; some of these pairs still sit above the default auto-merge threshold.
// What stops that is the lone-agreement gate refusing to let LOW evidence merge by itself,
// which TestLoneLowStrengthRuleCannotAutoMerge covers.
func TestNameMatchingSurnameCannotInflateADifferingGivenName(t *testing.T) {
	pairs := []struct{ aGiven, bGiven, shared string }{
		{"Ivan", "Ivana", "Petrov"},
		{"Michael", "Michelle", "Brown"},
		{"Ravi", "Rani", "Perera"},
		{"Stephen", "Stephanie", "Smith"},
		{"Katherine", "Kevin", "Jones"},
	}

	for _, p := range pairs {
		full := nameScore(t, p.aGiven+" "+p.shared, p.bGiven+" "+p.shared)
		givenOnly := nameSimilarity(strings.ToLower(p.aGiven), strings.ToLower(p.bGiven))

		if full > givenOnly+1e-9 {
			t.Errorf("%q/%q sharing the surname %q scored %.4f, above the %.4f the given names "+
				"are worth — the matching surname inflated the pair",
				p.aGiven, p.bGiven, p.shared, full, givenOnly)
		}
	}
}

// TestNameOneAmbiguousPartStaysReviewable keeps a diminutive, or an initial that could stand for
// either of two people, at the review bar. Taking the weakest part alone would drop these below
// it, where no task is raised and the pair is never seen.
func TestNameOneAmbiguousPartStaysReviewable(t *testing.T) {
	pairs := [][2]string{
		{"Dmitri Petrov", "Dima Petrov"},
		{"Katarzyna Zielinski", "Kasia Zielinski"},
	}

	for _, p := range pairs {
		score := nameScore(t, p[0], p[1])
		if score < constants.NameAmbiguousPartFloor {
			t.Errorf("%q and %q scored %.4f, below the review bar %.2f — the pair would never "+
				"be raised", p[0], p[1], score, constants.NameAmbiguousPartFloor)
		}
		if score >= 0.95 {
			t.Errorf("%q and %q scored %.4f — an ambiguous part must not reach auto-merge",
				p[0], p[1], score)
		}
	}
}

// TestNameMatchesSurviveSpellingAndOrder keeps the comparisons the engine is meant to forgive.
func TestNameMatchesSurviveSpellingAndOrder(t *testing.T) {
	tests := []struct {
		name, a, b string
		min        float64
	}{
		{"token order", "John Smith", "Smith John", 1.0},
		{"case and padding", "  john smith ", "John Smith", 1.0},
		{"phonetic spelling of the surname", "Jonathan Smith", "Jonathan Smyth", 0.9},
		{"typo in the given name", "Jonathan Smith", "Jonathon Smith", 0.9},
		{"middle initial on one side only", "Katherine Jones", "Katherine A. Jones", 0.75},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if score := nameScore(t, tt.a, tt.b); score < tt.min {
				t.Errorf("%q and %q scored %.4f, want at least %.2f", tt.a, tt.b, score, tt.min)
			}
		})
	}
}
