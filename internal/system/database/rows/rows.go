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

// Package rows reads typed values from the rows that the DB client returns. The two dialects
// return different Go types for the same column (for example, a boolean from a join in SQLite
// can arrive as an int64), so the readers accept each form.
package rows

import (
	"fmt"
	"time"
)

// String returns the column as a string, or "" when it is NULL.
func String(row map[string]interface{}, column string) string {

	switch v := row[column].(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

// Int returns the column as an int, or 0 when it is NULL.
func Int(row map[string]interface{}, column string) int {

	switch v := row[column].(type) {
	case int64:
		return int(v)
	case int32:
		return int(v)
	case int:
		return v
	case float64:
		return int(v)
	default:
		return 0
	}
}

// Bool returns the column as a bool.
func Bool(row map[string]interface{}, column string) bool {

	switch v := row[column].(type) {
	case bool:
		return v
	case int64:
		return v != 0
	case string:
		return v == "1" || v == "true" || v == "t"
	default:
		return false
	}
}

// Time returns the column as a time, or the zero time when it is NULL or not a time.
func Time(row map[string]interface{}, column string) time.Time {

	switch v := row[column].(type) {
	case time.Time:
		return v
	case string:
		for _, layout := range []string{"2006-01-02 15:04:05.999999999-07:00", time.RFC3339Nano} {
			if parsed, err := time.Parse(layout, v); err == nil {
				return parsed.UTC()
			}
		}
	}
	return time.Time{}
}
