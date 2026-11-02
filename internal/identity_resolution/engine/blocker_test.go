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
)

// TestFindCandidatesByIndexIsolatesSaturatedKeyKinds guards the regression where a crowded
// bucket took other keys down with it. Every key kind used to share one lookup per
// attribute, so an over-populated phonetic code pushed the result past the cap and the whole
// attribute returned nothing — losing both the exact duplicates and the LSH bands.
//
// The phonetic code is the kind that saturates first: it covers every spelling that sounds
// alike, where each individual spelling produces its own bands. So it has to be looked up
// apart from the bands, not merely apart from the exact key.
func TestFindCandidatesByIndexIsolatesSaturatedKeyKinds(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	keys := []model.BlockingKey{
		{AttributeName: "traits.name", KeyValue: "john smith"},
		{AttributeName: "traits.name", KeyValue: "lsh:deadbeef", Kind: model.KeyKindLSH},
		{AttributeName: "traits.name", KeyValue: "lsh:feedface", Kind: model.KeyKindLSH},
		{AttributeName: "traits.name", KeyValue: "JN SM0", Kind: model.KeyKindPhonetic},
	}

	queriedKinds := map[string]int{}
	lookup := func(_, _ string, keyValues []string, _ string, _ int) ([]string, error) {
		switch {
		case len(keyValues) == 1 && keyValues[0] == "john smith":
			queriedKinds["exact"]++
			return []string{"profile-exact"}, nil
		case len(keyValues) == 1 && keyValues[0] == "JN SM0":
			// A common name: the phonetic bucket is saturated, and the store returns nil to
			// signal "too common to be evidence".
			queriedKinds["phonetic"]++
			return nil, nil
		default:
			queriedKinds["lsh"]++
			return []string{"profile-typo"}, nil
		}
	}

	got := FindCandidatesByIndex(keys, "acme", "self", lookup)

	for _, kind := range []string{"exact", "phonetic", "lsh"} {
		if queriedKinds[kind] != 1 {
			t.Fatalf("each key kind must be looked up on its own; %s was queried %d times (all: %v)",
				kind, queriedKinds[kind], queriedKinds)
		}
	}

	// Both surviving kinds must contribute. The saturated phonetic code costs only its own
	// recall, and in particular must not suppress the typo match the bands exist to find.
	want := map[string]bool{"profile-exact": true, "profile-typo": true}
	if len(got) != len(want) {
		t.Fatalf("saturated phonetic bucket suppressed other kinds: got %v", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected candidate %q", id)
		}
	}
	if got[0] != "profile-exact" {
		t.Errorf("the narrowest kind should head the candidate list, got %v", got)
	}
}

func TestFindCandidatesByIndexDeduplicatesAcrossGroups(t *testing.T) {
	if err := log.Init("error"); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	keys := []model.BlockingKey{
		{AttributeName: "traits.name", KeyValue: "john smith"},
		{AttributeName: "traits.name", KeyValue: "lsh:beef", Kind: model.KeyKindLSH},
		{AttributeName: "identity_attributes.email", KeyValue: "j@acme.com"},
	}
	lookup := func(_, _ string, _ []string, _ string, _ int) ([]string, error) {
		return []string{"p1", "p2"}, nil
	}

	got := FindCandidatesByIndex(keys, "acme", "self", lookup)
	if len(got) != 2 {
		t.Errorf("expected 2 deduplicated candidates, got %v", got)
	}
}

// TestExactBlockingKeyAgreesWithMatcher is the property that makes blocking correct: any
// pair the matcher would score 1.0 must share an exact blocking key, or the two profiles
// are never compared in the first place.
func TestExactBlockingKeyAgreesWithMatcher(t *testing.T) {
	tests := []struct {
		name     string
		attrType string
		a, b     string
	}{
		{"date formats", constants.AttributeTypeDate, "2026-01-02", "Jan 2, 2026"},
		{"date separators", constants.AttributeTypeDate, "2026-01-02", "2026/01/02"},
		{"unique id case", constants.AttributeTypeUniqueID, "ABC123", "abc123"},
		{"unique id padding", constants.AttributeTypeUniqueID, " ABC123 ", "abc123"},
		{"email case", constants.AttributeTypeEmail, "J.Smith@Acme.com", "j.smith@acme.com"},
		{"email sub-address tag", constants.AttributeTypeEmail, "j.smith+news@acme.com", "j.smith@acme.com"},
		{"email trailing root dot", constants.AttributeTypeEmail, "j.smith@acme.com.", "j.smith@acme.com"},
		{"gmail ignores dots", constants.AttributeTypeEmail, "j.smith@gmail.com", "jsmith@gmail.com"},
		{"name token order", constants.AttributeTypeName, "John Smith", "Smith John"},
		{"phone formatting", constants.AttributeTypePhone, "+94 77 123 4567", "94771234567"},
		{"phone punctuation", constants.AttributeTypePhone, "(077) 123-4567", "0771234567"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, verdict := MatchAttribute(tt.a, tt.b, tt.attrType, constants.UnificationModeSmart)
			if verdict == model.VerdictUnknown || score < 1.0 {
				t.Fatalf("precondition: matcher should treat these as identical, got %.4f/%s", score, verdict)
			}

			keyA := ExactBlockingKey(tt.attrType, tt.a)
			keyB := ExactBlockingKey(tt.attrType, tt.b)
			if keyA != keyB {
				t.Errorf("matcher scores 1.0 but blocking keys differ: %q vs %q", keyA, keyB)
			}
		})
	}
}

// TestGenerateBlockingKeysRespectsUnificationMethod checks that a deterministic rule does
// not pay for recall-widening keys it can never score on.
func TestGenerateBlockingKeysRespectsUnificationMethod(t *testing.T) {
	fuzzyKeys := GenerateBlockingKeys(constants.AttributeTypeName, constants.UnificationMethodFuzzy,
		"traits.name", "Jonathan Smith")
	exactKeys := GenerateBlockingKeys(constants.AttributeTypeName, constants.UnificationMethodDeterministic,
		"traits.name", "Jonathan Smith")

	if len(exactKeys) != 1 {
		t.Errorf("deterministic rule should emit only the exact key, got %d: %v", len(exactKeys), exactKeys)
	}
	if exactKeys[0].Kind != model.KeyKindExact {
		t.Errorf("the sole deterministic key must be the exact key, got kind %d", exactKeys[0].Kind)
	}
	if len(fuzzyKeys) <= 1 {
		t.Errorf("fuzzy rule should emit recall-widening keys, got %d", len(fuzzyKeys))
	}

	var exactCount, phoneticCount, lshCount int
	for _, k := range fuzzyKeys {
		switch k.Kind {
		case model.KeyKindExact:
			exactCount++
		case model.KeyKindPhonetic:
			phoneticCount++
		case model.KeyKindLSH:
			lshCount++
		}
	}
	if exactCount != 1 {
		t.Errorf("a fuzzy rule should emit exactly one exact key, got %d", exactCount)
	}
	// Phonetic and LSH keys must be distinguishable, because they are looked up separately:
	// a phonetic code covers every spelling that sounds alike and saturates long before a
	// band does, and sharing a lookup would take the bands down with it.
	if phoneticCount == 0 || lshCount == 0 {
		t.Errorf("a fuzzy NAME rule should emit both phonetic and LSH keys, got %d phonetic and %d LSH",
			phoneticCount, lshCount)
	}
}

func TestGenerateBlockingKeysSkipsEmptyValues(t *testing.T) {
	for _, value := range []string{"", "   ", "\t"} {
		if keys := GenerateBlockingKeys(constants.AttributeTypeEmail, constants.UnificationMethodFuzzy,
			"identity_attributes.email", value); keys != nil {
			t.Errorf("expected no keys for %q, got %v", value, keys)
		}
	}
}

// TestNormalizePhoneInternationalPrefixScoresExact pins what used to be a documented
// limitation. "+94..." and "0094..." are one number, and the matcher now resolves both to the
// same country code and national number, so the pair scores an exact 1.0 rather than the 0.9
// suffix fallback it used to earn.
//
// The blocking keys still differ, because keys are built from the digits as written. That
// costs nothing here: the phone suffix key gathers the pair as candidates regardless, and the
// matcher is what decides.
func TestNormalizePhoneInternationalPrefixScoresExact(t *testing.T) {
	score, verdict := MatchAttribute("+94 77 123 4567", "0094771234567",
		constants.AttributeTypePhone, constants.UnificationModeSmart)
	if verdict == model.VerdictUnknown {
		t.Fatalf("expected a comparable pair, got %s", verdict)
	}
	if score != 1.0 {
		t.Errorf("expected the same number written two ways to score 1.0, got %.4f", score)
	}
}
