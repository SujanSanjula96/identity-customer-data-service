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
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// LSHBandHashes generates LSH blocking key strings for a normalized string.
func LSHBandHashes(normalized string) []string {
	if len(normalized) < constants.LSHMinLength {
		return nil
	}

	bigrams := CharacterBigrams(normalized)
	if len(bigrams) < 2 {
		return nil
	}

	sig := MinHashSignature(bigrams)
	keys := make([]string, 0, constants.LSHBands)

	for band := 0; band < constants.LSHBands; band++ {
		start := band * constants.LSHRows
		end := start + constants.LSHRows
		h := fnv.New64a()
		buf := make([]byte, 8)
		for i := start; i < end; i++ {
			binary.LittleEndian.PutUint64(buf, sig[i])
			_, _ = h.Write(buf)
		}

		keys = append(keys, fmt.Sprintf("lsh:%x", h.Sum64()))
	}

	return keys
}

// CharacterBigrams extracts all unique character bigrams from a string.
func CharacterBigrams(s string) []string {
	runes := []rune(strings.ToLower(s))
	if len(runes) < 2 {
		return nil
	}

	seen := make(map[string]bool)
	bigrams := make([]string, 0, len(runes)-1)
	for i := 0; i < len(runes)-1; i++ {
		bg := string(runes[i : i+2])
		if !seen[bg] {
			seen[bg] = true
			bigrams = append(bigrams, bg)
		}
	}

	return bigrams
}

// MinHashSignature computes a min-hash signature for a set of bigrams.
func MinHashSignature(bigrams []string) []uint64 {
	sig := make([]uint64, constants.LSHSignatureSize)
	for i := range sig {
		sig[i] = math.MaxUint64
	}

	for _, bg := range bigrams {
		h := fnv.New64a()
		_, _ = h.Write([]byte(bg))
		baseHash := h.Sum64()

		for i := 0; i < constants.LSHSignatureSize; i++ {
			a, b := minHashCoefficients(i)
			hVal := a*baseHash + b

			if hVal < sig[i] {
				sig[i] = hVal
			}
		}
	}

	return sig
}

// minHashCoefficients returns the multiplier and addend for one signature position.
//
// The band probability that LSHBands and LSHRows are chosen against — that two values with
// Jaccard similarity s share at least one band with probability 1-(1-s^rows)^bands —
// assumes the signature positions are independent. Deriving a and b directly from i (a =
// K*i+1, b = L*i+C) does not give that: every position is the same linear function of i, so
// the positions move together and near-duplicates collide far less often than the maths
// predicts. Running i through SplitMix64 first decorrelates them.
//
// The multiplier is forced odd because h(x) = a*x + b mod 2^64 only permutes the space when
// a is odd; an even multiplier cannot let the top bit of x influence the result, which
// costs that position its resolution.
func minHashCoefficients(i int) (uint64, uint64) {
	a := splitMix64(uint64(i)*2+1) | 1
	b := splitMix64(uint64(i)*2 + 2)
	return a, b
}

// splitMix64 is Steele et al.'s finalizing mixer: a bijection on uint64 that spreads
// sequential inputs across the whole range, so consecutive positions get unrelated
// coefficients.
func splitMix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}
