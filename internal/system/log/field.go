/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
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

package log

import "strings"

// Standard field names. All components use these names, so that a field has one name in every
// log stream. A new field name is in snake_case.
const (
	// KeyLogType names the log stream of a record: app, access, or audit.
	KeyLogType = "log_type"
	// KeyComponent names the component that wrote the record.
	KeyComponent = "component"
	// KeyCorrelationID is the correlation ID of the request.
	KeyCorrelationID = "correlation_id"
	// KeyOrg is the handle of the organization of the request.
	KeyOrg = "org"
	// KeyError is the error that the record reports.
	KeyError = "error"
	// KeyProfileID is the ID of a profile.
	KeyProfileID = "profile_id"
	// KeyUserID is the ID of a user.
	KeyUserID = "user_id"
)

// Field represents a key-value pair for structured logging.
type Field struct {
	Key   string
	Value interface{}
}

// String creates a Field with a string value.
func String(key, value string) Field {

	return Field{Key: key, Value: value}
}

// Int creates a Field with an integer value.
func Int(key string, value int) Field {

	return Field{Key: key, Value: value}
}

// Bool creates a Field with a boolean value.
func Bool(key string, value bool) Field {

	return Field{Key: key, Value: value}
}

// Any creates a Field with any value.
func Any(key string, value interface{}) Field {

	return Field{Key: key, Value: value}
}

// Error creates a Field with an error value.
func Error(value error) Field {

	return Field{Key: KeyError, Value: value}
}

// MaskedString creates a Field with a masked value. Use it for personal data, such as an email
// address, when the record must show that a value was present.
func MaskedString(key, value string) Field {

	return Field{Key: key, Value: Mask(value)}
}

// Mask hides all characters of value except the first and the last. It hides a short value
// completely.
func Mask(value string) string {

	runes := []rune(value)
	if len(runes) <= 3 {
		return strings.Repeat("*", len(runes))
	}
	return string(runes[0]) + strings.Repeat("*", len(runes)-2) + string(runes[len(runes)-1])
}
