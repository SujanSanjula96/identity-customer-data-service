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
package store

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

func TestSanitizeForAlias(t *testing.T) {
	assert.Equal(t, "app_1_theme", sanitizeForAlias("app-1.theme"))
	assert.Equal(t, "plain", sanitizeForAlias("plain"))
	assert.Equal(t, "_1st", sanitizeForAlias("1st"), "an alias does not start with a digit")
	assert.Equal(t, "a__b_", sanitizeForAlias("a; b'"))
	assert.Equal(t, "", sanitizeForAlias(""))
}

func TestToInt(t *testing.T) {
	tests := []struct {
		name string
		in   interface{}
		want int
		ok   bool
	}{
		{"int", 7, 7, true},
		{"int32", int32(7), 7, true},
		{"int64", int64(7), 7, true},
		{"float32", float32(7.9), 7, true},
		{"float64", 7.9, 7, true},
		{"an integer JSON number", json.Number("7"), 7, true},
		{"a fractional JSON number", json.Number("7.5"), 7, true},
		{"a numeric string", "12", 12, true},
		{"a string that is not a number", "twelve", 0, false},
		{"a bool", true, 0, false},
		{"nil", nil, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toInt(tc.in)

			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestToStringSlice(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	assert.Equal(t, []string{}, toStringSlice(nil))
	assert.Equal(t, []string{"a", "b"}, toStringSlice([]string{"a", "b"}))
	assert.Equal(t, []string{"a", "b"}, toStringSlice([]interface{}{"a", 5, "b"}),
		"items that are not strings are skipped")
	assert.Equal(t, []string{}, toStringSlice("a string"))
}

func TestToIntSlice(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	assert.Equal(t, []int{}, toIntSlice(nil))
	assert.Equal(t, []int{1, 2}, toIntSlice([]int{1, 2}))
	assert.Equal(t, []int{1, 2, 3}, toIntSlice([]interface{}{1, 2.0, "3", true}),
		"items that are not numbers are skipped")
	assert.Equal(t, []int{}, toIntSlice("a string"))
}

func TestContainsHelpers(t *testing.T) {
	assert.True(t, containsString([]string{"a", "b"}, "b"))
	assert.False(t, containsString([]string{"a", "b"}, "c"))
	assert.False(t, containsString(nil, "a"))
	assert.True(t, containsInt([]int{1, 2}, 2))
	assert.False(t, containsInt([]int{1, 2}, 3))
	assert.False(t, containsInt(nil, 1))
}

func TestEnrichFieldValuesCombinesLists(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))

	assert.Equal(t, []string{"a", "b", "c"}, enrichFieldValues([]string{"a", "b"}, []string{"b", "c"}))
	assert.Equal(t, []string{"x"}, enrichFieldValues(nil, []string{"x"}))
	assert.Equal(t, []string{"a", "b"}, enrichFieldValues([]interface{}{"a"}, []string{"b"}))
	assert.Equal(t, []int{1, 2, 3}, enrichFieldValues([]int{1, 2}, []int{2, 3}))
	assert.Equal(t, []int{4}, enrichFieldValues(nil, []int{4}))
}

func TestEnrichFieldValuesOverwritesSimpleValues(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))

	assert.Equal(t, "new", enrichFieldValues("old", "new"))
	assert.Equal(t, 2, enrichFieldValues(1, 2))
	assert.Equal(t, true, enrichFieldValues(false, true))
	assert.Equal(t, 1.5, enrichFieldValues(1.0, 1.5), "a type it does not know is taken as it comes")
}
