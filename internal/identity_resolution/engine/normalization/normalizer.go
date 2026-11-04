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
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

var multiSpace = regexp.MustCompile(`\s+`)

// nameSeparators join two name parts rather than sit inside one. They become spaces so
// that "Mary-Jane" tokenizes the same way as "Mary Jane"; deleting them instead fuses the
// parts into one token and the two spellings stop matching.
var nameSeparators = map[rune]bool{'-': true, '–': true, '—': true, '/': true, '\\': true, '_': true, ',': true}

func NormalizeName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))

	// Other punctuation — apostrophes, periods — is dropped outright, because it sits
	// inside a name part: "O'Brien" and "OBrien" are one token either way.
	var cleaned []rune
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsSpace(r):
			cleaned = append(cleaned, r)
		case nameSeparators[r]:
			cleaned = append(cleaned, ' ')
		}
	}

	return strings.TrimSpace(multiSpace.ReplaceAllString(string(cleaned), " "))
}

func TokenSortName(name string) string {
	normalized := NormalizeName(name)
	tokens := strings.Fields(normalized)
	sort.Strings(tokens)
	return strings.Join(tokens, " ")
}

// NormalizeEmail reduces an address to the mailbox it actually reaches.
//
// Lowercasing and trimming are not enough: a sub-address tag, a trailing root dot on the
// domain, and (at Google only) dots in the local part are all discarded by the receiving mail
// server, so two addresses differing only in those reach one inbox and belong to one person.
//
// This feeds blocking as well as matching, which is deliberate — a pair the matcher calls
// identical has to share an exact blocking key, or the two profiles are never compared.
func NormalizeEmail(email string) string {
	trimmed := strings.TrimSpace(strings.ToLower(email))

	local, domain, ok := strings.Cut(trimmed, "@")
	if !ok {
		return trimmed
	}

	// A trailing dot denotes the DNS root and names the same domain.
	domain = strings.TrimSuffix(domain, ".")

	// RFC 5233 sub-addressing: everything from the first '+' is a tag for the recipient's
	// own filing, not part of the mailbox.
	if plus := strings.IndexByte(local, '+'); plus > 0 {
		local = local[:plus]
	}

	// Google ignores dots in the local part. No other provider promises this, so it stays
	// scoped to their domains rather than applied generally — elsewhere j.smith and jsmith
	// are two different mailboxes.
	if domain == "gmail.com" || domain == "googlemail.com" {
		local = strings.ReplaceAll(local, ".", "")
	}

	return local + "@" + domain
}

func NormalizePhone(phone string) string {
	var digits []rune
	for _, r := range phone {
		if unicode.IsDigit(r) {
			digits = append(digits, r)
		}
	}
	return string(digits)
}

// ("01/02/2006", "02/01/2006") are excluded because the same input
// (e.g. "03/04/2026") parses successfully under both US (Mar 4) and EU
// (Apr 3) interpretations
var dateFormats = []string{
	"2006-01-02",
	"2006/01/02",
	"Jan 2, 2006",
	"January 2, 2006",
	"02-Jan-2006",
	"2006-01-02T15:04:05Z07:00",
}

func NormalizeDate(date string) string {
	date = strings.TrimSpace(date)
	if date == "" {
		return ""
	}
	for _, format := range dateFormats {
		if t, err := time.Parse(format, date); err == nil {
			return t.Format("2006.01.02")
		}
	}
	return date
}

func NormalizeForType(value string, attrType string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	switch attrType {
	case constants.AttributeTypeName:
		return TokenSortName(value)
	case constants.AttributeTypeEmail:
		return NormalizeEmail(value)
	case constants.AttributeTypePhone:
		return NormalizePhone(value)
	case constants.AttributeTypeDate:
		return NormalizeDate(value)
	case constants.AttributeTypeLocation:
		return strings.TrimSpace(strings.ToLower(value))
	case constants.AttributeTypeFuzzyString:
		return strings.TrimSpace(strings.ToLower(value))
	case constants.AttributeTypeUniqueID:
		return strings.TrimSpace(strings.ToLower(value))
	case constants.AttributeTypePrimitiveExact:
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return strings.TrimSpace(strings.ToLower(value))
	}
}

// uninformativeValues are values that are syntactically fine but carry no identity.
// They arrive constantly from import jobs, default form values and optional fields that
// a UI filled in rather than left blank. Two profiles agreeing on one of these agree on
// nothing, so a rule that lands on one must report Unknown rather than a match — otherwise
// every profile carrying the placeholder collapses into every other one.
var uninformativeValues = map[string]bool{
	"n/a": true, "na": true, "none": true, "null": true, "nil": true, "-": true,
	"--": true, "unknown": true, "undefined": true, "not provided": true,
	"notprovided": true, "not set": true, "empty": true, "blank": true,
	"anonymous": true, "test": true, "string": true,
}

// uninformativeEmailLocalParts are role addresses — shared mailboxes rather than people.
var uninformativeEmailLocalParts = map[string]bool{
	"noreply": true, "no-reply": true, "donotreply": true, "do-not-reply": true,
	"info": true, "admin": true, "administrator": true, "support": true,
	"contact": true, "sales": true, "help": true, "test": true, "postmaster": true,
	"webmaster": true, "mailer-daemon": true,
}

// uninformativeEmailDomains are reserved or placeholder domains.
var uninformativeEmailDomains = map[string]bool{
	"example.com": true, "example.org": true, "example.net": true,
	"test.com": true, "localhost": true, "invalid": true, "email.com": true,
}

// uninformativeDates are sentinel dates that stand in for "no date".
var uninformativeDates = map[string]bool{
	"0001.01.01": true, "1900.01.01": true, "1901.01.01": true, "1970.01.01": true,
}

// IsUninformative reports whether a value is present but says nothing about who the person
// is. Callers must treat a true result as missing data, not as a value that failed to match.
func IsUninformative(value string, attrType string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return true
	}
	if uninformativeValues[trimmed] {
		return true
	}

	switch attrType {
	case constants.AttributeTypeEmail:
		local, domain, ok := strings.Cut(trimmed, "@")
		if !ok {
			return false
		}
		// Strip any +tag before judging the mailbox.
		if plus := strings.Index(local, "+"); plus > 0 {
			local = local[:plus]
		}
		return uninformativeEmailLocalParts[local] || uninformativeEmailDomains[domain]

	case constants.AttributeTypePhone:
		digits := NormalizePhone(trimmed)
		// Too short to identify anyone, or a single repeated digit such as 0000000000.
		if len(digits) < constants.PhoneSuffixBlockingLength {
			return true
		}
		allSame := true
		for i := 1; i < len(digits); i++ {
			if digits[i] != digits[0] {
				allSame = false
				break
			}
		}
		return allSame

	case constants.AttributeTypeName:
		// A single character cannot distinguish anyone.
		return len([]rune(NormalizeName(trimmed))) < 2

	case constants.AttributeTypeDate:
		return uninformativeDates[NormalizeDate(trimmed)]
	}

	return false
}
