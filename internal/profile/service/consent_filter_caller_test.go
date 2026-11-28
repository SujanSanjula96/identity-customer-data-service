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

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A term naming no application would match any application's data, so it is pinned to the
// caller's; terms on other scopes, and terms already naming an application, are kept.
func TestScopeFiltersToCaller(t *testing.T) {
	filters := []string{
		"application_data.device_id eq d1",
		"application_data.other-app.device_id eq d2",
		"traits.interests co read ing",
		"user_id eq u1",
	}

	assert.Equal(t, []string{
		"application_data.crm.device_id eq d1",
		"application_data.other-app.device_id eq d2",
		"traits.interests co read ing",
		"user_id eq u1",
	}, ScopeFiltersToCaller(filters, "crm"))
	assert.Equal(t, filters, ScopeFiltersToCaller(filters, ""), "with no caller the terms are kept")
}

// A profile is selected only by values the caller could read on it.
func TestFiltersVisible(t *testing.T) {
	allowed := map[string]bool{
		"identity_attributes::email":   true,
		"traits::interests":            true,
		"application_data::events::id": true,
	}

	tests := []struct {
		name    string
		filters []string
		caller  string
		want    bool
	}{
		{"no filters", nil, "crm", true},
		{"profile and user ids", []string{"profile_id eq p1", "user_id sw u"}, "", true},
		{"consented identity attribute", []string{"identity_attributes.email eq a@b.c"}, "crm", true},
		{"consented trait", []string{"traits.interests co read"}, "crm", true},
		{"trait not consented", []string{"traits.age eq 32"}, "crm", false},
		{"one term not consented", []string{"traits.interests eq x", "identity_attributes.phone eq 1"}, "crm", false},
		{"caller's consented application data", []string{"application_data.crm.events eq x"}, "crm", true},
		{"caller's application data not consented", []string{"application_data.crm.scores eq 1"}, "crm", false},
		{"another application's data", []string{"application_data.billing.events eq x"}, "crm", false},
		{"application data naming no application", []string{"application_data.events eq x"}, "crm", false},
		{"application data with no caller", []string{"application_data.crm.events eq x"}, "", false},
		{"unknown field", []string{"nickname eq x"}, "crm", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, filtersVisible(tt.filters, allowed, tt.caller))
		})
	}
}

// A profile discloses a requested category's attributes only once it has consented to it;
// a mandatory category's always.
func TestConsentFilterAllowed(t *testing.T) {
	filter := &consentFilter{
		categoryIDs: []string{"marketing", "identity"},
		mandatory:   map[string]bool{"identity": true},
		attributeKeys: map[string]map[string]bool{
			"marketing": {"traits::interests": true},
			"identity":  {"identity_attributes::email": true},
		},
		consents: map[string]map[string]bool{"consented": {"marketing": true}},
	}

	assert.Equal(t, map[string]bool{"traits::interests": true, "identity_attributes::email": true},
		filter.allowed("consented"))
	assert.Equal(t, map[string]bool{"identity_attributes::email": true}, filter.allowed("not-consented"))
}
