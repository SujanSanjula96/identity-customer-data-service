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
	"sort"
	"strings"
	"unicode"

	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/engine/normalization"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

const maxMetaphoneLen = constants.MaxMetaphoneLen

func DoubleMetaphone(s string) (primary, alternate string) {
	// Normalize: uppercase, strip non-alpha
	var letters []byte
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters = append(letters, byte(unicode.ToUpper(r)))
		}
	}
	if len(letters) == 0 {
		return "", ""
	}

	word := string(letters)
	length := len(word)
	last := length - 1

	var pri, alt strings.Builder
	pri.Grow(maxMetaphoneLen)
	alt.Grow(maxMetaphoneLen)

	current := 0

	charAt := func(pos int) byte {
		if pos < 0 || pos >= length {
			return 0
		}
		return word[pos]
	}

	stringAt := func(pos, ln int, strs ...string) bool {
		if pos < 0 || pos+ln > length {
			return false
		}
		sub := word[pos : pos+ln]
		for _, s := range strs {
			if sub == s {
				return true
			}
		}
		return false
	}

	isVowel := func(pos int) bool {
		c := charAt(pos)
		return c == 'A' || c == 'E' || c == 'I' || c == 'O' || c == 'U'
	}

	slavoGermanic := strings.ContainsAny(word, "WK") ||
		strings.Contains(word, "CZ") ||
		strings.Contains(word, "WITZ")

	addPri := func(s string) {
		if pri.Len() < maxMetaphoneLen {
			pri.WriteString(s)
		}
	}
	addAlt := func(s string) {
		if alt.Len() < maxMetaphoneLen {
			alt.WriteString(s)
		}
	}
	addBoth := func(s string) {
		addPri(s)
		addAlt(s)
	}

	// Skip silent initial letters
	if stringAt(0, 2, "GN", "KN", "PN", "AE", "WR") {
		current++
	}

	if charAt(0) == 'X' {
		addBoth("S")
		current++
	}

	for pri.Len() < maxMetaphoneLen || alt.Len() < maxMetaphoneLen {
		if current >= length {
			break
		}

		c := charAt(current)

		switch c {
		case 'A', 'E', 'I', 'O', 'U':
			if current == 0 {
				addBoth("A")
			}
			current++

		case 'B':
			addBoth("P")
			if charAt(current+1) == 'B' {
				current += 2
			} else {
				current++
			}

		case 'C':
			if current > 1 && !isVowel(current-2) && stringAt(current-1, 3, "ACH") &&
				charAt(current+2) != 'I' && (charAt(current+2) != 'E' || stringAt(current-2, 6, "BACHER", "MACHER")) {
				addBoth("K")
				current += 2
			} else if current == 0 && stringAt(current, 6, "CAESAR") {
				addBoth("S")
				current += 2
			} else if stringAt(current, 4, "CHIA") {
				addBoth("K")
				current += 2
			} else if stringAt(current, 2, "CH") {
				if current > 0 && stringAt(current, 4, "CHAE") {
					addPri("K")
					addAlt("X")
					current += 2
				} else if current == 0 && (stringAt(current+1, 5, "HARAC", "HARIS") ||
					stringAt(current+1, 3, "HOR", "HYM", "HIA", "HEM")) &&
					!stringAt(0, 5, "CHORE") {
					addBoth("K")
					current += 2
				} else if stringAt(0, 4, "VAN ", "VON ") || stringAt(0, 3, "SCH") ||
					stringAt(current-2, 6, "ORCHES", "ARCHIT", "ORCHID") ||
					stringAt(current+2, 1, "T", "S") ||
					((current == 0 || stringAt(current-1, 1, "A", "O", "U", "E")) &&
						stringAt(current+2, 1, "L", "R", "N", "M", "B", "H", "F", "V", "W", " ")) {
					addBoth("K")
					current += 2
				} else if current > 0 {
					if stringAt(0, 2, "MC") {
						addBoth("K")
					} else {
						addPri("X")
						addAlt("K")
					}
					current += 2
				} else {
					addBoth("X")
					current += 2
				}
			} else if stringAt(current, 2, "CZ") && !stringAt(current-2, 4, "WICZ") {
				addPri("S")
				addAlt("X")
				current += 2
			} else if stringAt(current+1, 3, "CIA") {
				addBoth("X")
				current += 3
			} else if stringAt(current, 2, "CC") && !(current == 1 && charAt(0) == 'M') {
				if stringAt(current+2, 1, "I", "E", "H") && !stringAt(current+2, 2, "HU") {
					if (current == 1 && charAt(0) == 'A') || stringAt(current-1, 5, "UCCEE", "UCCES") {
						addBoth("KS")
					} else {
						addBoth("X")
					}
					current += 3
				} else {
					addBoth("K")
					current += 2
				}
			} else if stringAt(current, 2, "CK", "CG", "CQ") {
				addBoth("K")
				current += 2
			} else if stringAt(current, 2, "CI", "CE", "CY") {
				if stringAt(current, 3, "CIO", "CIE", "CIA") {
					addBoth("S")
				} else {
					addBoth("S")
				}
				current += 2
			} else {
				addBoth("K")
				if stringAt(current+1, 2, " C", " Q", " G") {
					current += 3
				} else if stringAt(current+1, 1, "C", "K", "Q") && !stringAt(current+1, 2, "CE", "CI") {
					current += 2
				} else {
					current++
				}
			}

		case 'D':
			if stringAt(current, 2, "DG") {
				if stringAt(current+2, 1, "I", "E", "Y") {
					addBoth("J")
					current += 3
				} else {
					addBoth("TK")
					current += 2
				}
			} else if stringAt(current, 2, "DT", "DD") {
				addBoth("T")
				current += 2
			} else {
				addBoth("T")
				current++
			}

		case 'F':
			if charAt(current+1) == 'F' {
				current += 2
			} else {
				current++
			}
			addBoth("F")

		case 'G':
			if charAt(current+1) == 'H' {
				if current > 0 && !isVowel(current-1) {
					addBoth("K")
					current += 2
				} else if current == 0 {
					if charAt(current+2) == 'I' {
						addBoth("J")
					} else {
						addBoth("K")
					}
					current += 2
				} else if (current > 1 && stringAt(current-2, 1, "B", "H", "D")) ||
					(current > 2 && stringAt(current-3, 1, "B", "H", "D")) ||
					(current > 3 && stringAt(current-4, 1, "B", "H")) {
					current += 2
				} else {
					if current > 2 && charAt(current-1) == 'U' &&
						stringAt(current-3, 1, "C", "G", "L", "R", "T") {
						addBoth("F")
					} else if current > 0 && charAt(current-1) != 'I' {
						addBoth("K")
					}
					current += 2
				}
			} else if charAt(current+1) == 'N' {
				if current == 1 && isVowel(0) && !slavoGermanic {
					addPri("KN")
					addAlt("N")
				} else {
					if !stringAt(current+2, 2, "EY") && charAt(current+1) != 'Y' && !slavoGermanic {
						addPri("N")
						addAlt("KN")
					} else {
						addBoth("KN")
					}
				}
				current += 2
			} else if stringAt(current+1, 2, "LI") && !slavoGermanic {
				addPri("KL")
				addAlt("L")
				current += 2
			} else if current == 0 && (charAt(current+1) == 'Y' ||
				stringAt(current+1, 2, "ES", "EP", "EB", "EL", "EY", "IB", "IL", "IN", "IE", "EI", "ER")) {
				addPri("K")
				addAlt("J")
				current += 2
			} else if (stringAt(current+1, 2, "ER") || charAt(current+1) == 'Y') &&
				!stringAt(0, 6, "DANGER", "RANGER", "MANGER") &&
				!stringAt(current-1, 1, "E", "I") &&
				!stringAt(current-1, 3, "RGY", "OGY") {
				addPri("K")
				addAlt("J")
				current += 2
			} else if stringAt(current+1, 1, "E", "I", "Y") ||
				stringAt(current-1, 4, "AGGI", "OGGI") {
				if stringAt(0, 4, "VAN ", "VON ") || stringAt(0, 3, "SCH") ||
					stringAt(current+1, 2, "ET") {
					addBoth("K")
				} else {
					if stringAt(current+1, 4, "IER ") {
						addBoth("J")
					} else {
						addPri("J")
						addAlt("K")
					}
				}
				current += 2
			} else {
				if charAt(current+1) == 'G' {
					current += 2
				} else {
					current++
				}
				addBoth("K")
			}

		case 'H':
			if (current == 0 || isVowel(current-1)) && isVowel(current+1) {
				addBoth("H")
				current += 2
			} else {
				current++
			}

		case 'J':
			if stringAt(current, 4, "JOSE") || stringAt(0, 4, "SAN ") {
				if (current == 0 && charAt(current+4) == ' ') || stringAt(0, 4, "SAN ") {
					addBoth("H")
				} else {
					addPri("J")
					addAlt("H")
				}
				current++
			} else {
				if current == 0 && !stringAt(current, 4, "JOSE") {
					addPri("J")
					addAlt("A")
				} else if isVowel(current-1) && !slavoGermanic && (charAt(current+1) == 'A' || charAt(current+1) == 'O') {
					addPri("J")
					addAlt("H")
				} else if current == last {
					addPri("J")
				} else if !stringAt(current+1, 1, "L", "T", "K", "S", "N", "M", "B", "H", "F", "G", "D") &&
					!stringAt(current-1, 1, "S", "K", "L") {
					addBoth("J")
				}
				if charAt(current+1) == 'J' {
					current += 2
				} else {
					current++
				}
			}

		case 'K':
			if charAt(current+1) == 'K' {
				current += 2
			} else {
				current++
			}
			addBoth("K")

		case 'L':
			if charAt(current+1) == 'L' {
				if (current == length-3 && stringAt(current-1, 4, "ILLO", "ILLA", "ALLE")) ||
					((stringAt(last-1, 2, "AS", "OS") || stringAt(last, 1, "A", "O")) &&
						stringAt(current-1, 4, "ALLE")) {
					addPri("L")
					current += 2
				} else {
					addBoth("L")
					current += 2
				}
			} else {
				current++
				addBoth("L")
			}

		case 'M':
			if (stringAt(current-1, 3, "UMB") && (current+1 == last || stringAt(current+2, 2, "ER"))) ||
				charAt(current+1) == 'M' {
				current += 2
			} else {
				current++
			}
			addBoth("M")

		case 'N':
			if charAt(current+1) == 'N' {
				current += 2
			} else {
				current++
			}
			addBoth("N")

		case 'P':
			if charAt(current+1) == 'H' {
				addBoth("F")
				current += 2
			} else {
				if stringAt(current+1, 1, "P", "B") {
					current += 2
				} else {
					current++
				}
				addBoth("P")
			}

		case 'Q':
			if charAt(current+1) == 'Q' {
				current += 2
			} else {
				current++
			}
			addBoth("K")

		case 'R':
			if current == last && !slavoGermanic &&
				stringAt(current-2, 2, "IE") && !stringAt(current-4, 2, "ME", "MA") {
				addAlt("R")
			} else {
				addBoth("R")
			}
			if charAt(current+1) == 'R' {
				current += 2
			} else {
				current++
			}

		case 'S':
			if stringAt(current-1, 3, "ISL", "YSL") {
				current++
			} else if current == 0 && stringAt(current, 5, "SUGAR") {
				addPri("X")
				addAlt("S")
				current++
			} else if stringAt(current, 2, "SH") {
				if stringAt(current+1, 4, "HEIM", "HOEK", "HOLM", "HOLZ") {
					addBoth("S")
				} else {
					addBoth("X")
				}
				current += 2
			} else if stringAt(current, 3, "SIO", "SIA") || stringAt(current, 4, "SIAN") {
				if !slavoGermanic {
					addPri("S")
					addAlt("X")
				} else {
					addBoth("S")
				}
				current += 3
			} else if (current == 0 && stringAt(current+1, 1, "M", "N", "L", "W")) || stringAt(current+1, 1, "Z") {
				addPri("S")
				addAlt("X")
				if stringAt(current+1, 1, "Z") {
					current += 2
				} else {
					current++
				}
			} else if stringAt(current, 2, "SC") {
				if charAt(current+2) == 'H' {
					if stringAt(current+3, 2, "OO", "ER", "EN", "UY", "ED", "EM") {
						if stringAt(current+3, 2, "ER", "EN") {
							addPri("X")
							addAlt("SK")
						} else {
							addBoth("SK")
						}
					} else {
						if current == 0 && !isVowel(3) && charAt(3) != 'W' {
							addPri("X")
							addAlt("S")
						} else {
							addBoth("X")
						}
					}
					current += 3
				} else if stringAt(current+2, 1, "I", "E", "Y") {
					addBoth("S")
					current += 3
				} else {
					addBoth("SK")
					current += 3
				}
			} else {
				if current == last && stringAt(current-2, 2, "AI", "OI") {
					addAlt("S")
				} else {
					addBoth("S")
				}
				if stringAt(current+1, 1, "S", "Z") {
					current += 2
				} else {
					current++
				}
			}

		case 'T':
			if stringAt(current, 4, "TION") {
				addBoth("XN")
				current += 3
			} else if stringAt(current, 3, "TIA", "TCH") {
				addBoth("X")
				current += 3
			} else if stringAt(current, 2, "TH") || stringAt(current, 3, "TTH") {
				if stringAt(current+2, 2, "OM", "AM") || stringAt(0, 4, "VAN ", "VON ") || stringAt(0, 3, "SCH") {
					addBoth("T")
				} else {
					addPri("0")
					addAlt("T")
				}
				current += 2
			} else {
				if stringAt(current+1, 1, "T", "D") {
					current += 2
				} else {
					current++
				}
				addBoth("T")
			}

		case 'V':
			if charAt(current+1) == 'V' {
				current += 2
			} else {
				current++
			}
			addBoth("F")

		case 'W':
			if stringAt(current, 2, "WR") {
				addBoth("R")
				current += 2
			} else if current == 0 && (isVowel(current+1) || stringAt(current, 2, "WH")) {
				if isVowel(current + 1) {
					addPri("A")
					addAlt("F")
				} else {
					addBoth("A")
				}
				current++
			} else {
				if (current == last && isVowel(current-1)) ||
					stringAt(current-1, 5, "EWSKI", "EWSKY", "OWSKI", "OWSKY") ||
					stringAt(0, 3, "SCH") {
					addAlt("F")
					current++
				} else if stringAt(current, 4, "WICZ", "WITZ") {
					addPri("TS")
					addAlt("FX")
					current += 4
				} else {
					current++
				}
			}

		case 'X':
			if !(current == last && (stringAt(current-3, 3, "IAU", "EAU") || stringAt(current-2, 2, "AU", "OU"))) {
				addBoth("KS")
			}
			if stringAt(current+1, 1, "C", "X") {
				current += 2
			} else {
				current++
			}

		case 'Z':
			if charAt(current+1) == 'H' {
				addBoth("J")
				current += 2
			} else if stringAt(current+1, 2, "ZO", "ZI", "ZA") ||
				(slavoGermanic && current > 0 && charAt(current-1) != 'T') {
				addPri("S")
				addAlt("TS")
				current++
			} else {
				addBoth("S")
				current++
			}
			if charAt(current) == 'Z' {
				current++
			}

		default:
			current++
		}
	}

	primary = pri.String()
	alternate = alt.String()
	if len(primary) > maxMetaphoneLen {
		primary = primary[:maxMetaphoneLen]
	}
	if len(alternate) > maxMetaphoneLen {
		alternate = alternate[:maxMetaphoneLen]
	}

	return primary, alternate
}

// DoubleMetaphonePhrase computes Double Metaphone codes for each word in a phrase, sorts them, and joins then producing two keys.
func DoubleMetaphonePhrase(phrase string) (primary, alternate string) {
	norm := normalization.NormalizeName(phrase)
	tokens := strings.Fields(norm)
	if len(tokens) == 0 {
		return "", ""
	}

	priCodes := make([]string, 0, len(tokens))
	altCodes := make([]string, 0, len(tokens))

	for _, token := range tokens {
		pri, alt := DoubleMetaphone(token)
		if pri != "" {
			priCodes = append(priCodes, pri)
		}
		if alt != "" {
			altCodes = append(altCodes, alt)
		}
	}

	sort.Strings(priCodes)
	sort.Strings(altCodes)

	return strings.Join(priCodes, " "), strings.Join(altCodes, " ")
}

// PhoneticSimilarity computes how similar two names are phonetically using Double Metaphone.
func PhoneticSimilarity(name1, name2 string) float64 {
	pri1, alt1 := DoubleMetaphonePhrase(name1)
	pri2, alt2 := DoubleMetaphonePhrase(name2)

	if pri1 == "" || pri2 == "" {
		return 0.0
	}

	if pri1 == pri2 {
		return 1.0
	}

	if pri1 == alt2 || alt1 == pri2 || (alt1 != "" && alt2 != "" && alt1 == alt2) {
		return constants.PhoneticAlternateScore
	}

	return 0.0
}
