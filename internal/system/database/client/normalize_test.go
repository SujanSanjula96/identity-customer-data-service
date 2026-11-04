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

package client

import (
	"reflect"
	"testing"
	"time"
)

func Test_normalizeSQLiteValue(t *testing.T) {

	reference := time.Date(2026, 7, 25, 6, 48, 58, 169070000, time.UTC)

	testCases := []struct {
		name         string
		value        interface{}
		declaredType string
		expected     interface{}
	}{
		{"json text becomes bytes", `{"a":1}`, "JSONB", []byte(`{"a":1}`)},
		{"json bytes are left alone", []byte(`{"a":1}`), "JSONB", []byte(`{"a":1}`)},
		{"json null stays null", nil, "JSONB", nil},
		{"bool one is true", int64(1), "BOOLEAN", true},
		{"bool zero is false", int64(0), "BOOLEAN", false},
		{"bool is already a bool", true, "BOOLEAN", true},
		{"bool text is parsed", "true", "BOOL", true},
		{"bool null stays null", nil, "BOOLEAN", nil},
		{"time is already a time", reference, "TIMESTAMP", reference},
		{"time text is parsed", "2026-07-25 06:48:58.16907+00:00", "TIMESTAMP", reference},
		{"time text without a fraction is parsed", "2026-07-25 06:48:58+00:00", "TIMESTAMP",
			time.Date(2026, 7, 25, 6, 48, 58, 0, time.UTC)},
		{"time text written by the old driver format is parsed",
			"2026-07-25 06:48:58.16907 +0000 UTC", "TIMESTAMPTZ", reference},
		{"time bytes are parsed", []byte("2026-07-25 06:48:58.16907+00:00"), "DATETIME", reference},
		{"unparseable time is left alone", "not a time", "TIMESTAMP", "not a time"},
		{"time null stays null", nil, "TIMESTAMP", nil},
		{"other types are left alone", "plain", "VARCHAR(255)", "plain"},
		{"a cast column has no declared type", "plain", "", "plain"},
		{"integers are left alone", int64(7), "INTEGER", int64(7)},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := normalizeSQLiteValue(testCase.value, testCase.declaredType)
			if !reflect.DeepEqual(actual, testCase.expected) {
				t.Fatalf("expected %#v, got %#v", testCase.expected, actual)
			}

			// Normalizing an already normalized value must not change it, since a
			// column may already arrive with the target type.
			again := normalizeSQLiteValue(actual, testCase.declaredType)
			if !reflect.DeepEqual(again, actual) {
				t.Fatalf("normalization is not idempotent: %#v became %#v", actual, again)
			}
		})
	}
}

func Test_normalizeSQLiteValue_timeIsUTC(t *testing.T) {

	normalized := normalizeSQLiteValue("2026-07-25 12:18:58.16907+05:30", "TIMESTAMP")

	parsed, ok := normalized.(time.Time)
	if !ok {
		t.Fatalf("expected a time.Time, got %#v", normalized)
	}
	if parsed.Location() != time.UTC {
		t.Fatalf("expected the value in UTC, got %s", parsed.Location())
	}
	if expected := time.Date(2026, 7, 25, 6, 48, 58, 169070000, time.UTC); !parsed.Equal(expected) {
		t.Fatalf("expected %s, got %s", expected, parsed)
	}
}
