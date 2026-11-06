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

package scripts

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/lib/pq"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// safeJSONKey restricts the JSON keys that may be interpolated into a statement
// to the same character set the filter values allow.
var safeJSONKey = regexp.MustCompile(constants.FilterRegex)

// This file holds the dialect-specific SQL fragments used by the statements
// that stores assemble at runtime, which queries.go cannot cover.

// LikeOperator returns the case-insensitive pattern-match operator. SQLite's
// LIKE is case-insensitive for ASCII, which is the closest equivalent of
// PostgreSQL's ILIKE.
func LikeOperator(dbType string) string {

	if dbType == database.TypeSQLite {
		return "LIKE"
	}
	return "ILIKE"
}

// ValidateJSONKey checks a JSON key before it is interpolated into a statement.
// Keys arrive from user-supplied filters, so they are restricted to the
// characters allowed for filter values.
func ValidateJSONKey(key string) error {

	if key == "" {
		return fmt.Errorf("JSON key must not be empty")
	}
	if !safeJSONKey.MatchString(key) {
		return fmt.Errorf("invalid JSON key: %s", key)
	}
	return nil
}

// JSONEqCondition builds an equality condition against a key nested inside a
// JSON column, together with the argument to bind for it. The argument is
// returned because the two dialects need different ones, though both select the
// same rows.
//
// path is the chain of keys to the value, e.g. ["app_specific_data", "email"].
func JSONEqCondition(dbType, column string, path []string, value string, argID int) (string, interface{}, error) {

	if len(path) == 0 {
		return "", nil, fmt.Errorf("JSON path must not be empty")
	}
	for _, key := range path {
		if err := ValidateJSONKey(key); err != nil {
			return "", nil, err
		}
	}

	if dbType == database.TypeSQLite {
		condition := fmt.Sprintf("json_extract(%s, '%s') = $%d", column, sqliteJSONPath(path), argID)
		return condition, value, nil
	}

	// PostgreSQL matches by containment, which keeps the GIN indexes usable.
	valueJSON, err := json.Marshal(value)
	if err != nil {
		return "", nil, err
	}
	document := string(valueJSON)
	for i := len(path) - 1; i >= 0; i-- {
		keyJSON, err := json.Marshal(path[i])
		if err != nil {
			return "", nil, err
		}
		document = fmt.Sprintf("{%s: %s}", keyJSON, document)
	}

	return fmt.Sprintf("%s @> $%d::jsonb", column, argID), document, nil
}

// JSONLikeCondition builds a case-insensitive pattern-match condition against a
// key nested inside a JSON column. The caller binds the pattern.
func JSONLikeCondition(dbType, column string, path []string, argID int) (string, error) {

	if len(path) == 0 {
		return "", fmt.Errorf("JSON path must not be empty")
	}
	for _, key := range path {
		if err := ValidateJSONKey(key); err != nil {
			return "", err
		}
	}

	if dbType == database.TypeSQLite {
		return fmt.Sprintf("json_extract(%s, '%s') LIKE $%d", column, sqliteJSONPath(path), argID), nil
	}

	// PostgreSQL: navigate with -> and read the last key as text with ->>.
	accessor := column
	for i, key := range path {
		if i == len(path)-1 {
			accessor = fmt.Sprintf("%s ->> '%s'", accessor, key)
			continue
		}
		accessor = fmt.Sprintf("%s -> '%s'", accessor, key)
	}

	return fmt.Sprintf("%s %s $%d", accessor, LikeOperator(dbType), argID), nil
}

// sqliteJSONPath renders a JSON path expression for json_extract. Each key is
// quoted so that a key containing a dot is not read as a nested lookup.
func sqliteJSONPath(path []string) string {

	var builder strings.Builder
	builder.WriteString("$")
	for _, key := range path {
		builder.WriteString(`."`)
		builder.WriteString(key)
		builder.WriteString(`"`)
	}

	return builder.String()
}

// KeysetCondition builds the row-value comparison used for cursor pagination.
func KeysetCondition(dbType, operator string, timestampArgID, idArgID int) string {

	if dbType == database.TypeSQLite {
		return fmt.Sprintf("(p.created_at, p.profile_id) %s ($%d, $%d)", operator, timestampArgID, idArgID)
	}
	return fmt.Sprintf("(p.created_at, p.profile_id) %s ($%d::timestamptz, $%d::text)",
		operator, timestampArgID, idArgID)
}

// EncodeStringArray prepares a list of strings for storage: a PostgreSQL array
// or, since SQLite has no array type, a JSON array. DecodeStringArray reads
// back either form.
func EncodeStringArray(dbType string, values []string) interface{} {

	if dbType != database.TypeSQLite {
		return pq.Array(values)
	}

	if values == nil {
		values = []string{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}

	return string(encoded)
}

// DecodeStringArray reads a list of strings written by EncodeStringArray, in
// either form.
func DecodeStringArray(raw interface{}) []string {

	var text string
	switch typed := raw.(type) {
	case nil:
		return nil
	case string:
		text = typed
	case []byte:
		text = string(typed)
	default:
		return nil
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	// JSON array, as written for SQLite.
	if strings.HasPrefix(text, "[") {
		var values []string
		if err := json.Unmarshal([]byte(text), &values); err != nil {
			return nil
		}
		return values
	}

	// PostgreSQL array literal.
	text = strings.Trim(text, "{}")
	if text == "" {
		return nil
	}
	parts := strings.Split(text, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		values = append(values, strings.Trim(strings.TrimSpace(part), `"`))
	}

	return values
}
