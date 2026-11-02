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
	"fmt"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/engine/algorithms"
	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/engine/normalization"
	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	urModel "github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
)

type CandidateLookupFunc func(orgHandle, attributeName string, keyValues []string, excludeProfileID string, maxResults int) ([]string, error)

func FindCandidatesByIndex(
	keys []model.BlockingKey,
	orgHandle string,
	excludeProfileID string,
	candidateLookup CandidateLookupFunc,
) []string {
	logger := log.GetLogger()

	// Each kind of key is looked up on its own, even within one attribute. They share a key
	// space but not a selectivity, and one lookup's cap is shared by everything in it.
	//
	// An exact key is the narrowest. An LSH band covers values close to one spelling. A
	// phonetic code is the broadest by design — every spelling that sounds alike collapses
	// onto it, so for a common name it holds far more profiles than any single band does.
	// Grouped together, the phonetic code alone pushes the lookup past the cap and the bands
	// are discarded with it, losing the typo matches they exist to find. Separated, an
	// over-broad kind costs only its own recall.
	type lookupGroup struct {
		attrName string
		kind     model.KeyKind
	}

	grouped := make(map[lookupGroup][]string)
	for _, k := range keys {
		g := lookupGroup{attrName: k.AttributeName, kind: k.Kind}
		grouped[g] = append(grouped[g], k.KeyValue)
	}

	seen := make(map[string]bool)
	var candidateIDs []string

	lookup := func(g lookupGroup) {
		ids, err := candidateLookup(orgHandle, g.attrName, grouped[g], excludeProfileID, constants.MaxCandidatesPerRule)
		if err != nil {
			logger.Error(fmt.Sprintf("Blocker: query failed for attribute '%s' (kind=%d)", g.attrName, g.kind),
				log.Error(err))
			return
		}
		if ids == nil {
			return
		}

		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				candidateIDs = append(candidateIDs, id)
			}
		}
	}

	// Narrowest kind first, so the most precise matches head the candidate list and the
	// broader kinds only add what they find beyond it.
	for _, kind := range []model.KeyKind{
		model.KeyKindExact,
		model.KeyKindSuffix,
		model.KeyKindLSH,
		model.KeyKindPhonetic,
	} {
		for g := range grouped {
			if g.kind == kind {
				lookup(g)
			}
		}
	}

	return candidateIDs
}

// ExactBlockingKey derives the one key that every value of this attribute type is indexed
// under, regardless of matching method. It is the value reduced exactly as far as the
// attribute's matcher reduces it before comparing, so two values the matcher would call
// identical always share this key.
//
// Frequency lookups use it too: how common a value is means how many profiles share this
// key, so indexing and rarity must agree on what "the same value" is.
func ExactBlockingKey(attrType string, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	switch {
	case constants.FuzzyCapableAttributeTypes[attrType]:
		return normalization.NormalizeForType(value, attrType)
	case attrType == constants.AttributeTypeDate, attrType == constants.AttributeTypeUniqueID:
		// matchDate parses before comparing and matchID lowercases, so the key must too,
		// or "2026-01-02" and "Jan 2, 2026" never meet.
		return normalization.NormalizeForType(value, attrType)
	default:
		// The matcher compares raw values; the key must not reduce further.
		return value
	}
}

// GenerateBlockingKeys builds the index entries for one attribute value.
//
// Every value gets an exact key derived the same way its matcher compares values, so any
// pair the matcher would score 1.0 is guaranteed to land in the same bucket. A rule
// matched fuzzily gets additional recall-widening keys — LSH bands, phonetic codes, a
// phone suffix — so near-misses become candidates too. A deterministic rule gets none of
// those: it can only ever score on exact equality, so keys that merely bring similar
// values together would cost index rows and candidate fetches to produce guaranteed zeros.
func GenerateBlockingKeys(attrType string, method string, attrName string, value string) []model.BlockingKey {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	var keys []model.BlockingKey
	fuzzyMatched := method == constants.UnificationMethodFuzzy && constants.FuzzyCapableAttributeTypes[attrType]

	if constants.FuzzyCapableAttributeTypes[attrType] {
		norm := ExactBlockingKey(attrType, value)
		if norm == "" {
			return nil
		}

		switch attrType {
		case constants.AttributeTypeEmail:
			keys = append(keys, model.BlockingKey{AttributeName: attrName, KeyValue: norm})
			if !fuzzyMatched {
				break
			}
			for _, lshKey := range algorithms.LSHBandHashes(norm) {
				keys = append(keys, model.BlockingKey{AttributeName: attrName, KeyValue: lshKey, Kind: model.KeyKindLSH})
			}

		case constants.AttributeTypePhone:
			keys = append(keys, model.BlockingKey{AttributeName: attrName, KeyValue: norm})
			if !fuzzyMatched {
				break
			}
			// Grab the end of the phone number so we can match users even if they typed a different country code.
			// We limit the length to prevent too many unrelated people from landing in the same search bucket.
			if len(norm) >= constants.PhoneSuffixBlockingLength {
				suffix := norm[len(norm)-constants.PhoneSuffixBlockingLength:]
				if suffix != norm {
					keys = append(keys, model.BlockingKey{AttributeName: attrName, KeyValue: suffix, Kind: model.KeyKindSuffix})
				}
			}

		case constants.AttributeTypeName:
			keys = append(keys, model.BlockingKey{AttributeName: attrName, KeyValue: norm})
			if !fuzzyMatched {
				break
			}
			priPhonetic, altPhonetic := algorithms.DoubleMetaphonePhrase(value)
			if priPhonetic != "" && priPhonetic != norm {
				keys = append(keys, model.BlockingKey{AttributeName: attrName, KeyValue: priPhonetic, Kind: model.KeyKindPhonetic})
			}
			if altPhonetic != "" && altPhonetic != priPhonetic && altPhonetic != norm {
				keys = append(keys, model.BlockingKey{AttributeName: attrName, KeyValue: altPhonetic, Kind: model.KeyKindPhonetic})
			}
			for _, lshKey := range algorithms.LSHBandHashes(norm) {
				keys = append(keys, model.BlockingKey{AttributeName: attrName, KeyValue: lshKey, Kind: model.KeyKindLSH})
			}

		case constants.AttributeTypeLocation:
			keys = append(keys, model.BlockingKey{AttributeName: attrName, KeyValue: norm})
			if !fuzzyMatched {
				break
			}
			for _, lshKey := range algorithms.LSHBandHashes(norm) {
				keys = append(keys, model.BlockingKey{AttributeName: attrName, KeyValue: lshKey, Kind: model.KeyKindLSH})
			}

		case constants.AttributeTypeFuzzyString:
			keys = append(keys, model.BlockingKey{AttributeName: attrName, KeyValue: norm})
			if !fuzzyMatched {
				break
			}
			for _, lshKey := range algorithms.LSHBandHashes(norm) {
				keys = append(keys, model.BlockingKey{AttributeName: attrName, KeyValue: lshKey, Kind: model.KeyKindLSH})
			}
		}
	} else {
		// Exact-only types. The key has to be derived exactly as MatchAttribute derives
		// the values it compares, or pairs the matcher would call identical never meet:
		// matchDate parses before comparing, so "2026-01-02" and "Jan 2, 2026" must share
		// a key, and matchID lowercases, so "ABC123" and "abc123" must too. Types whose
		// matcher compares raw values keep the raw value.
		norm := ExactBlockingKey(attrType, value)
		if norm == "" {
			return nil
		}
		keys = append(keys, model.BlockingKey{AttributeName: attrName, KeyValue: norm})
	}

	return keys
}

// GenerateBlockingKeysFromRules generates blocking keys only for attributes that have active unification rules.
// For multi-value attributes (arrays) it generates one set of blocking keys per element so that each
// individual value is independently searchable during candidate search.
func GenerateBlockingKeysFromRules(flatAttrs map[string]interface{}, rules []urModel.UnificationRule) []model.BlockingKey {
	var allKeys []model.BlockingKey
	for _, rule := range rules {
		for _, strVal := range getStringValues(flatAttrs, rule.PropertyName) {
			keys := GenerateBlockingKeys(rule.AttributeType, rule.UnificationMethod, rule.PropertyName, strVal)
			allKeys = append(allKeys, keys...)
		}
	}
	return allKeys
}

func getStringValues(attrs map[string]interface{}, key string) []string {
	if attrs == nil {
		return nil
	}
	v, ok := attrs[key]
	if !ok || v == nil {
		return nil
	}
	switch typed := v.(type) {
	case string:
		if typed == "" {
			return nil
		}
		return []string{typed}
	case []interface{}:
		var result []string
		for _, elem := range typed {
			if s, ok := elem.(string); ok && s != "" {
				result = append(result, s)
			}
		}
		return result
	case []string:
		var result []string
		for _, s := range typed {
			if s != "" {
				result = append(result, s)
			}
		}
		return result
	default:
		s := fmt.Sprintf("%v", v)
		if s == "" {
			return nil
		}
		return []string{s}
	}
}
