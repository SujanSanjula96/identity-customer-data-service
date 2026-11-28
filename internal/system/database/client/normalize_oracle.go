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
	"strconv"
	"strings"
)

// emptyStringColumns names the columns the other datasources hold an empty
// string in, in lower case. Oracle stores an empty string as NULL, and the
// stores read these columns as text.
var emptyStringColumns = map[string]bool{
	"application_identifier": true,
	"attribute_type":         true,
	"display_name":           true,
	"purpose":                true,
	"reference_profile_id":   true,
	"reference_reason":       true,
	"scope":                  true,
	"unification_method":     true,
	"user_id":                true,
	"value":                  true,
}

// columnTypeNumber is the declared type the driver reports for every numeric
// column, whatever its precision.
const columnTypeNumber = "NUMBER"

// normalizeOracleValue converts a value scanned from Oracle to the type the
// stores read.
func normalizeOracleValue(value interface{}, declaredType, column string) interface{} {
	name := strings.ToLower(column)

	if value == nil {
		if emptyStringColumns[name] {
			return ""
		}
		return nil
	}

	if booleanColumns[name] {
		if boolean, ok := boolFromValue(value); ok {
			return boolean
		}
		return value
	}

	if jsonColumns[name] {
		if text, ok := value.(string); ok {
			return []byte(text)
		}
		return value
	}

	if strings.EqualFold(strings.TrimSpace(declaredType), columnTypeNumber) {
		return numberFromValue(value)
	}

	switch value.(type) {
	case []byte:
		return textFromValue(value)
	default:
		return timeFromValue(value)
	}
}

// numberFromValue reads a number the driver returned as text: an int64 when it
// is whole and a float64 otherwise.
func numberFromValue(value interface{}) interface{} {
	text, ok := value.(string)
	if !ok {
		return value
	}
	if parsed, err := strconv.ParseInt(text, 10, 64); err == nil {
		return parsed
	}
	if parsed, err := strconv.ParseFloat(text, 64); err == nil {
		return parsed
	}
	return value
}
