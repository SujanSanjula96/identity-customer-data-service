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
package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/profile/model"
)

func requestWithQuery(query string) *http.Request {
	return httptest.NewRequest(http.MethodGet, "/profiles?"+query, nil)
}

func TestParseApplicationDataParams(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		wantInclude bool
		wantIDs     []string
	}{
		{"defaults", "", false, []string{}},
		{"include flag is case insensitive", "includeApplicationData=TRUE", true, []string{}},
		{"include flag must be true", "includeApplicationData=yes", false, []string{}},
		{"wildcard", "application_identifier=*", false, []string{"*"}},
		{"comma separated ids are trimmed", "application_identifier=app-1,%20app-2%20", false, []string{"app-1",
			"app-2"}},
		{"empty entries are dropped", "application_identifier=app-1,,", false, []string{"app-1"}},
		{"both", "includeApplicationData=true&application_identifier=app-1", true, []string{"app-1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseApplicationDataParams(requestWithQuery(tc.query))

			assert.Equal(t, tc.wantInclude, got.IncludeAppData)
			assert.Equal(t, tc.wantIDs, got.RequestedAppIDs)
		})
	}
}

func TestParseRequestedAttributes(t *testing.T) {
	assert.Nil(t, parseRequestedAttributes(requestWithQuery("")))

	got := parseRequestedAttributes(requestWithQuery(
		"attributes=traits.age,traits.city,identity_attributes,application_data.theme"))

	assert.Equal(t, map[string][]string{
		"traits":              {"age", "city"},
		"identity_attributes": {"*"},
		"application_data":    {"theme"},
	}, got)
}

func TestParseFilterStrings(t *testing.T) {
	got := parseFilterStrings([]string{
		"traits.city eq Colombo and traits.age co 3",
		"  user_id eq u-1  ",
		"",
		" and ",
	})

	assert.Equal(t, []string{"traits.city eq Colombo", "traits.age co 3", "user_id eq u-1"}, got)
	assert.Empty(t, parseFilterStrings(nil))
}

func TestParseCommaSeparatedOrRepeated(t *testing.T) {
	got := parseCommaSeparatedOrRepeated([]string{"a,b", " c ", ",", "d,,e"})

	assert.Equal(t, []string{"a", "b", "c", "d", "e"}, got)
	assert.Empty(t, parseCommaSeparatedOrRepeated(nil))
}

func TestSetNestedMapValue(t *testing.T) {
	m := map[string]interface{}{}

	setNestedMapValue(m, "name", "Jane")
	setNestedMapValue(m, "address.city", "Colombo")
	setNestedMapValue(m, "address.geo.lat", 6.9)

	assert.Equal(t, map[string]interface{}{
		"name": "Jane",
		"address": map[string]interface{}{
			"city": "Colombo",
			"geo":  map[string]interface{}{"lat": 6.9},
		},
	}, m)
}

func TestSetNestedMapValueReplacesANonMapOnThePath(t *testing.T) {
	m := map[string]interface{}{"address": "a string"}

	setNestedMapValue(m, "address.city", "Colombo")

	assert.Equal(t, map[string]interface{}{"address": map[string]interface{}{"city": "Colombo"}}, m)
}

func TestExtractClaimKeyFromLocalURI(t *testing.T) {
	assert.Equal(t, "emailaddress", extractClaimKeyFromLocalURI("http://wso2.org/claims/emailaddress"))
	assert.Equal(t, "plain", extractClaimKeyFromLocalURI("plain"))
	assert.Equal(t, "", extractClaimKeyFromLocalURI("http://wso2.org/claims/"))
}

func TestExtractAppIDFromClaims(t *testing.T) {
	tests := []struct {
		name   string
		claims map[string]interface{}
		want   string
	}{
		{"azp wins", map[string]interface{}{"azp": "app-azp", "client_id": "app-client"}, "app-azp"},
		{"falls back to client_id", map[string]interface{}{"client_id": "app-client"}, "app-client"},
		{"empty azp falls back", map[string]interface{}{"azp": "", "client_id": "app-client"}, "app-client"},
		{"wrong type is ignored", map[string]interface{}{"azp": 42}, ""},
		{"nothing", map[string]interface{}{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, extractAppIDFromClaims(tc.claims))
		})
	}
}

func sampleProfile() model.ProfileResponse {
	return model.ProfileResponse{
		ProfileId:          "p-1",
		UserId:             "u-1",
		IdentityAttributes: map[string]interface{}{"email": "a@b.c", "phone": "123"},
		Traits:             map[string]interface{}{"age": 30, "city": "Colombo"},
		ApplicationData: map[string]map[string]interface{}{
			"app-1": {"theme": "dark", "lang": "en"},
			"app-2": {"other": 1},
		},
	}
}

func TestBuildProfileListResponseWithoutAttributesReturnsOnlyTheHeader(t *testing.T) {
	got := buildProfileListResponse([]model.ProfileResponse{sampleProfile()}, nil)

	require.Len(t, got, 1)
	assert.Equal(t, "p-1", got[0].ProfileId)
	assert.Equal(t, "u-1", got[0].UserId)
	assert.Nil(t, got[0].IdentityAttributes)
	assert.Nil(t, got[0].Traits)
	assert.Nil(t, got[0].ApplicationData)
}

func TestBuildProfileListResponseSelectsRequestedFields(t *testing.T) {
	attrs := map[string][]string{
		"identity_attributes": {"email", "missing"},
		"traits":              {"*"},
		"application_data":    {"theme"},
	}

	got := buildProfileListResponse([]model.ProfileResponse{sampleProfile()}, attrs)

	require.Len(t, got, 1)
	assert.Equal(t, map[string]interface{}{"email": "a@b.c"}, got[0].IdentityAttributes)
	assert.Equal(t, map[string]interface{}{"age": 30, "city": "Colombo"}, got[0].Traits)
	// app-2 has no "theme", so it is left out.
	assert.Equal(t, map[string]map[string]interface{}{"app-1": {"theme": "dark"}}, got[0].ApplicationData)
}

func TestBuildProfileListResponseSkipsApplicationDataWhenNothingMatches(t *testing.T) {
	got := buildProfileListResponse([]model.ProfileResponse{sampleProfile()},
		map[string][]string{"application_data": {"nothing"}})

	assert.Nil(t, got[0].ApplicationData)
}

func TestBuildProfileListResponseOfNoProfilesIsEmptyNotNil(t *testing.T) {
	got := buildProfileListResponse(nil, nil)

	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestBuildFuzzyResponseItemsCarriesTheScore(t *testing.T) {
	results := []model.FuzzyMatchResult{{
		Profile:        sampleProfile(),
		MatchScore:     0.91,
		ScoreBreakdown: map[string]float64{"rule-1": 0.91},
	}}

	got := buildFuzzyResponseItems(results, nil)

	require.Len(t, got, 1)
	require.NotNil(t, got[0].MatchScore)
	assert.InDelta(t, 0.91, *got[0].MatchScore, 1e-9)
	assert.Equal(t, map[string]float64{"rule-1": 0.91}, got[0].ScoreBreakdown)
	assert.Equal(t, "p-1", got[0].ProfileId)
}

func TestBuildFuzzyResponseItemsSelectsRequestedFields(t *testing.T) {
	results := []model.FuzzyMatchResult{{Profile: sampleProfile(), MatchScore: 0.5}}

	got := buildFuzzyResponseItems(results, map[string][]string{"identity_attributes": {"email"}})

	require.Len(t, got, 1)
	assert.Equal(t, map[string]interface{}{"email": "a@b.c"}, got[0].IdentityAttributes)
	assert.Nil(t, got[0].Traits)
}
