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
)

// TestMatchAttributeReportsUnknownSeparatelyFromDisagreement is the distinction the whole
// scorer rests on: a value that identifies nobody must not read as a failed match, because
// a failed match is evidence and an absent one is not.
func TestMatchAttributeReportsUnknownSeparatelyFromDisagreement(t *testing.T) {
	tests := []struct {
		name     string
		a, b     string
		attrType string
		want     model.Verdict
	}{
		{"empty left", "", "j@acme.com", constants.AttributeTypeEmail, model.VerdictUnknown},
		{"empty right", "j@acme.com", "", constants.AttributeTypeEmail, model.VerdictUnknown},
		{"role mailbox", "noreply@acme.com", "noreply@acme.com", constants.AttributeTypeEmail, model.VerdictUnknown},
		{"reserved domain", "a@example.com", "b@example.com", constants.AttributeTypeEmail, model.VerdictUnknown},
		{"plus-tagged role mailbox", "info+sales@acme.com", "info@acme.com", constants.AttributeTypeEmail, model.VerdictUnknown},
		{"n/a text", "N/A", "N/A", constants.AttributeTypeFuzzyString, model.VerdictUnknown},
		{"sentinel date", "1970-01-01", "1970-01-01", constants.AttributeTypeDate, model.VerdictUnknown},
		{"repeated digits phone", "0000000000", "0000000000", constants.AttributeTypePhone, model.VerdictUnknown},
		{"single character name", "J", "J", constants.AttributeTypeName, model.VerdictUnknown},
		{"real values that differ", "a@acme.com", "b@acme.com", constants.AttributeTypeEmail, model.VerdictInconclusive},
		{"real values that agree", "a@acme.com", "a@acme.com", constants.AttributeTypeEmail, model.VerdictInconclusive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, verdict := MatchAttribute(tt.a, tt.b, tt.attrType, constants.UnificationModeSmart)
			if verdict != tt.want {
				t.Errorf("verdict = %s (score %.4f), want %s", verdict, score, tt.want)
			}
			if verdict == model.VerdictUnknown && score != 0 {
				t.Errorf("unknown comparisons must score 0, got %.4f", score)
			}
		})
	}
}

// TestMatchAttributeHonoursMode checks that a deterministic rule stays exact while a fuzzy
// rule on the same attribute type tolerates variation — the two must be able to coexist.
func TestMatchAttributeHonoursMode(t *testing.T) {
	tests := []struct {
		name       string
		a, b       string
		attrType   string
		wantStrict float64
		fuzzyAbove float64
	}{
		{"name typo", "Jonathan Smith", "Jonathon Smith", constants.AttributeTypeName, 0.0, 0.8},
		{"email typo", "j.smith@acme.com", "j.smyth@acme.com", constants.AttributeTypeEmail, 0.0, 0.7},
		{"location abbreviation", "12 Main Street", "12 Main St", constants.AttributeTypeLocation, 0.0, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strict, _ := MatchAttribute(tt.a, tt.b, tt.attrType, constants.UnificationModeStrict)
			if strict != tt.wantStrict {
				t.Errorf("strict score = %.4f, want %.4f", strict, tt.wantStrict)
			}

			fuzzy, _ := MatchAttribute(tt.a, tt.b, tt.attrType, constants.UnificationModeSmart)
			if fuzzy <= tt.fuzzyAbove {
				t.Errorf("fuzzy score = %.4f, want > %.4f", fuzzy, tt.fuzzyAbove)
			}
		})
	}
}

// TestMatchAttributeStrictStillMatchesIdenticalValues guards the case that made the old
// engine equivalent: a deterministic rule must still fire on byte-identical values.
func TestMatchAttributeStrictStillMatchesIdenticalValues(t *testing.T) {
	for _, attrType := range []string{
		constants.AttributeTypeEmail, constants.AttributeTypeName, constants.AttributeTypePrimitiveExact,
		constants.AttributeTypeUniqueID, constants.AttributeTypeLocation, constants.AttributeTypeFuzzyString,
	} {
		score, verdict := MatchAttribute("Exact-Value-1", "Exact-Value-1", attrType, constants.UnificationModeStrict)
		if verdict == model.VerdictUnknown || score != 1.0 {
			t.Errorf("%s: identical values scored %.4f/%s, want 1.0", attrType, score, verdict)
		}
	}
}

func TestClassifyVerdictBands(t *testing.T) {
	const review, contradiction = 0.75, 0.3

	tests := []struct {
		score float64
		want  model.Verdict
	}{
		{1.00, model.VerdictAgree},
		{0.75, model.VerdictAgree},
		{0.74, model.VerdictInconclusive},
		{0.31, model.VerdictInconclusive},
		{0.30, model.VerdictDisagree},
		{0.00, model.VerdictDisagree},
	}

	for _, tt := range tests {
		if got := model.ClassifyVerdict(tt.score, review, contradiction); got != tt.want {
			t.Errorf("score %.2f => %s, want %s", tt.score, got, tt.want)
		}
	}
}
