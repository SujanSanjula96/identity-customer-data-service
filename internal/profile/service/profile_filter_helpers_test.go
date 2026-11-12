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
	"net/http"
	"testing"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

func TestIsValidFilterKey(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	tests := map[string]bool{
		"traits.city":                         true,
		"identity_attributes.email":           true,
		"application_data.app-1.loyalty_tier": true,
		"traits.address.city":                 true,
		"":                                    false,
		".traits":                             false,
		"traits.":                             false,
		"traits..city":                        false,
		"traits.city; DROP TABLE profiles":    false,
		"traits.city'":                        false,
		"traits.city or 1=1":                  false,
		"traits.(city)":                       false,
	}
	for key, want := range tests {
		assert.Equal(t, want, isValidFilterKey(key), key)
	}
}

func TestParseTypedValueForFilters(t *testing.T) {
	assert.Equal(t, 42, parseTypedValueForFilters("int", "42"))
	assert.Equal(t, 0, parseTypedValueForFilters("int", "not a number"))
	assert.Equal(t, 1.5, parseTypedValueForFilters("float", "1.5"))
	assert.Equal(t, 2.25, parseTypedValueForFilters("double", "2.25"))
	assert.Equal(t, true, parseTypedValueForFilters("boolean", "true"))
	assert.Equal(t, false, parseTypedValueForFilters("boolean", "yes"))
	assert.Equal(t, "x", parseTypedValueForFilters("string", "x"))
	assert.Equal(t, "x", parseTypedValueForFilters("", "x"))
	assert.Equal(t, "x", parseTypedValueForFilters("unknown", "x"))
}

func TestDeepMerge(t *testing.T) {
	dst := map[string]interface{}{
		"a":      1,
		"nested": map[string]interface{}{"keep": "yes", "change": "old"},
		"scalar": "text",
	}
	src := map[string]interface{}{
		"b":      2,
		"nested": map[string]interface{}{"change": "new", "add": true},
		"scalar": map[string]interface{}{"now": "a map"},
	}

	got := DeepMerge(dst, src)

	assert.Equal(t, map[string]interface{}{
		"a":      1,
		"b":      2,
		"nested": map[string]interface{}{"keep": "yes", "change": "new", "add": true},
		"scalar": map[string]interface{}{"now": "a map"},
	}, got)
}

func TestFilteredListRefusals(t *testing.T) {
	env := newProfileEnv(t)
	tests := map[string][]string{
		"a filter without a value":    {"traits.city eq"},
		"a filter with one word":      {"traits.city"},
		"an unsupported operator":     {"traits.city gt 5"},
		"a key with a dangerous form": {"traits.city;drop eq x"},
		"a key with two dots":         {"traits..city eq x"},
	}
	for name, filters := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := env.svc.GetAllProfilesWithFilterCursor(env.ctx, env.orgID, filters, 10, nil)

			_ = requireStatus(t, err, http.StatusBadRequest)
		})
	}
}

func TestFilteredListWithAnApplicationDataFilter(t *testing.T) {
	env := newProfileEnv(t)
	env.seedSchemaForApp(t, "app-1")
	env.seedSchemaForApp(t, "app-2")
	dark, err := env.svc.CreateProfile(env.ctx, profileRequestWithApp("app-1", "dark"), env.orgID)
	require.NoError(t, err)
	_, err = env.svc.CreateProfile(env.ctx, profileRequestWithApp("app-1", "light"), env.orgID)
	require.NoError(t, err)
	otherApp, err := env.svc.CreateProfile(env.ctx, profileRequestWithApp("app-2", "dark"), env.orgID)
	require.NoError(t, err)

	ids := func(filter string) []string {
		page, _, err := env.svc.GetAllProfilesWithFilterCursor(env.ctx, env.orgID, []string{filter}, 10, nil)
		require.NoError(t, err)
		out := make([]string, 0, len(page))
		for _, profile := range page {
			out = append(out, profile.ProfileId)
		}
		return out
	}

	assert.ElementsMatch(t, []string{dark.ProfileId}, ids("application_data.app-1.theme eq dark"))
	assert.ElementsMatch(t, []string{dark.ProfileId, otherApp.ProfileId}, ids("application_data.theme eq dark"),
		"without an application the attribute is matched in every application")
	assert.ElementsMatch(t, []string{dark.ProfileId}, ids("application_data.app-1.theme sw da"))
}

func profileRequestWithApp(appID, theme string) profileModel.ProfileRequest {
	return profileModel.ProfileRequest{
		ApplicationData: map[string]interface{}{appID: map[string]interface{}{"theme": theme}},
	}
}
