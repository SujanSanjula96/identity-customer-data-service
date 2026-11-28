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

package client

import (
	"reflect"
	"testing"
	"time"
)

func Test_boolFromValue(t *testing.T) {
	cases := []struct {
		name   string
		value  interface{}
		want   bool
		wantOK bool
	}{
		{"bool true", true, true, true},
		{"bool false", false, false, true},
		{"int64 one", int64(1), true, true},
		{"int64 zero", int64(0), false, true},
		{"uint64 one", uint64(1), true, true},
		{"uint64 zero", uint64(0), false, true},
		{"float64 one", float64(1), true, true},
		{"float64 zero", float64(0), false, true},
		{"bytes 1", []byte("1"), true, true},
		{"bytes 0", []byte("0"), false, true},
		{"string true", "TRUE", true, true},
		{"string false with spaces", " false ", false, true},
		{"string t", "t", true, true},
		{"string f", "f", false, true},
		{"text that is not a boolean", "maybe", false, false},
		{"bytes that are not a boolean", []byte("2"), false, false},
		{"a type that is not a boolean form", struct{}{}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := boolFromValue(c.value)
			if got != c.want || ok != c.wantOK {
				t.Fatalf("boolFromValue(%#v) = %v, %v; expected %v, %v", c.value, got, ok, c.want, c.wantOK)
			}
		})
	}
}

func Test_integerAndFloatFromValue(t *testing.T) {
	if got := integerFromValue([]byte("42")); got != int64(42) {
		t.Errorf("integer text became %#v", got)
	}
	if got := integerFromValue(int64(7)); got != int64(7) {
		t.Errorf("an int64 must be returned as it is, got %#v", got)
	}
	if got := integerFromValue([]byte("4x")); !reflect.DeepEqual(got, []byte("4x")) {
		t.Errorf("unparseable integer text must be kept, got %#v", got)
	}

	if got := floatFromValue([]byte("0.25")); got != 0.25 {
		t.Errorf("float text became %#v", got)
	}
	if got := floatFromValue(0.5); got != 0.5 {
		t.Errorf("a float64 must be returned as it is, got %#v", got)
	}
	if got := floatFromValue([]byte("x")); !reflect.DeepEqual(got, []byte("x")) {
		t.Errorf("unparseable float text must be kept, got %#v", got)
	}
}

func Test_timeFromValue(t *testing.T) {
	moment := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	cases := []struct {
		name  string
		value interface{}
		want  interface{}
	}{
		{"a time in another zone is returned in UTC",
			time.Date(2026, 3, 4, 7, 6, 7, 0, time.FixedZone("x", 7200)), moment},
		{"text with an offset", "2026-03-04 07:06:07+02:00", moment},
		{"text without an offset is read as UTC", "2026-03-04 05:06:07", moment},
		{"RFC 3339 text", "2026-03-04T05:06:07Z", moment},
		{"a date", []byte("2026-03-04"), time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)},
		{"text in no known layout is kept", "yesterday", "yesterday"},
		{"a number is kept", int64(5), int64(5)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := timeFromValue(c.value); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("timeFromValue(%#v) = %#v, expected %#v", c.value, got, c.want)
			}
		})
	}
}

func Test_textFromValue(t *testing.T) {
	if got := textFromValue([]byte("abc")); got != "abc" {
		t.Errorf("bytes became %#v", got)
	}
	if got := textFromValue(int64(3)); got != int64(3) {
		t.Errorf("a value that is not bytes must be returned as it is, got %#v", got)
	}
}
