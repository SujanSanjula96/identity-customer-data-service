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

	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/engine/algorithms"
	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/engine/normalization"
	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// MatchAttribute compares two values of one attribute and reports both how similar they
// are and whether the comparison meant anything.
//
// The second return value exists because a score alone cannot distinguish "these are
// different people" from "there was nothing to compare". Both used to surface as 0.0, so a
// missing attribute was indistinguishable from a contradicting one — which let sparse
// profiles outscore well-populated ones. Unknown is returned when either side is empty or
// holds a value that identifies nobody (a placeholder, a role mailbox, a sentinel date);
// callers must treat it as absent evidence and exclude it from scoring entirely.
//
// Only the org's thresholds can separate agreement from contradiction, so a comparable
// pair comes back as Inconclusive here and is classified by the scorer.
func MatchAttribute(val1, val2 string, attrType string, mode string) (float64, model.Verdict) {

	if val1 == "" || val2 == "" {
		return 0.0, model.VerdictUnknown
	}
	if normalization.IsUninformative(val1, attrType) || normalization.IsUninformative(val2, attrType) {
		return 0.0, model.VerdictUnknown
	}

	var score float64

	switch attrType {
	case constants.AttributeTypeName:
		score = matchName(val1, val2, mode)
	case constants.AttributeTypeEmail:
		score = matchEmail(val1, val2, mode)
	case constants.AttributeTypePhone:
		score = matchPhone(val1, val2, mode)
	case constants.AttributeTypeDate:
		score = matchDate(val1, val2)
	case constants.AttributeTypeUniqueID:
		score = matchID(val1, val2)
	case constants.AttributeTypeLocation:
		score = matchLocation(val1, val2, mode)
	case constants.AttributeTypeFuzzyString:
		score = matchFuzzyString(val1, val2, mode)
	case constants.AttributeTypePrimitiveExact:
		score = matchExact(val1, val2)
	default:
		score = matchExact(val1, val2)
	}

	return score, model.VerdictInconclusive
}

func matchName(val1, val2 string, mode string) float64 {
	// Raw exact check first (no normalization).
	if val1 == val2 {
		return 1.0
	}
	// If values are not exactly the same, strict mode returns 0 immediately without further processing.
	if mode == constants.UnificationModeStrict {
		return 0.0
	}

	// Fuzzy mode: normalize then apply algorithms.
	sorted1 := normalization.TokenSortName(val1)
	sorted2 := normalization.TokenSortName(val2)
	if sorted1 == sorted2 {
		return 1.0
	}

	tokens1, tokens2 := strings.Fields(sorted1), strings.Fields(sorted2)

	// Different token counts mean a middle name or an initial on one side only, and there is
	// no honest alignment to make. Compare the names whole in that case.
	if len(tokens1) != len(tokens2) || len(tokens1) == 0 {
		return nameSimilarity(sorted1, sorted2)
	}

	// Compare each part and take the weakest.
	//
	// Comparing the whole name as one string lets a matching surname carry a differing given
	// name: "Ivan Petrov" and "Ivana Petrov" differ in the only part that tells two people
	// apart, yet agree on 11 of 12 characters — and Jaro-Winkler's prefix bonus rewards the
	// shared opening on top of that. Scoring parts separately means the part that disagrees
	// sets the result instead of being averaged away.
	weakest := 1.0
	differing := 0
	differingShareInitial := true

	for i := range tokens1 {
		if score := nameSimilarity(tokens1[i], tokens2[i]); score < weakest {
			weakest = score
		}
		if tokens1[i] != tokens2[i] {
			differing++
			if tokens1[i][0] != tokens2[i][0] {
				differingShareInitial = false
			}
		}
	}

	// One differing part that still shares its first letter is ambiguous rather than absent:
	// a diminutive ("Dmitri"/"Dima") and an initial standing for either of two people
	// ("Kevin"/"Katherine" Jones) look alike from here. Neither is enough to merge on, and
	// both are worth a person's attention, so the score is held at the review bar rather than
	// allowed to fall out of sight.
	if weakest < constants.NameAmbiguousPartFloor && differing == 1 && differingShareInitial &&
		len(tokens1) > 1 {
		return constants.NameAmbiguousPartFloor
	}

	return weakest
}

// nameSimilarity compares two name strings, letting names that sound alike score as alike even
// when they are spelled differently.
func nameSimilarity(a, b string) float64 {
	if a == b {
		return 1.0
	}

	jwScore := algorithms.JaroWinkler(a, b)
	if algorithms.PhoneticSimilarity(a, b) >= 1.0 && jwScore < constants.NamePhoneticExactJWMin {
		return constants.NamePhoneticExactJWMin
	}

	return jwScore
}

// matchPhone compares two written numbers as numbers rather than as strings of digits.
//
// Under strict matching the two spellings must be identical. Otherwise they are resolved into
// country code and national significant number first, so the same subscriber written
// internationally, nationally, with a trunk zero or behind a "00" prefix all compare equal —
// and two subscribers who merely share a tail do not.
//
// This replaced a comparison of the last PhoneSuffixBlockingLength digits. That window sits
// inside the subscriber number and excludes the operator or area code, so it could not tell
// +94 77 123 4567 from +94 70 123 4567, nor one national number from the same digits in
// another country. The suffix is still how phone blocking keys are built — it is a reasonable
// way to gather candidates, just not to decide between them.
func matchPhone(val1, val2 string, mode string) float64 {
	if normalization.NormalizePhone(val1) == normalization.NormalizePhone(val2) {
		return 1.0
	}
	if mode == constants.UnificationModeStrict {
		return 0.0
	}
	if normalization.SamePhoneNumber(val1, val2) {
		return 1.0
	}

	return 0.0
}

func matchDate(val1, val2 string) float64 {
	n1 := normalization.NormalizeDate(val1)
	n2 := normalization.NormalizeDate(val2)

	if n1 == n2 {
		return 1.0
	}
	return 0.0
}

func matchID(val1, val2 string) float64 {
	n1 := strings.TrimSpace(strings.ToLower(val1))
	n2 := strings.TrimSpace(strings.ToLower(val2))
	if n1 == n2 {
		return 1.0
	}
	return 0.0
}

func matchEmail(val1, val2 string, mode string) float64 {
	// Raw exact check first (no normalization).
	if val1 == val2 {
		return 1.0
	}
	if mode == constants.UnificationModeStrict {
		return 0.0
	}

	// Fuzzy mode: normalize then compare.
	n1 := normalization.NormalizeEmail(val1)
	n2 := normalization.NormalizeEmail(val2)
	if n1 == n2 {
		return 1.0
	}

	local1, domain1, ok1 := strings.Cut(n1, "@")
	local2, domain2, ok2 := strings.Cut(n2, "@")
	if !ok1 || !ok2 {
		// Malformed email (no '@')
		return 0.0
	}

	localSim := algorithms.LevenshteinSimilarity(local1, local2)

	domainSim := 1.0
	if domain1 != domain2 {
		domainSim = algorithms.JaroWinkler(domain1, domain2)
	}

	if localSim < domainSim {
		return localSim
	}
	return domainSim
}

func matchExact(val1, val2 string) float64 {
	if val1 == val2 {
		return 1.0
	}
	return 0.0
}

func matchLocation(val1, val2 string, mode string) float64 {
	// Raw exact check first (no normalization).
	if val1 == val2 {
		return 1.0
	}
	if mode == constants.UnificationModeStrict {
		return 0.0
	}

	// Fuzzy mode: normalize then compare.
	n1 := normalization.NormalizeForType(val1, constants.AttributeTypeLocation)
	n2 := normalization.NormalizeForType(val2, constants.AttributeTypeLocation)
	if n1 == n2 {
		return 1.0
	}
	expanded1 := algorithms.ExpandAddressAbbreviations(n1)
	expanded2 := algorithms.ExpandAddressAbbreviations(n2)
	return algorithms.JaccardSimilarity(expanded1, expanded2)
}

func matchFuzzyString(val1, val2 string, mode string) float64 {
	// Raw exact check first (no normalization).
	if val1 == val2 {
		return 1.0
	}
	if mode == constants.UnificationModeStrict {
		return 0.0
	}

	// Fuzzy mode: normalize then compare.
	n1 := normalization.NormalizeForType(val1, constants.AttributeTypeFuzzyString)
	n2 := normalization.NormalizeForType(val2, constants.AttributeTypeFuzzyString)
	if n1 == n2 {
		return 1.0
	}
	jwScore := algorithms.JaroWinkler(n1, n2)
	return jwScore
}
