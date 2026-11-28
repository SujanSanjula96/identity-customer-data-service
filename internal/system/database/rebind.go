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

package database

import (
	"fmt"
	"strconv"
	"strings"
)

// BindStatement prepares a statement and its arguments for dbType: it rewrites
// the placeholders and converts the argument values to the types that
// datasource's driver binds correctly.
func BindStatement(dbType, sqlText string, args []interface{}) (string, []interface{}, error) {
	text, bound, err := Rebind(dbType, sqlText, args)
	if err != nil {
		return "", nil, err
	}

	return text, NormalizeArgs(dbType, bound), nil
}

// Rebind rewrites the numbered placeholders ($1, $2, ...) of a statement into
// the style dbType understands, and returns the arguments to bind with it.
func Rebind(dbType, sqlText string, args []interface{}) (string, []interface{}, error) {
	switch dbType {
	case TypeMySQL:
		return rebindPositional(sqlText, args)
	case TypeMSSQL:
		return rebindNamed(sqlText, args)
	case TypeOracle:
		return rebindOrdinal(sqlText, args)
	default:
		return sqlText, args, nil
	}
}

func rebindPositional(sqlText string, args []interface{}) (string, []interface{}, error) {
	text, order, err := rewritePlaceholders(sqlText, func(int) string { return "?" })
	if err != nil {
		return "", nil, err
	}
	if err := checkPlaceholderUse(order, len(args)); err != nil {
		return "", nil, err
	}

	ordered := make([]interface{}, len(order))
	for i, number := range order {
		ordered[i] = args[number-1]
	}

	return text, ordered, nil
}

func rebindOrdinal(sqlText string, args []interface{}) (string, []interface{}, error) {
	sqlText = strings.TrimRight(sqlText, "; \t\r\n")

	position := 0
	text, order, err := rewritePlaceholders(sqlText, func(int) string {
		position++
		return ":" + strconv.Itoa(position)
	})
	if err != nil {
		return "", nil, err
	}
	if err := checkPlaceholderUse(order, len(args)); err != nil {
		return "", nil, err
	}

	ordered := make([]interface{}, len(order))
	for i, number := range order {
		ordered[i] = args[number-1]
	}

	return text, ordered, nil
}

func rebindNamed(sqlText string, args []interface{}) (string, []interface{}, error) {
	text, order, err := rewritePlaceholders(sqlText, func(number int) string {
		return "@p" + strconv.Itoa(number)
	})
	if err != nil {
		return "", nil, err
	}
	if err := checkPlaceholderUse(order, len(args)); err != nil {
		return "", nil, err
	}

	return text, args, nil
}

// checkPlaceholderUse verifies that every placeholder has an argument and every
// argument has a placeholder.
func checkPlaceholderUse(order []int, argCount int) error {
	used := make([]bool, argCount)
	for _, number := range order {
		if number < 1 || number > argCount {
			return fmt.Errorf("placeholder $%d has no argument (%d given)", number, argCount)
		}
		used[number-1] = true
	}
	for i, isUsed := range used {
		if !isUsed {
			return fmt.Errorf("argument $%d is not used by the statement", i+1)
		}
	}

	return nil
}

// rewritePlaceholders replaces each $n outside quotes and comments with the
// text replacement returns, and reports the numbers in order of appearance.
func rewritePlaceholders(sqlText string, replacement func(number int) string) (string, []int, error) {
	var out strings.Builder
	out.Grow(len(sqlText))

	var order []int
	for i := 0; i < len(sqlText); {
		c := sqlText[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			end := quotedEnd(sqlText, i)
			out.WriteString(sqlText[i:end])
			i = end
		case c == '-' && strings.HasPrefix(sqlText[i:], "--"):
			end := len(sqlText)
			if newline := strings.IndexByte(sqlText[i:], '\n'); newline >= 0 {
				end = i + newline
			}
			out.WriteString(sqlText[i:end])
			i = end
		case c == '/' && strings.HasPrefix(sqlText[i:], "/*"):
			end := len(sqlText)
			if closing := strings.Index(sqlText[i+2:], "*/"); closing >= 0 {
				end = i + 2 + closing + 2
			}
			out.WriteString(sqlText[i:end])
			i = end
		case c == '$' && i+1 < len(sqlText) && isDigit(sqlText[i+1]):
			end := i + 1
			for end < len(sqlText) && isDigit(sqlText[end]) {
				end++
			}
			number, err := strconv.Atoi(sqlText[i+1 : end])
			if err != nil {
				return "", nil, fmt.Errorf("invalid placeholder %q: %w", sqlText[i:end], err)
			}
			order = append(order, number)
			out.WriteString(replacement(number))
			i = end
		default:
			out.WriteByte(c)
			i++
		}
	}

	return out.String(), order, nil
}

// quotedEnd returns the index just past the quoted section that starts at start.
// A doubled quote character inside the section is an escaped quote, not the end.
// An unterminated section runs to the end of the text.
func quotedEnd(sqlText string, start int) int {
	quote := sqlText[start]
	for i := start + 1; i < len(sqlText); i++ {
		if sqlText[i] != quote {
			continue
		}
		if i+1 < len(sqlText) && sqlText[i+1] == quote {
			i++
			continue
		}
		return i + 1
	}

	return len(sqlText)
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// ReturnsRows reports whether a bound statement is read as a query.
func ReturnsRows(dbType, sqlText string) bool {
	if dbType != TypeOracle {
		return true
	}

	text := strings.ToUpper(strings.TrimLeft(sqlText, " \t\r\n("))
	return strings.HasPrefix(text, "SELECT") || strings.HasPrefix(text, "WITH")
}
