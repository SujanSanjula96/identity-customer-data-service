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

package utils

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type decodeTarget struct {
	Name       string            `json:"name"`
	Count      int               `json:"count"`
	Enabled    *bool             `json:"enabled"`
	Tags       []string          `json:"tags"`
	Raw        []byte            `json:"raw"`
	Attributes map[string]string `json:"attributes"`
	Nested     decodeNested      `json:"nested"`
	Items      []decodeNested    `json:"items"`
	When       time.Time         `json:"when"`
}

type decodeNested struct {
	Value float64 `json:"value"`
}

func decodeMessage(t *testing.T, body string, target any) string {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(target)
	if err == nil {
		t.Fatalf("decoding %s did not fail", body)
	}
	return HandleDecodeError(err, "test")
}

func TestHandleDecodeErrorNamesTheFieldAndTheJSONType(t *testing.T) {
	tests := []struct {
		body     string
		expected string
	}{
		{`{"name": 1}`, "Invalid type for field 'name'. Expected a string in test request body."},
		{`{"count": "1"}`, "Invalid type for field 'count'. Expected a number in test request body."},
		{`{"enabled": "yes"}`, "Invalid type for field 'enabled'. Expected a boolean in test request body."},
		{`{"tags": "a"}`, "Invalid type for field 'tags'. Expected an array in test request body."},
		{`{"raw": 1}`, "Invalid type for field 'raw'. Expected a string in test request body."},
		{`{"attributes": []}`, "Invalid type for field 'attributes'. Expected an object in test request body."},
		{`{"nested": 1}`, "Invalid type for field 'nested'. Expected an object in test request body."},
		{`{"nested": {"value": "x"}}`,
			"Invalid type for field 'nested.value'. Expected a number in test request body."},
		{`{"items": 1}`, "Invalid type for field 'items'. Expected an array in test request body."},
	}
	for _, tc := range tests {
		t.Run(tc.body, func(t *testing.T) {
			message := decodeMessage(t, tc.body, &decodeTarget{})

			assert.Equal(t, tc.expected, message)
			assert.NotContains(t, message, "decode")
			assert.NotContains(t, message, "utils.")
			assert.NotContains(t, message, "map[")
			assert.NotContains(t, message, "[]")
		})
	}
}

// TestHandleDecodeErrorNamesNoGoType covers errors whose text differs between Go versions: the path
// of an array element, and a type that reads itself from JSON.
func TestHandleDecodeErrorNamesNoGoType(t *testing.T) {
	for _, body := range []string{`{"items": [1]}`, `{"when": 1}`} {
		t.Run(body, func(t *testing.T) {
			message := decodeMessage(t, body, &decodeTarget{})

			assert.NotContains(t, message, "decodeNested")
			assert.NotContains(t, message, "time.Time")
			assert.NotContains(t, message, "utils.")
		})
	}
	assert.Contains(t, decodeMessage(t, `{"items": [1]}`, &decodeTarget{}), "Expected an object")
}

func TestHandleDecodeErrorOtherCases(t *testing.T) {
	assert.Equal(t, "Request body for test is empty.", decodeMessage(t, ``, &decodeTarget{}))
	assert.Equal(t, "Malformed JSON in test request body (unexpected end of input).",
		decodeMessage(t, `{"name":`, &decodeTarget{}))
	assert.Equal(t, "Malformed JSON in test request body.", decodeMessage(t, `{"name" 1}`, &decodeTarget{}))
	assert.Equal(t, `Unknown field "other" in test request body.`, decodeMessage(t, `{"other": 1}`, &decodeTarget{}))
	assert.Equal(t, "Request body for test must be a JSON array.", decodeMessage(t, `{}`, &[]decodeTarget{}))
	assert.Equal(t, "Invalid JSON type for test request body.", decodeMessage(t, `[]`, &decodeTarget{}))
	assert.Equal(t, "", HandleDecodeError(nil, "test"))
}
