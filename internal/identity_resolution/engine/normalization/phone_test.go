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

package normalization

import "testing"

// TestSamePhoneNumberAcceptsOneSubscriberWrittenManyWays covers the spellings a single
// subscriber genuinely arrives in.
func TestSamePhoneNumberAcceptsOneSubscriberWrittenManyWays(t *testing.T) {
	tests := []struct {
		name, a, b string
	}{
		{"international and national", "+94771234567", "0771234567"},
		{"national without the trunk zero", "+94771234567", "771234567"},
		{"trunk zero kept after the country code", "+94 (0) 77 123 4567", "+94771234567"},
		{"00 standing in for +", "0094771234567", "+94771234567"},
		{"punctuation and spacing", "+94-77-123-4567", "+94 77 123 4567"},
		{"US national form", "+1 415 555 0132", "(415) 555-0132"},
		{"UK national form", "+447700900123", "07700900123"},
		{"eight digit national number", "+6512345678", "12345678"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !SamePhoneNumber(tt.a, tt.b) {
				t.Errorf("%q and %q are the same subscriber, but were read as different", tt.a, tt.b)
			}
		})
	}
}

// TestSamePhoneNumberRejectsDifferentSubscribers covers the pairs a tail comparison cannot
// separate. Each of these shares the last seven digits, which is what the previous
// implementation compared, so each one used to score 0.9 and count as agreement.
func TestSamePhoneNumberRejectsDifferentSubscribers(t *testing.T) {
	tests := []struct {
		name, a, b string
	}{
		{"different operator code", "+94701234567", "+94771234567"},
		{"landline and mobile sharing a tail", "+94112223344", "+94712223344"},
		{"same national number, different country", "+4512345678", "+6512345678"},
		{"two one-digit country codes", "+11234567890", "+71234567890"},
		{"head that is not an assigned country code", "4771234567", "+94771234567"},
		{"last digit differs", "+94771234567", "+94771234568"},
		{"transposed digits", "+14155552756", "+14515552756"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if SamePhoneNumber(tt.a, tt.b) {
				t.Errorf("%q and %q are different subscribers, but were read as one", tt.a, tt.b)
			}
		})
	}
}

// TestSamePhoneNumberIgnoresValuesTooShortToIdentify keeps extensions and short codes from
// standing in for a subscriber number.
func TestSamePhoneNumberIgnoresValuesTooShortToIdentify(t *testing.T) {
	for _, value := range []string{"", "12345", "911", "0"} {
		if SamePhoneNumber(value, value) {
			t.Errorf("%q is too short to identify anyone, but matched itself", value)
		}
	}
}

// TestPhoneReadingsRecordsAnAbsentCountryRatherThanGuessing is what lets a nationally written
// number meet an international one without asserting a country it was never given.
func TestPhoneReadingsRecordsAnAbsentCountryRatherThanGuessing(t *testing.T) {
	readings := PhoneReadings("0771234567")

	var sawUnknownCountry bool
	for _, r := range readings {
		if r.CountryCode == "" && r.National == "771234567" {
			sawUnknownCountry = true
		}
	}
	if !sawUnknownCountry {
		t.Fatalf("expected a reading with no country code, got %+v", readings)
	}

	// The longest assigned code wins, so +9477... is Sri Lanka rather than a shorter prefix.
	var sawSriLanka bool
	for _, r := range PhoneReadings("+94771234567") {
		if r.CountryCode == "94" && r.National == "771234567" {
			sawSriLanka = true
		}
	}
	if !sawSriLanka {
		t.Errorf("expected +94771234567 to be read as country 94, national 771234567")
	}
}
