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

// Package client runs statements against the configured datasource and converts
// what the MySQL and MSSQL drivers return to the types the stores read.
package client

import (
	"strconv"
	"strings"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

const (
	columnTypeDate     = "DATE"
	columnTypeDatetime = "DATETIME"
)

func normalizesResults(dbType string) bool {
	switch dbType {
	case database.TypeSQLite, database.TypeMySQL, database.TypeMSSQL:
		return true
	default:
		return false
	}
}

func normalizeValue(dbType string, value interface{}, declaredType, column string) interface{} {
	switch dbType {
	case database.TypeSQLite:
		return normalizeSQLiteValue(value, declaredType)
	case database.TypeMySQL:
		return normalizeMySQLValue(value, declaredType, column)
	case database.TypeMSSQL:
		return normalizeMSSQLValue(value, declaredType, column)
	default:
		return value
	}
}

// booleanColumns names the schema's BOOLEAN columns, in lower case.
var booleanColumns = map[string]bool{
	"list_profile":   true,
	"delete_profile": true,
	"multi_valued":   true,
	"is_active":      true,
	"is_mandatory":   true,
	"consent_status": true,
}

var jsonColumns = map[string]bool{
	"traits":              true,
	"identity_attributes": true,
	"application_data":    true,
}

// boolFromValue reads a boolean from the forms a driver returns it in.
func boolFromValue(value interface{}) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case int64:
		return typed != 0, true
	case uint64:
		return typed != 0, true
	case float64:
		return typed != 0, true
	case []byte:
		return boolFromText(string(typed))
	case string:
		return boolFromText(typed)
	default:
		return false, false
	}
}

func boolFromText(text string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "1", "t", "true":
		return true, true
	case "0", "f", "false":
		return false, true
	default:
		return false, false
	}
}

// integerFromValue reads an integer a driver returned as text. Any other form is
// returned as it is.
func integerFromValue(value interface{}) interface{} {
	raw, ok := value.([]byte)
	if !ok {
		return value
	}
	if parsed, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
		return parsed
	}
	return value
}

// floatFromValue reads a floating-point number a driver returned as text. Any
// other form is returned as it is.
func floatFromValue(value interface{}) interface{} {
	raw, ok := value.([]byte)
	if !ok {
		return value
	}
	if parsed, err := strconv.ParseFloat(string(raw), 64); err == nil {
		return parsed
	}
	return value
}

// serverTimeLayouts are the forms a MySQL or MSSQL driver returns a timestamp
// in when it does not return a time.Time.
var serverTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	time.RFC3339Nano,
	"2006-01-02",
}

// timeFromValue returns a timestamp as a UTC time.Time. A value that is neither
// a time nor text in a known layout is returned as it is.
func timeFromValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC()
	case []byte:
		return parseServerTime(string(typed), value)
	case string:
		return parseServerTime(typed, value)
	default:
		return value
	}
}

func parseServerTime(text string, original interface{}) interface{} {
	trimmed := strings.TrimSpace(text)
	for _, layout := range serverTimeLayouts {
		if parsed, err := time.ParseInLocation(layout, trimmed, time.UTC); err == nil {
			return parsed.UTC()
		}
	}
	return original
}

// textFromValue returns a driver's byte slice as a string, and any other form
// as it is.
func textFromValue(value interface{}) interface{} {
	if raw, ok := value.([]byte); ok {
		return string(raw)
	}
	return value
}
