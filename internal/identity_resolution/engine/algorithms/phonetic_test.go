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
package algorithms

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

func TestDoubleMetaphoneKnownNames(t *testing.T) {
	tests := []struct {
		word, primary, alternate string
	}{
		{"Smith", "SM0", "XMT"},
		{"Schmidt", "XMT", "SMT"},
		{"Knight", "NT", "NT"},
		{"Wright", "RT", "RT"},
		{"Gnome", "NM", "NM"},
		{"Gnu", "N", "N"},
		{"Michael", "MKL", "MXL"},
		{"Xavier", "SF", "SFR"},
		{"Jose", "JS", "HS"},
		{"Czerny", "SRN", "XRN"},
		{"Phillip", "FLP", "FLP"},
		{"Catherine", "K0RN", "KTRN"},
		{"Katherine", "K0RN", "KTRN"},
		{"Stephen", "STFN", "STFN"},
		{"Steven", "STFN", "STFN"},
		{"Johnson", "JNSN", "ANSN"},
		{"Jonson", "JNSN", "ANSN"},
		{"Edge", "AJ", "AJ"},
		{"Judge", "JJ", "AJ"},
		{"Laugh", "LF", "LF"},
		{"Tough", "TF", "TF"},
		{"Cough", "KF", "KF"},
		{"Rough", "RF", "RF"},
		{"Dumb", "TM", "TM"},
		{"Thomas", "TMS", "TMS"},
		{"Shaun", "XN", "XN"},
		{"Sean", "SN", "SN"},
		{"Bach", "PK", "PK"},
		{"Bacchus", "PKS", "PKS"},
		{"Accident", "AKST", "AKST"},
		{"Succeed", "SKST", "SKST"},
		{"Wasserman", "ASRM", "FSRM"},
		{"Vasserman", "FSRM", "FSRM"},
		{"Filipowicz", "FLPT", "FLPF"},
		{"Gerald", "KRLT", "JRLT"},
		{"Girard", "JRRT", "KRRT"},
		{"Pizza", "PS", "PTS"},
		{"Island", "ALNT", "ALNT"},
		{"Zhang", "JNK", "JNK"},
		{"Cheng", "XNK", "XNK"},
		{"Chemistry", "KMST", "KMST"},
		{"Tchaikovsky", "XKFS", "XKFS"},
		{"Guinness", "KNS", "KNS"},
		{"Rogier", "RJ", "RKR"},
		{"Ghost", "KST", "KST"},
		{"Hugh", "H", "H"},
		{"Caesar", "SSR", "SSR"},
		{"Mozart", "MSRT", "MSRT"},
	}
	for _, tc := range tests {
		t.Run(tc.word, func(t *testing.T) {
			primary, alternate := DoubleMetaphone(tc.word)

			assert.Equal(t, tc.primary, primary)
			assert.Equal(t, tc.alternate, alternate)
		})
	}
}

func TestDoubleMetaphoneIgnoresCaseAndNonLetters(t *testing.T) {
	primary, alternate := DoubleMetaphone("o'BRIEN-smith 42")
	expectedPrimary, expectedAlternate := DoubleMetaphone("OBRIENSMITH")

	assert.Equal(t, expectedPrimary, primary)
	assert.Equal(t, expectedAlternate, alternate)

	for _, input := range []string{"", "   ", "1234", "-'.-"} {
		primary, alternate = DoubleMetaphone(input)
		assert.Empty(t, primary, input)
		assert.Empty(t, alternate, input)
	}
}

func TestDoubleMetaphoneCodesAreShort(t *testing.T) {
	primary, alternate := DoubleMetaphone("Wolfeschlegelsteinhausenbergerdorff")

	assert.LessOrEqual(t, len(primary), constants.MaxMetaphoneLen)
	assert.LessOrEqual(t, len(alternate), constants.MaxMetaphoneLen)
	assert.NotEmpty(t, primary)
}

// Every word below takes a different path through the rules. The codes are letters only and
// never longer than the limit.
func TestDoubleMetaphoneHandlesTheDifficultSpellings(t *testing.T) {
	words := []string{
		"Aguilar", "Bajador", "Cabrillo", "Caucasus", "Chianti", "Chorus", "Cia", "Ciao", "Cheese", "Church",
		"McHugh", "Orchestra", "Charisma", "Gianni", "Gyro", "Gallegos", "Ghiradelli", "Ghoul", "Hochmeier",
		"Jankelowicz",
		"Jalapeno", "Mcclellan", "Mcmillan", "Pfizer", "Phone", "Psalm", "Quentin", "Rhythm", "Szabo", "Schneider",
		"Schenker", "Schermerhorn", "Sciences", "Scream", "Sugar", "Shoemaker", "Sholz", "Sian", "Thames", "Tchad",
		"Vanderbilt", "Weiss", "Wiesbaden", "Wojciechowski", "Xiao", "Yvonne", "Zhao", "Zaragoza", "Ziegler", "Sanchez",
		"Breaux", "Dobbs", "Dumbledore", "Edgar", "Evgeny", "Felipe", "Gnash", "Hodge", "Knave", "Lleyton",
		"Margulis", "Nguyen", "O'Neill", "Peabody", "Quigley", "Roberts", "Sczepanski", "Tchoupitoulas", "Uhlmann",
		"Wagner",
		"Bouquet", "Cuisine", "Giovanni", "Mancini", "Vecchio", "Chiara", "Cesar", "Bologna", "Gheorghe", "Hajime",
	}
	for _, word := range words {
		primary, alternate := DoubleMetaphone(word)

		assert.NotEmpty(t, primary, word)
		assert.LessOrEqual(t, len(primary), constants.MaxMetaphoneLen, word)
		assert.LessOrEqual(t, len(alternate), constants.MaxMetaphoneLen, word)
		assert.Equal(t, strings.ToUpper(primary), primary, word)
		assert.Equal(t, strings.ToUpper(alternate), alternate, word)
	}
}

func TestDoubleMetaphonePhraseIsInsensitiveToWordOrder(t *testing.T) {
	forward, forwardAlt := DoubleMetaphonePhrase("John Smith")
	reversed, reversedAlt := DoubleMetaphonePhrase("Smith John")

	assert.Equal(t, forward, reversed)
	assert.Equal(t, forwardAlt, reversedAlt)
	assert.Equal(t, 2, len(strings.Fields(forward)), "one code for each word")

	empty, emptyAlt := DoubleMetaphonePhrase("   ")
	assert.Empty(t, empty)
	assert.Empty(t, emptyAlt)
}

func TestPhoneticSimilarity(t *testing.T) {
	tests := []struct {
		name  string
		a, b  string
		score float64
	}{
		{"the same spelling", "Smith", "Smith", 1.0},
		{"a different spelling with the same sound", "Catherine", "Katherine", 1.0},
		{"names in another order", "John Smith", "Smith John", 1.0},
		{"the alternate code of one is the primary of the other", "Smith", "Schmidt", constants.PhoneticAlternateScore},
		{"names that sound different", "Smith", "Jones", 0.0},
		{"an empty name", "", "Smith", 0.0},
		{"two empty names", "", "", 0.0},
		{"only punctuation", "--", "Smith", 0.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.InDelta(t, tc.score, PhoneticSimilarity(tc.a, tc.b), 1e-9)
		})
	}
}
