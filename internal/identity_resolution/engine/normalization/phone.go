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

import "strings"

// PhoneMinNationalLength is the shortest run of digits treated as a subscriber number. Below
// this a value is an extension or a short code, and two of them coinciding says nothing about
// whether they belong to the same person.
const PhoneMinNationalLength = 7

// callingCodes holds the country calling codes assigned under ITU-T E.164.
//
// It answers one question: how many leading digits name the country. It deliberately says
// nothing about whether the digits after them form a valid subscriber number there — that needs
// per-country numbering plans, which is the job of a full phone number library. Knowing only
// where the country ends is enough to stop two different subscribers being read as one.
var callingCodes = map[string]bool{
	"1": true, "7": true,

	"20": true, "27": true, "30": true, "31": true, "32": true, "33": true, "34": true,
	"36": true, "39": true, "40": true, "41": true, "43": true, "44": true, "45": true,
	"46": true, "47": true, "48": true, "49": true, "51": true, "52": true, "53": true,
	"54": true, "55": true, "56": true, "57": true, "58": true, "60": true, "61": true,
	"62": true, "63": true, "64": true, "65": true, "66": true, "81": true, "82": true,
	"84": true, "86": true, "90": true, "91": true, "92": true, "93": true, "94": true,
	"95": true, "98": true,

	"211": true, "212": true, "213": true, "216": true, "218": true, "220": true, "221": true,
	"222": true, "223": true, "224": true, "225": true, "226": true, "227": true, "228": true,
	"229": true, "230": true, "231": true, "232": true, "233": true, "234": true, "235": true,
	"236": true, "237": true, "238": true, "239": true, "240": true, "241": true, "242": true,
	"243": true, "244": true, "245": true, "246": true, "248": true, "249": true, "250": true,
	"251": true, "252": true, "253": true, "254": true, "255": true, "256": true, "257": true,
	"258": true, "260": true, "261": true, "262": true, "263": true, "264": true, "265": true,
	"266": true, "267": true, "268": true, "269": true, "290": true, "291": true, "297": true,
	"298": true, "299": true, "350": true, "351": true, "352": true, "353": true, "354": true,
	"355": true, "356": true, "357": true, "358": true, "359": true, "370": true, "371": true,
	"372": true, "373": true, "374": true, "375": true, "376": true, "377": true, "378": true,
	"380": true, "381": true, "382": true, "383": true, "385": true, "386": true, "387": true,
	"389": true, "420": true, "421": true, "423": true, "500": true, "501": true, "502": true,
	"503": true, "504": true, "505": true, "506": true, "507": true, "508": true, "509": true,
	"590": true, "591": true, "592": true, "593": true, "594": true, "595": true, "596": true,
	"597": true, "598": true, "599": true, "670": true, "672": true, "673": true, "674": true,
	"675": true, "676": true, "677": true, "678": true, "679": true, "680": true, "681": true,
	"682": true, "683": true, "685": true, "686": true, "687": true, "688": true, "689": true,
	"690": true, "691": true, "692": true, "850": true, "852": true, "853": true, "855": true,
	"856": true, "880": true, "886": true, "960": true, "961": true, "962": true, "963": true,
	"964": true, "965": true, "966": true, "967": true, "968": true, "970": true, "971": true,
	"972": true, "973": true, "974": true, "975": true, "976": true, "977": true, "992": true,
	"993": true, "994": true, "995": true, "996": true, "998": true,
}

// PhoneReading is one way a written number can be understood: the country it names, and the
// national significant number within it. CountryCode is empty when the number was written
// nationally, which is an absence rather than a guess — an unknown country contradicts nothing.
type PhoneReading struct {
	CountryCode string
	National    string
}

// PhoneReadings enumerates the readings of a written phone number.
//
// The same subscriber is written several ways: with the country code or without it, with a
// national trunk "0" before the operator code, with "00" standing in for "+", and with the
// trunk zero parenthesised after the country code. Each of those is produced here, so two
// spellings of one number meet on a common reading.
func PhoneReadings(raw string) []PhoneReading {
	digits := NormalizePhone(raw)
	if digits == "" {
		return nil
	}

	seen := map[PhoneReading]bool{}
	var out []PhoneReading

	add := func(r PhoneReading) {
		if len(r.National) >= PhoneMinNationalLength && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}

	bases := []string{digits}
	if strings.HasPrefix(digits, "00") && len(digits) > 2 {
		// "00" is the international prefix in most of the world, equivalent to "+".
		bases = append(bases, digits[2:])
	}

	for _, base := range bases {
		// Read as international. The longest assigned code wins: "1" is a prefix of "12x"
		// only by accident, and taking the shortest match would split Kazakhstan as Russia.
		for length := 3; length >= 1; length-- {
			if len(base) > length && callingCodes[base[:length]] {
				add(PhoneReading{
					CountryCode: base[:length],
					National:    strings.TrimPrefix(base[length:], "0"),
				})
			}
		}
		// Read as national, with or without the trunk prefix.
		add(PhoneReading{National: strings.TrimPrefix(base, "0")})
	}

	return out
}

// SamePhoneNumber reports whether two written numbers name the same subscriber.
//
// The national significant numbers must be identical and the countries must not contradict
// each other. Comparing only a fixed-length tail cannot do this: the operator or area code
// sits just outside such a window, and that is precisely the part that separates two people.
func SamePhoneNumber(a, b string) bool {
	readingsA, readingsB := PhoneReadings(a), PhoneReadings(b)

	for _, ra := range readingsA {
		for _, rb := range readingsB {
			if ra.National != rb.National {
				continue
			}
			if ra.CountryCode == "" || rb.CountryCode == "" || ra.CountryCode == rb.CountryCode {
				return true
			}
		}
	}

	return false
}
