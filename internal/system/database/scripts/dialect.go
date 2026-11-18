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
// that stores assemble at runtime, which queries.go cannot cover. Each helper
// names every supported datasource and returns an error for any other, so an
// unrecognized type never receives another datasource's SQL.

// unsupportedDatasource is the error a helper returns for a datasource it has no
// fragment for.
func unsupportedDatasource(dbType string) error {
	return fmt.Errorf("unsupported datasource type %q", dbType)
}

// LikeOperator returns the pattern-match operator for case-insensitive search.
// PostgreSQL has ILIKE for this. SQLite's LIKE is case-insensitive for ASCII,
// which is the closest equivalent; MySQL and MSSQL decide case by the collation
// of the column, so their LIKE is the operator and the schema supplies the
// collation.
func LikeOperator(dbType string) (string, error) {
	switch dbType {
	case database.TypePostgres:
		return "ILIKE", nil
	case database.TypeSQLite, database.TypeMySQL, database.TypeMSSQL:
		return "LIKE", nil
	default:
		return "", unsupportedDatasource(dbType)
	}
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

// validateJSONPath checks every key of a path before it is interpolated into a
// statement.
func validateJSONPath(path []string) error {
	if len(path) == 0 {
		return fmt.Errorf("JSON path must not be empty")
	}
	for _, key := range path {
		if err := ValidateJSONKey(key); err != nil {
			return err
		}
	}
	return nil
}

// JSONEqCondition builds an equality condition against a key nested inside a
// JSON column, together with the argument to bind for it. The argument is
// returned because the datasources need different ones, though all select the
// same rows.
//
// PostgreSQL tests containment, which keeps its GIN indexes usable. The other
// datasources read the value at the path as text and compare it, so a value at
// the path that is an array or an object does not match.
//
// path is the chain of keys to the value, e.g. ["app_specific_data", "email"].
func JSONEqCondition(dbType, column string, path []string, value string, argID int) (string, interface{}, error) {
	if err := validateJSONPath(path); err != nil {
		return "", nil, err
	}

	switch dbType {
	case database.TypePostgres:
		return postgresJSONContainment(column, path, value, argID)
	case database.TypeSQLite:
		condition := fmt.Sprintf("json_extract(%s, '%s') = $%d", column, jsonPathExpression(path), argID)
		return condition, value, nil
	case database.TypeMySQL:
		condition := fmt.Sprintf("JSON_UNQUOTE(JSON_EXTRACT(%s, '%s')) = $%d",
			column, jsonPathExpression(path), argID)
		return condition, value, nil
	case database.TypeMSSQL:
		condition := fmt.Sprintf("JSON_VALUE(%s, '%s') = $%d", column, jsonPathExpression(path), argID)
		return condition, value, nil
	default:
		return "", nil, unsupportedDatasource(dbType)
	}
}

// postgresJSONContainment builds the containment condition and the JSON document
// it tests for.
func postgresJSONContainment(column string, path []string, value string, argID int) (
	string, interface{}, error) {
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

// JSONLikeCondition builds a pattern-match condition against a key nested
// inside a JSON column. The caller binds the pattern.
func JSONLikeCondition(dbType, column string, path []string, argID int) (string, error) {
	if err := validateJSONPath(path); err != nil {
		return "", err
	}
	operator, err := LikeOperator(dbType)
	if err != nil {
		return "", err
	}

	switch dbType {
	case database.TypePostgres:
		// Navigate with -> and read the last key as text with ->>.
		accessor := column
		for i, key := range path {
			if i == len(path)-1 {
				accessor = fmt.Sprintf("%s ->> '%s'", accessor, key)
				continue
			}
			accessor = fmt.Sprintf("%s -> '%s'", accessor, key)
		}
		return fmt.Sprintf("%s %s $%d", accessor, operator, argID), nil
	case database.TypeSQLite:
		return fmt.Sprintf("json_extract(%s, '%s') %s $%d", column, jsonPathExpression(path), operator, argID), nil
	case database.TypeMySQL:
		return fmt.Sprintf("JSON_UNQUOTE(JSON_EXTRACT(%s, '%s')) %s $%d",
			column, jsonPathExpression(path), operator, argID), nil
	case database.TypeMSSQL:
		return fmt.Sprintf("JSON_VALUE(%s, '%s') %s $%d", column, jsonPathExpression(path), operator, argID), nil
	default:
		return "", unsupportedDatasource(dbType)
	}
}

// jsonPathExpression renders a JSON path expression for json_extract,
// JSON_EXTRACT and JSON_VALUE. Each key is quoted so that a key containing a dot
// is not read as a nested lookup.
func jsonPathExpression(path []string) string {
	var builder strings.Builder
	builder.WriteString("$")
	for _, key := range path {
		builder.WriteString(`."`)
		builder.WriteString(key)
		builder.WriteString(`"`)
	}

	return builder.String()
}

// KeysetCondition builds the condition used for cursor pagination: the rows
// whose (created_at, profile_id) pair is before or after the cursor's.
//
// operator is one of <, >, <= and >=. PostgreSQL, SQLite and MySQL compare the
// pair as a row. MSSQL has no row comparison, so the same test is written out:
// the first column compares strictly, or it is equal and the second compares by
// the operator.
func KeysetCondition(dbType, operator string, timestampArgID, idArgID int) (string, error) {
	switch operator {
	case "<", ">", "<=", ">=":
	default:
		return "", fmt.Errorf("unsupported keyset operator %q", operator)
	}

	switch dbType {
	case database.TypePostgres:
		return fmt.Sprintf("(p.created_at, p.profile_id) %s ($%d::timestamptz, $%d::text)",
			operator, timestampArgID, idArgID), nil
	case database.TypeSQLite, database.TypeMySQL:
		return fmt.Sprintf("(p.created_at, p.profile_id) %s ($%d, $%d)", operator, timestampArgID, idArgID), nil
	case database.TypeMSSQL:
		strict := strings.TrimSuffix(operator, "=")
		return fmt.Sprintf("(p.created_at %s $%d OR (p.created_at = $%d AND p.profile_id %s $%d))",
			strict, timestampArgID, timestampArgID, operator, idArgID), nil
	default:
		return "", unsupportedDatasource(dbType)
	}
}

// EncodeStringArray prepares a list of strings for storage: a PostgreSQL array
// or, for the datasources with no array type, a JSON array. DecodeStringArray
// reads back either form.
func EncodeStringArray(dbType string, values []string) (interface{}, error) {
	switch dbType {
	case database.TypePostgres:
		return pq.Array(values), nil
	case database.TypeSQLite, database.TypeMySQL, database.TypeMSSQL:
		if values == nil {
			values = []string{}
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			return nil, err
		}
		return string(encoded), nil
	default:
		return nil, unsupportedDatasource(dbType)
	}
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

	// JSON array, as written for every datasource but PostgreSQL.
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
