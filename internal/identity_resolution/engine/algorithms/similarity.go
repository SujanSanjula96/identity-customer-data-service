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
	"math"
	"strings"
	"unicode/utf8"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

func JaroWinkler(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}
	r1, r2 := []rune(s1), []rune(s2)
	jaro := jaroSimilarity(r1, r2)
	prefixLen := 0
	for i := 0; i < len(r1) && i < len(r2) && i < constants.JaroWinklerMaxPrefix; i++ {
		if r1[i] == r2[i] {
			prefixLen++
		} else {
			break
		}
	}
	return jaro + float64(prefixLen)*constants.JaroWinklerPFactor*(1.0-jaro)
}

func jaroSimilarity(r1, r2 []rune) float64 {
	if len(r1) == 0 && len(r2) == 0 {
		return 1.0
	}
	if len(r1) == 0 || len(r2) == 0 {
		return 0.0
	}

	matchDist := int(math.Max(float64(len(r1)), float64(len(r2))))/2 - 1
	if matchDist < 0 {
		matchDist = 0
	}

	r1Matches := make([]bool, len(r1))
	r2Matches := make([]bool, len(r2))

	matches := 0
	transpositions := 0

	for i := 0; i < len(r1); i++ {
		start := int(math.Max(0, float64(i-matchDist)))
		end := int(math.Min(float64(len(r2)-1), float64(i+matchDist)))
		for j := start; j <= end; j++ {
			if r2Matches[j] || r1[i] != r2[j] {
				continue
			}
			r1Matches[i] = true
			r2Matches[j] = true
			matches++
			break
		}
	}

	if matches == 0 {
		return 0.0
	}

	k := 0
	for i := 0; i < len(r1); i++ {
		if !r1Matches[i] {
			continue
		}
		for !r2Matches[k] {
			k++
		}
		if r1[i] != r2[k] {
			transpositions++
		}
		k++
	}

	m := float64(matches)
	return (m/float64(len(r1)) + m/float64(len(r2)) + (m-float64(transpositions)/2.0)/m) / 3.0
}

func LevenshteinDistance(s1, s2 string) int {
	r1 := []rune(s1)
	r2 := []rune(s2)
	n := len(r1)
	m := len(r2)

	if n == 0 {
		return m
	}
	if m == 0 {
		return n
	}

	prev := make([]int, m+1)
	curr := make([]int, m+1)

	for j := 0; j <= m; j++ {
		prev[j] = j
	}

	for i := 1; i <= n; i++ {
		curr[0] = i
		for j := 1; j <= m; j++ {
			cost := 1
			if r1[i-1] == r2[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}

	return prev[m]
}

func LevenshteinSimilarity(s1, s2 string) float64 {
	dist := LevenshteinDistance(s1, s2)
	maxLen := math.Max(float64(utf8.RuneCountInString(s1)), float64(utf8.RuneCountInString(s2)))
	if maxLen == 0 {
		return 1.0
	}
	return 1.0 - float64(dist)/maxLen
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

var AddressAbbreviations = map[string]string{
	"st":    "street",
	"st.":   "street",
	"ave":   "avenue",
	"ave.":  "avenue",
	"blvd":  "boulevard",
	"blvd.": "boulevard",
	"dr":    "drive",
	"dr.":   "drive",
	"ln":    "lane",
	"ln.":   "lane",
	"rd":    "road",
	"rd.":   "road",
	"ct":    "court",
	"ct.":   "court",
	"pl":    "place",
	"pl.":   "place",
	"cir":   "circle",
	"cir.":  "circle",
	"apt":   "apartment",
	"apt.":  "apartment",
	"ste":   "suite",
	"ste.":  "suite",
	"pkwy":  "parkway",
	"hwy":   "highway",
	"sq":    "square",
	"sq.":   "square",
	"n":     "north",
	"n.":    "north",
	"s":     "south",
	"s.":    "south",
	"e":     "east",
	"e.":    "east",
	"w":     "west",
	"w.":    "west",
}

// ExpandAddressAbbreviations replaces known address abbreviations with full forms.
func ExpandAddressAbbreviations(addr string) string {
	addr = strings.NewReplacer(",", " ", ".", " ", "#", " ").Replace(addr)
	tokens := strings.Fields(addr)
	for i, t := range tokens {
		lower := strings.ToLower(t)
		if expanded, ok := AddressAbbreviations[lower]; ok {
			tokens[i] = expanded
		} else {
			tokens[i] = lower
		}
	}
	return strings.Join(tokens, " ")
}

func JaccardSimilarity(s1, s2 string) float64 {
	tokens1 := Tokenize(s1)
	tokens2 := Tokenize(s2)

	if len(tokens1) == 0 && len(tokens2) == 0 {
		return 1.0
	}
	if len(tokens1) == 0 || len(tokens2) == 0 {
		return 0.0
	}

	set1 := make(map[string]bool, len(tokens1))
	for _, t := range tokens1 {
		set1[t] = true
	}

	set2 := make(map[string]bool, len(tokens2))
	for _, t := range tokens2 {
		set2[t] = true
	}

	intersection := 0
	for t := range set1 {
		if set2[t] {
			intersection++
		}
	}

	union := len(set1)
	for t := range set2 {
		if !set1[t] {
			union++
		}
	}

	if union == 0 {
		return 0.0
	}

	return float64(intersection) / float64(union)
}

// Tokenize splits a string into lowercase tokens, removing empty entries.
func Tokenize(s string) []string {
	parts := strings.Fields(strings.ToLower(s))
	tokens := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			tokens = append(tokens, p)
		}
	}
	return tokens
}
