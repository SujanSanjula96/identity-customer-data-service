/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
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

// Package model defines the identity resolution types: the profile data and blocking keys the engine works
// on, rule verdicts and match explanations, review tasks, rejections, and the merge thresholds.
package model

import (
	"fmt"
)

// KeyKind separates the key kinds derived from one attribute value. Candidate lookup queries each
// kind on its own, because they do not share a selectivity: a phonetic code deliberately
// covers every spelling variant of a name, while each spelling produces its own LSH bands.
// Grouping them into one lookup lets the broader kind exhaust the candidate cap and
// discard the narrower kind's keys along with it. Not persisted — the stored index is
// just a key, and which kind produced it is only needed while searching.
type KeyKind uint8

const (
	// KeyKindExact is the normalized value itself.
	KeyKindExact KeyKind = iota
	// KeyKindPhonetic is a Double Metaphone code. The broadest kind: every spelling that
	// sounds alike collapses onto it.
	KeyKindPhonetic
	// KeyKindLSH is one MinHash band hash, narrow by construction.
	KeyKindLSH
	// KeyKindSuffix is a phone number's trailing local digits.
	KeyKindSuffix
)

// BlockingKey is one index entry for a profile: the attribute it came from, the key itself,
// and the kind of key it is.
type BlockingKey struct {
	AttributeName string
	KeyValue      string
	Kind          KeyKind
}

// ProfileData is the view of a profile that identity resolution compares: its IDs and its flattened attributes.
type ProfileData struct {
	ProfileID          string
	UserID             string
	ReferenceProfileID string // non-empty if this profile is a child (merged into another)
	Attributes         map[string]interface{}
}

// GetAllAttributeValues returns all non-empty string values for the given attribute name.
func (profile *ProfileData) GetAllAttributeValues(name string) []string {
	if profile.Attributes == nil {
		return nil
	}
	attrValue, ok := profile.Attributes[name]
	if !ok || attrValue == nil {
		return nil
	}
	switch typed := attrValue.(type) {
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
		s := fmt.Sprintf("%v", attrValue)
		if s == "" {
			return nil
		}
		return []string{s}
	}
}

// IsChild returns true if this profile has been merged into another profile.
func (profile *ProfileData) IsChild() bool {
	return profile.ReferenceProfileID != ""
}
