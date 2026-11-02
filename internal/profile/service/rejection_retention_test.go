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

package service

import (
	"testing"
	"time"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	UnificationModel "github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
)

// TestRuleValuesChanged decides whether an admin's "these are different people" survives a
// profile edit. Clearing rejections on every update meant an unrelated change — a marketing
// preference, a last-seen timestamp — resurrected every pair the admin had dismissed, so
// the same comparisons came back indefinitely.
func TestRuleValuesChanged(t *testing.T) {
	rules := []UnificationModel.UnificationRule{
		{PropertyName: "identity_attributes.email", IsActive: true},
		{PropertyName: "traits.phone", IsActive: true},
		{PropertyName: "traits.retired", IsActive: false},
	}

	tests := []struct {
		name   string
		before map[string]interface{}
		after  map[string]interface{}
		want   bool
	}{
		{
			name:   "unrelated attribute changed",
			before: map[string]interface{}{"identity_attributes.email": "a@acme.com", "traits.city": "Colombo"},
			after:  map[string]interface{}{"identity_attributes.email": "a@acme.com", "traits.city": "Kandy"},
			want:   false,
		},
		{
			name:   "matched attribute changed",
			before: map[string]interface{}{"identity_attributes.email": "a@acme.com"},
			after:  map[string]interface{}{"identity_attributes.email": "b@acme.com"},
			want:   true,
		},
		{
			name:   "matched attribute added",
			before: map[string]interface{}{},
			after:  map[string]interface{}{"traits.phone": "0771234567"},
			want:   true,
		},
		{
			name:   "matched attribute removed",
			before: map[string]interface{}{"traits.phone": "0771234567"},
			after:  map[string]interface{}{},
			want:   true,
		},
		{
			name:   "inactive rule's attribute changed",
			before: map[string]interface{}{"traits.retired": "yes"},
			after:  map[string]interface{}{"traits.retired": "no"},
			want:   false,
		},
		{
			name:   "multi-valued attribute reordered",
			before: map[string]interface{}{"traits.phone": []interface{}{"0771234567", "0712345678"}},
			after:  map[string]interface{}{"traits.phone": []interface{}{"0712345678", "0771234567"}},
			want:   false,
		},
		{
			name:   "multi-valued attribute extended",
			before: map[string]interface{}{"traits.phone": []interface{}{"0771234567"}},
			after:  map[string]interface{}{"traits.phone": []interface{}{"0771234567", "0712345678"}},
			want:   true,
		},
		{
			name:   "nothing changed",
			before: map[string]interface{}{"identity_attributes.email": "a@acme.com", "traits.phone": "0771234567"},
			after:  map[string]interface{}{"identity_attributes.email": "a@acme.com", "traits.phone": "0771234567"},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ruleValuesChanged(tt.before, tt.after, rules); got != tt.want {
				t.Errorf("ruleValuesChanged = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestHasAttributeMatchingAnyRule covers the enqueue gate. A profile with no rule-relevant
// attribute is skipped, but the caller must still enqueue it when it carries a userId,
// because same-userId merging holds with no rules configured at all.
func TestHasAttributeMatchingAnyRule(t *testing.T) {
	rules := []UnificationModel.UnificationRule{
		{PropertyName: "identity_attributes.email", IsActive: true},
		{PropertyName: "traits.retired", IsActive: false},
	}

	if hasAttributeMatchingAnyRule(map[string]interface{}{"traits.city": "Colombo"}, rules) {
		t.Error("a profile with no rule attribute should not match")
	}
	if hasAttributeMatchingAnyRule(map[string]interface{}{"traits.retired": "yes"}, rules) {
		t.Error("an inactive rule should not cause a match")
	}
	if hasAttributeMatchingAnyRule(map[string]interface{}{"identity_attributes.email": nil}, rules) {
		t.Error("a nil value should not count as present")
	}
	if !hasAttributeMatchingAnyRule(map[string]interface{}{"identity_attributes.email": "a@acme.com"}, rules) {
		t.Error("an active rule's attribute should match")
	}
}

func TestToComparableStrings(t *testing.T) {
	if got := toComparableStrings(nil); got != nil {
		t.Errorf("nil => %v, want nil", got)
	}
	if got := toComparableStrings("one"); len(got) != 1 || got[0] != "one" {
		t.Errorf("string => %v", got)
	}
	if got := toComparableStrings([]interface{}{"a", "b"}); len(got) != 2 {
		t.Errorf("slice => %v", got)
	}
	if got := toComparableStrings(42); len(got) != 1 || got[0] != "42" {
		t.Errorf("int => %v", got)
	}
	_ = constants.AttributeTypeEmail
}

// TestShouldResolveAfterUpdate covers the gate that decides whether an update pays for a
// full re-resolution: a blocking-key rewrite plus a candidate query per key group. An
// update that touched nothing a rule matches on would reach the same conclusion the
// previous write already reached, so it must be skipped — except where the userId moved,
// which is a merge trigger in its own right.
func TestShouldResolveAfterUpdate(t *testing.T) {
	profileWrittenAt := time.Now()
	ruleOlderThanProfile := profileWrittenAt.Add(-time.Hour)

	rules := []UnificationModel.UnificationRule{
		{PropertyName: "identity_attributes.email", IsActive: true, UpdatedAt: ruleOlderThanProfile},
	}

	withEmail := func(userID, email, city string) profileModel.Profile {
		return profileModel.Profile{
			UserId:             userID,
			IdentityAttributes: map[string]interface{}{"email": email},
			Traits:             map[string]interface{}{"city": city},
			UpdatedAt:          profileWrittenAt,
		}
	}

	tests := []struct {
		name                 string
		before, after        profileModel.Profile
		matchedValuesChanged bool
		want                 bool
	}{
		{
			// The case the gate exists for: a permanent profile edited in a way no rule
			// looks at previously paid the whole pipeline on every write.
			name:   "unrelated edit on a profile with a userId",
			before: withEmail("u1", "a@acme.com", "Colombo"),
			after:  withEmail("u1", "a@acme.com", "Kandy"),
			want:   false,
		},
		{
			name:                 "matched value changed",
			before:               withEmail("u1", "a@acme.com", "Colombo"),
			after:                withEmail("u1", "b@acme.com", "Colombo"),
			matchedValuesChanged: true,
			want:                 true,
		},
		{
			// Same-userId merging holds with no rules configured, so a profile that has
			// just gained one must be re-examined even though no rule value moved.
			name:   "profile gained a userId",
			before: withEmail("", "a@acme.com", "Colombo"),
			after:  withEmail("u1", "a@acme.com", "Colombo"),
			want:   true,
		},
		{
			name:   "userId changed",
			before: withEmail("u1", "a@acme.com", "Colombo"),
			after:  withEmail("u2", "a@acme.com", "Colombo"),
			want:   true,
		},
		{
			name:                 "matched value changed but nothing matchable remains",
			before:               withEmail("", "a@acme.com", "Colombo"),
			after:                profileModel.Profile{Traits: map[string]interface{}{"city": "Colombo"}},
			matchedValuesChanged: true,
			want:                 false,
		},
		{
			name:   "anonymous profile, unrelated edit",
			before: withEmail("", "a@acme.com", "Colombo"),
			after:  withEmail("", "a@acme.com", "Kandy"),
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldResolveAfterUpdate(tt.before, tt.after, tt.matchedValuesChanged, rules)
			if got != tt.want {
				t.Errorf("shouldResolveAfterUpdate = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestShouldResolveAfterUpdateCatchesUpToANewRule covers the case that makes a newly added
// rule work at all for profiles that already existed.
//
// Such a profile carries no blocking keys for the new attribute, so nothing can match it
// until it is processed again. Gating purely on "did a matched value change" would skip it
// — and for a profile whose values never change again, that is permanent. The backfill
// triggered by activating the rule covers this as well; this is the second chance for the
// profiles it missed.
func TestShouldResolveAfterUpdateCatchesUpToANewRule(t *testing.T) {
	profileWrittenAt := time.Now().Add(-time.Hour)

	profile := profileModel.Profile{
		Traits:    map[string]interface{}{"city": "Colombo"},
		UpdatedAt: profileWrittenAt,
	}

	newRule := []UnificationModel.UnificationRule{
		{PropertyName: "traits.city", IsActive: true, UpdatedAt: time.Now()},
	}
	if !shouldResolveAfterUpdate(profile, profile, false, newRule) {
		t.Error("a profile predating an active rule must be re-resolved so it enters the index")
	}

	establishedRule := []UnificationModel.UnificationRule{
		{PropertyName: "traits.city", IsActive: true, UpdatedAt: profileWrittenAt.Add(-time.Hour)},
	}
	if shouldResolveAfterUpdate(profile, profile, false, establishedRule) {
		t.Error("an unrelated edit under an established rule should not pay for re-resolution")
	}

	inactiveNewRule := []UnificationModel.UnificationRule{
		{PropertyName: "traits.city", IsActive: false, UpdatedAt: time.Now()},
	}
	if shouldResolveAfterUpdate(profile, profile, false, inactiveNewRule) {
		t.Error("an inactive rule indexes nothing, so it should not trigger re-resolution")
	}
}
