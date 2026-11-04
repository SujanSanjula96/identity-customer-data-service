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

package client

import (
	"strings"
	"time"
)

// The stores read query results with direct type assertions, so a column's Go
// type is part of the contract. The helpers below coerce SQLite results to the
// types lib/pq returns:
//
//	column type   lib/pq      modernc.org/sqlite
//	-----------   ---------   ------------------
//	JSONB         []byte      string
//	BOOLEAN       bool        int64 (0/1)
//	TIMESTAMP     time.Time   time.Time or text
//
// Coercion is keyed on the column's declared type, so a new column of an
// existing type needs no change here. Every coercion is idempotent and nil-safe.

// sqliteTimeLayouts are the timestamp formats the inbuilt database holds, in
// the order they are tried.
var sqliteTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999 -0700 MST",
	time.RFC3339Nano,
}

// normalizeSQLiteValue coerces a single scanned value based on its declared
// column type.
func normalizeSQLiteValue(value interface{}, declaredType string) interface{} {

	if value == nil {
		return nil
	}

	switch sqliteColumnClass(declaredType) {
	case columnClassJSON:
		// The stores unmarshal JSON columns from []byte.
		if text, ok := value.(string); ok {
			return []byte(text)
		}
	case columnClassBool:
		// SQLite has no boolean storage class; booleans arrive as 0/1.
		switch typed := value.(type) {
		case int64:
			return typed != 0
		case float64:
			return typed != 0
		case string:
			return parseSQLiteBool(typed)
		}
	case columnClassTime:
		// TIMESTAMP columns already arrive as time.Time. Other date
		// declarations can arrive as text.
		switch typed := value.(type) {
		case string:
			if parsed, ok := parseSQLiteTime(typed); ok {
				return parsed
			}
		case []byte:
			if parsed, ok := parseSQLiteTime(string(typed)); ok {
				return parsed
			}
		}
	}

	return value
}

// columnClass identifies the groups of declared column types that need coercion.
type columnClass int

const (
	columnClassOther columnClass = iota
	columnClassJSON
	columnClassBool
	columnClassTime
)

// sqliteColumnClass maps a declared column type to its coercion class.
func sqliteColumnClass(declaredType string) columnClass {

	switch strings.ToUpper(strings.TrimSpace(declaredType)) {
	case "JSON", "JSONB":
		return columnClassJSON
	case "BOOL", "BOOLEAN":
		return columnClassBool
	case "TIMESTAMP", "TIMESTAMPTZ", "DATETIME", "DATE":
		return columnClassTime
	default:
		return columnClassOther
	}
}

// parseSQLiteTime parses a timestamp stored by the inbuilt database.
func parseSQLiteTime(value string) (time.Time, bool) {

	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, false
	}

	for _, layout := range sqliteTimeLayouts {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed.UTC(), true
		}
	}

	return time.Time{}, false
}

// parseSQLiteBool interprets the boolean spellings SQLite may hold.
func parseSQLiteBool(value string) bool {

	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes":
		return true
	default:
		return false
	}
}
