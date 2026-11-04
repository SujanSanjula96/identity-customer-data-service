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

package algorithms

import (
	"math/rand"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// TestMinHashMultipliersAreOdd pins the fix for a defect that silently halved the index's
// resolution. h(x) = a*x + b mod 2^64 only permutes the space when a is odd; with an even
// multiplier the top bit of x cannot influence the result, so that signature position
// collapses. The original a = KnuthMult*i + 1 was even for every odd i, leaving four of the
// eight positions degenerate and making the band maths optimistic.
func TestMinHashMultipliersAreOdd(t *testing.T) {
	seen := make(map[uint64]bool, constants.LSHSignatureSize)
	for i := 0; i < constants.LSHSignatureSize; i++ {
		a, _ := minHashCoefficients(i)
		if a%2 == 0 {
			t.Errorf("signature position %d has even multiplier %d", i, a)
		}
		if seen[a] {
			t.Errorf("signature position %d reuses multiplier %d", i, a)
		}
		seen[a] = true
	}
}

// TestMinHashSignatureVariesAcrossPositions is the observable consequence: a degenerate
// position produces the same minimum for unrelated inputs far more often than a healthy
// one. Distinct inputs should not collapse to an identical signature.
func TestMinHashSignatureVariesAcrossPositions(t *testing.T) {
	a := MinHashSignature(CharacterBigrams("jonathan smith"))
	b := MinHashSignature(CharacterBigrams("wei zhang"))

	if len(a) != constants.LSHSignatureSize || len(b) != constants.LSHSignatureSize {
		t.Fatalf("signature length = %d/%d, want %d", len(a), len(b), constants.LSHSignatureSize)
	}

	identical := 0
	for i := range a {
		if a[i] == b[i] {
			identical++
		}
	}
	if identical == len(a) {
		t.Errorf("unrelated inputs produced identical signatures: %v", a)
	}
}

func TestLSHBandHashesShape(t *testing.T) {
	keys := LSHBandHashes("jonathan smith")
	if len(keys) != constants.LSHBands {
		t.Fatalf("expected %d band keys, got %d", constants.LSHBands, len(keys))
	}
	for _, k := range keys {
		if len(k) < 5 || k[:4] != "lsh:" {
			t.Errorf("band key %q is not lsh-prefixed", k)
		}
	}
}

func TestLSHBandHashesAreDeterministic(t *testing.T) {
	first := LSHBandHashes("12 main street colombo")
	second := LSHBandHashes("12 main street colombo")

	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("band %d not stable: %q vs %q", i, first[i], second[i])
		}
	}
}

// TestLSHBandHashesSkipsShortValues checks the guard that keeps two-character values out
// of the index, where near-everything would collide.
func TestLSHBandHashesSkipsShortValues(t *testing.T) {
	for _, short := range []string{"", "a", "ab", "abc"} {
		if keys := LSHBandHashes(short); keys != nil {
			t.Errorf("expected no band keys for %q, got %v", short, keys)
		}
	}
}

// TestLSHRecallForNearDuplicates exercises the property the index exists for: values that
// differ by a typo must share at least one band, or the pair never becomes a candidate.
func TestLSHRecallForNearDuplicates(t *testing.T) {
	pairs := [][2]string{
		{"jonathan smith", "jonathon smith"},
		{"j.smith@acme.com", "j.smith@acme.co"},
		{"12 main street colombo", "12 main street colomba"},
		{"kandy road peradeniya", "kandy rd peradeniya"},
	}

	for _, pair := range pairs {
		a := LSHBandHashes(pair[0])
		b := LSHBandHashes(pair[1])

		if !sharesBand(a, b) {
			t.Errorf("no shared band for near-duplicates %q / %q — they would never be compared",
				pair[0], pair[1])
		}
	}
}

// TestCharacterBigramsAreUnique guards the set semantics MinHash assumes: repeated bigrams
// must not be counted twice or the Jaccard estimate skews.
func TestCharacterBigramsAreUnique(t *testing.T) {
	bigrams := CharacterBigrams("aaaa")
	if len(bigrams) != 1 || bigrams[0] != "aa" {
		t.Errorf("expected a single unique bigram, got %v", bigrams)
	}

	seen := map[string]bool{}
	for _, bg := range CharacterBigrams("mississippi") {
		if seen[bg] {
			t.Errorf("duplicate bigram %q", bg)
		}
		seen[bg] = true
	}
}

// TestLSHTypoRecallRate measures the property the index is chosen for, rather than trusting
// a handful of hand-picked pairs: how often a single-character typo still lands in a shared
// band. The band configuration (4 bands x 2 rows) is picked so that highly similar values
// almost always collide, and a change that decorrelates or recorrelates the hash family
// shows up here before it shows up as missed duplicates in production.
//
// The seed is fixed so the figure is stable; the floor is set well below the measured rate
// so ordinary drift does not fail the build.
func TestLSHTypoRecallRate(t *testing.T) {
	const (
		samples  = 3000
		minimum  = 0.95
		alphabet = "abcdefghijklmnopqrstuvwxyz"
	)

	rng := rand.New(rand.NewSource(42))
	shared, compared := 0, 0

	for n := 0; n < samples; n++ {
		length := 10 + rng.Intn(15)
		original := make([]byte, length)
		for i := range original {
			original[i] = alphabet[rng.Intn(len(alphabet))]
		}

		typo := append([]byte(nil), original...)
		typo[rng.Intn(length)] = alphabet[rng.Intn(len(alphabet))]
		if string(typo) == string(original) {
			continue
		}

		compared++
		if sharesBand(LSHBandHashes(string(original)), LSHBandHashes(string(typo))) {
			shared++
		}
	}

	rate := float64(shared) / float64(compared)
	if rate < minimum {
		t.Errorf("single-character typo recall = %.3f (%d/%d), want >= %.2f",
			rate, shared, compared, minimum)
	}
	t.Logf("single-character typo recall = %.3f (%d/%d)", rate, shared, compared)
}

func sharesBand(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
