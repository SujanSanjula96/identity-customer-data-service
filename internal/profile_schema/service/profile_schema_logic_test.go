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
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

func TestParseFilter(t *testing.T) {
	field, op, value, err := parseFilter("application_identifier eq app 1")

	require.NoError(t, err)
	assert.Equal(t, "application_identifier", field)
	assert.Equal(t, "eq", op)
	// The value is everything after the operator, so it may contain spaces.
	assert.Equal(t, "app 1", value)
}

func TestParseFilterRejectsAMalformedCondition(t *testing.T) {
	for _, in := range []string{"", "attribute_name", "attribute_name eq"} {
		_, _, _, err := parseFilter(in)

		var clientError *errors2.ClientError
		require.ErrorAs(t, err, &clientError, in)
		assert.Equal(t, http.StatusBadRequest, clientError.StatusCode)
		assert.Equal(t, errors2.INVALID_FILTER_FORMAT.Code, clientError.Code)
	}
}

func TestMatches(t *testing.T) {
	attr := model.ProfileSchemaAttribute{AttributeName: "traits.city", ApplicationIdentifier: "app-1"}

	tests := []struct {
		name            string
		field, op, want string
		matches         bool
	}{
		{"name equals", "attribute_name", "eq", "traits.city", true},
		{"name differs", "attribute_name", "eq", "traits.age", false},
		{"name contains", "attribute_name", "contains", "city", true},
		{"app equals", "application_identifier", "eq", "app-1", true},
		{"app contains", "application_identifier", "contains", "pp-", true},
		{"unknown operator", "attribute_name", "sw", "traits", false},
		{"unknown field", "scope", "eq", "traits", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.matches, matches(attr, tc.field, tc.op, tc.want))
		})
	}
}

func TestMatchesAllFilters(t *testing.T) {
	attr := model.ProfileSchemaAttribute{AttributeName: "application_data.theme", ApplicationIdentifier: "app-1"}

	tests := []struct {
		name    string
		filters []string
		want    bool
	}{
		{"no filters match everything", nil, true},
		{"eq on name", []string{"attribute_name eq application_data.theme"}, true},
		{"co on name", []string{"attribute_name co theme"}, true},
		{"sw on name", []string{"attribute_name sw application_data."}, true},
		{"sw that does not match", []string{"attribute_name sw traits."}, false},
		{"eq on application", []string{"application_identifier eq app-1"}, true},
		{"eq on another application", []string{"application_identifier eq app-2"}, false},
		{"every condition must hold", []string{"attribute_name co theme", "application_identifier eq app-2"}, false},
		{"all conditions hold", []string{"attribute_name co theme", "application_identifier eq app-1"}, true},
		{"unknown field never matches", []string{"scope eq traits"}, false},
		{"malformed condition never matches", []string{"attribute_name eq"}, false},
		{"unknown operator does not filter", []string{"attribute_name like theme"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, matchesAllFilters(attr, tc.filters))
		})
	}
}

func TestAttributeKey(t *testing.T) {
	tests := []struct {
		name string
		attr model.ProfileSchemaAttribute
		want string
	}{
		{"trait", model.ProfileSchemaAttribute{AttributeName: "traits.city"}, "traits.city"},
		{"application attribute is scoped to its app", model.ProfileSchemaAttribute{
			AttributeName: "application_data.theme", ApplicationIdentifier: "app-1"},
			"app-1\x00application_data.theme"},
		{"application attribute without an app", model.ProfileSchemaAttribute{
			AttributeName: "application_data.theme"}, "application_data.theme"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, attributeKey(tc.attr))
		})
	}

	// The same name under two applications is two different attributes.
	a := model.ProfileSchemaAttribute{AttributeName: "application_data.theme", ApplicationIdentifier: "app-1"}
	b := model.ProfileSchemaAttribute{AttributeName: "application_data.theme", ApplicationIdentifier: "app-2"}
	assert.NotEqual(t, attributeKey(a), attributeKey(b))
}

func TestKeysOf(t *testing.T) {
	keys := keysOf(map[string]bool{"b": true, "a": false, "c": true})

	sort.Strings(keys)
	assert.Equal(t, []string{"a", "b", "c"}, keys)
	assert.Empty(t, keysOf(nil))
}
