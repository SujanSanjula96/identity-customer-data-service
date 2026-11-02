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

import (
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

func TestIsUninformative(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		attrType string
		want     bool
	}{
		{"empty", "", constants.AttributeTypeFuzzyString, true},
		{"whitespace", "   ", constants.AttributeTypeFuzzyString, true},
		{"n/a", "N/A", constants.AttributeTypeFuzzyString, true},
		{"none", "None", constants.AttributeTypeFuzzyString, true},
		{"dash", "-", constants.AttributeTypeFuzzyString, true},
		{"real text", "Colombo", constants.AttributeTypeFuzzyString, false},

		{"role mailbox", "noreply@acme.com", constants.AttributeTypeEmail, true},
		{"role mailbox tagged", "info+orders@acme.com", constants.AttributeTypeEmail, true},
		{"reserved domain", "jane@example.com", constants.AttributeTypeEmail, true},
		{"real email", "jane.doe@acme.com", constants.AttributeTypeEmail, false},
		{"real email named info-something", "information.desk@acme.com", constants.AttributeTypeEmail, false},

		{"repeated digits", "0000000000", constants.AttributeTypePhone, true},
		{"too short", "12345", constants.AttributeTypePhone, true},
		{"real phone", "0771234567", constants.AttributeTypePhone, false},

		{"single letter name", "J", constants.AttributeTypeName, true},
		{"real name", "Jo", constants.AttributeTypeName, false},

		{"epoch date", "1970-01-01", constants.AttributeTypeDate, true},
		{"zero date", "0001-01-01", constants.AttributeTypeDate, true},
		{"real date", "1990-04-02", constants.AttributeTypeDate, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsUninformative(tt.value, tt.attrType); got != tt.want {
				t.Errorf("IsUninformative(%q, %s) = %v, want %v", tt.value, tt.attrType, got, tt.want)
			}
		})
	}
}

func TestNormalizeDateAcceptsCommonFormats(t *testing.T) {
	for _, value := range []string{"2026-01-02", "2026/01/02", "Jan 2, 2026", "January 2, 2026", "02-Jan-2026"} {
		if got := NormalizeDate(value); got != "2026.01.02" {
			t.Errorf("NormalizeDate(%q) = %q, want 2026.01.02", value, got)
		}
	}
}

// TestNormalizeDateLeavesAmbiguousFormatsAlone documents a deliberate choice: numeric
// day/month orders are not parsed, because 03/04/2026 is a valid date under both the US and
// EU readings and guessing wrong silently merges the wrong people.
func TestNormalizeDateLeavesAmbiguousFormatsAlone(t *testing.T) {
	for _, value := range []string{"03/04/2026", "04/03/2026"} {
		if got := NormalizeDate(value); got != value {
			t.Errorf("NormalizeDate(%q) = %q, want the input unchanged", value, got)
		}
	}
}

func TestTokenSortNameIsOrderInsensitive(t *testing.T) {
	if TokenSortName("John Smith") != TokenSortName("Smith John") {
		t.Error("token order should not change the normalized name")
	}
	if TokenSortName("  JOHN   smith ") != TokenSortName("john smith") {
		t.Error("case and spacing should not change the normalized name")
	}
	// An apostrophe sits inside a name part and is dropped; a hyphen joins two parts and
	// becomes a separator, so the hyphenated and spaced spellings agree.
	if TokenSortName("O'Brien-Smith") != TokenSortName("obrien smith") {
		t.Errorf("hyphen should separate and apostrophe should drop: %q vs %q",
			TokenSortName("O'Brien-Smith"), TokenSortName("obrien smith"))
	}
	if TokenSortName("Mary-Jane Watson") != TokenSortName("Watson Mary Jane") {
		t.Errorf("hyphenated given name should tokenize like the spaced form: %q vs %q",
			TokenSortName("Mary-Jane Watson"), TokenSortName("Watson Mary Jane"))
	}
}

func TestNormalizePhoneKeepsDigitsOnly(t *testing.T) {
	if got := NormalizePhone("+94 (77) 123-4567"); got != "94771234567" {
		t.Errorf("NormalizePhone = %q, want 94771234567", got)
	}
}

func TestNormalizeForTypeIsStableAcrossCalls(t *testing.T) {
	types := []string{
		constants.AttributeTypeName, constants.AttributeTypeEmail, constants.AttributeTypePhone,
		constants.AttributeTypeDate, constants.AttributeTypeLocation, constants.AttributeTypeFuzzyString,
		constants.AttributeTypeUniqueID, constants.AttributeTypePrimitiveExact,
	}
	for _, attrType := range types {
		once := NormalizeForType("Sample Value 123", attrType)
		twice := NormalizeForType(once, attrType)
		if once != twice {
			t.Errorf("%s: normalization is not idempotent: %q -> %q", attrType, once, twice)
		}
	}
}

// TestNormalizeEmailReducesToTheMailbox covers the spellings that reach one inbox.
func TestNormalizeEmailReducesToTheMailbox(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"case and padding", "  J.Smith@Acme.COM ", "j.smith@acme.com"},
		{"sub-address tag", "j.smith+newsletter@acme.com", "j.smith@acme.com"},
		{"tag containing dots", "j.smith+a.b.c@acme.com", "j.smith@acme.com"},
		{"trailing root dot", "j.smith@acme.com.", "j.smith@acme.com"},
		{"gmail folds dots", "j.smith@gmail.com", "jsmith@gmail.com"},
		{"googlemail folds dots", "j.smith@googlemail.com", "jsmith@googlemail.com"},
		{"dots kept elsewhere", "j.smith@acme.com", "j.smith@acme.com"},
		{"leading plus is not a tag", "+promo@acme.com", "+promo@acme.com"},
		{"not an address at all", "not-an-email", "not-an-email"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeEmail(tt.in); got != tt.want {
				t.Errorf("NormalizeEmail(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestNormalizeEmailKeepsDistinctMailboxesApart guards the other direction: the rules above
// must not fold two addresses that genuinely reach different people.
func TestNormalizeEmailKeepsDistinctMailboxesApart(t *testing.T) {
	pairs := [][2]string{
		{"j.smith@acme.com", "jsmith@acme.com"},   // only Google ignores dots
		{"j.smith@acme.com", "j.smith@acme.co"},   // different domain
		{"j.smith@acme.com", "a.smith@acme.com"},  // different mailbox
		{"j.smith@gmail.com", "j.smith@acme.com"}, // same local, different provider
	}

	for _, p := range pairs {
		if NormalizeEmail(p[0]) == NormalizeEmail(p[1]) {
			t.Errorf("%q and %q are different mailboxes but normalized alike", p[0], p[1])
		}
	}
}
