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

package scripts

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
)

// BuildFindOldestReferenceProfileIDByAttributeValuesQuery builds a bounded
// profile lookup with string equality/array overlap semantics. Only the two
// supported column names enter SQL; paths and values are encoded and bound as
// data, including quotes and JSON metacharacters. Arguments $1-$3 are the
// organization, excluded profile ID, and incoming profile user ID.
func BuildFindOldestReferenceProfileIDByAttributeValuesQuery(
	dbType, property string, values []string) (model.DBQuery, []interface{}, error) {
	parts := strings.Split(property, ".")
	if len(parts) < 2 || (parts[0] != "traits" && parts[0] != "identity_attributes") {
		return model.DBQuery{}, nil, fmt.Errorf("unsupported profile property %q", property)
	}
	for _, part := range parts[1:] {
		if part == "" {
			return model.DBQuery{}, nil, fmt.Errorf("empty key in profile property %q", property)
		}
	}
	if len(values) == 0 {
		return model.DBQuery{}, nil, fmt.Errorf("profile lookup requires at least one string value")
	}
	column := "p." + parts[0]
	query := FindOldestReferenceProfileIDByAttributeValues.Format(column)
	switch dbType {
	case database.TypeSQLite:
		pathJSON, _ := json.Marshal(parts[1:])
		valuesJSON, _ := json.Marshal(values)
		return query,
			[]interface{}{string(pathJSON), string(valuesJSON)}, nil
	case database.TypePostgres:
		return query, postgresAttributeLookupArgs(parts[1:], values), nil
	case database.TypeMySQL:
		valuesJSON, _ := json.Marshal(values)
		return query, []interface{}{mysqlLookupPath(parts[1:]), string(valuesJSON)}, nil
	case database.TypeMSSQL:
		keys := parts[1:]
		valuesJSON, _ := json.Marshal(values)
		args := make([]interface{}, 0, len(keys)+1)
		for _, key := range keys {
			args = append(args, key)
		}
		args = append(args, string(valuesJSON))
		return query.WithOverride(database.TypeMSSQL, mssqlLookupStatement(column, len(keys))), args, nil
	case database.TypeOracle:
		args := make([]interface{}, len(values))
		for i, value := range values {
			args[i] = value
		}
		return query.WithOverride(database.TypeOracle, oracleLookupStatement(column, parts[1:], len(values))), args, nil
	default:
		return model.DBQuery{}, nil, unsupportedDatasource(dbType)
	}
}

// postgresAttributeLookupArgs builds the JSONPath arguments of the PostgreSQL
// attribute lookup, after the three arguments the caller binds.
func postgresAttributeLookupArgs(keys, values []string) []interface{} {
	// @? supplies an indexable prefilter. The exact check unwraps the leaf
	// once because lax JSONPath equality alone also matches nested arrays.
	leafPath := quotedJSONPath(keys)
	var path strings.Builder
	path.WriteString(leafPath)
	predicates := make([]string, 0, len(values))
	for _, value := range values {
		encoded, _ := json.Marshal(value)
		predicates = append(predicates, "@ == "+string(encoded))
	}
	path.WriteString(" ? (")
	path.WriteString(strings.Join(predicates, " || "))
	path.WriteByte(')')
	valuesJSON, _ := json.Marshal(values)
	return []interface{}{path.String(), leafPath, string(valuesJSON)}
}

// quotedJSONPath renders the path to a key, quoting each key as a JSON string
// so that a key containing a dot or a quote is read as one key. It is bound as
// an argument, never placed in the statement text.
func quotedJSONPath(keys []string) string {
	var path strings.Builder
	path.WriteString("$")
	for _, part := range keys {
		key, _ := json.Marshal(part)
		path.WriteByte('.')
		path.Write(key)
	}
	return path.String()
}

// mysqlLookupPath renders the path to a key for MySQL, which has no lax mode of
// its own. A [0 to last] after every key, the last one included, makes the path
// read as PostgreSQL's lax JSONPath does: MySQL reads an array there element by
// element (once) and wraps an object or a scalar as a one-element array, so a
// string or an array at the last key gives its immediate elements.
func mysqlLookupPath(keys []string) string {
	var path strings.Builder
	path.WriteString("$")
	for _, part := range keys {
		key, _ := json.Marshal(part)
		path.WriteByte('.')
		path.Write(key)
		path.WriteString("[0 to last]")
	}
	return path.String()
}

// mssqlLookupStatement builds the attribute lookup for SQL Server, which has
// neither a lax JSON path nor a way to take a path of any length as one
// argument. The statement reads the column key by key with OPENJSON, so its text
// depends on the number of keys. The keys and the wanted values are bound as
// arguments: $4 to $(3+keys) are the keys and the next one is the JSON array of
// values.
//
// Each step reads a value as PostgreSQL's lax JSONPath does: an array is read
// element by element (once) and anything else as itself, and only then is the
// key looked up in the objects. The last step reads strings, from an array one
// level deep or from the value itself. The collation is binary, so the match is
// case-sensitive.
func mssqlLookupStatement(column string, keyCount int) string {
	const (
		binary = "COLLATE Latin1_General_100_BIN2"
		// unwrap reads the rows of the previous step element by element when
		// they are arrays. OPENJSON gets '[]' for any other row, because it
		// rejects text that is not JSON.
		unwrap = "CROSS APPLY (SELECT %[1]s.v AS v, %[1]s.t AS t WHERE %[1]s.t <> 4 UNION ALL " +
			"SELECT e.value, e.type FROM OPENJSON(CASE WHEN %[1]s.t = 4 THEN %[1]s.v ELSE '[]' END) e) %[2]s "
		member = "CROSS APPLY (SELECT m.value AS v, m.type AS t FROM " +
			"OPENJSON(CASE WHEN %[1]s.t = 5 THEN %[1]s.v ELSE '{}' END) m WHERE m.[key] " + binary + " = $%[2]d) %[3]s "
	)

	var from strings.Builder
	fmt.Fprintf(&from, "(SELECT %s AS v, 5 AS t) s0 ", column)
	previous := "s0"
	for i := 1; i <= keyCount; i++ {
		unwrapped := fmt.Sprintf("u%d", i)
		current := fmt.Sprintf("s%d", i)
		fmt.Fprintf(&from, unwrap, previous, unwrapped)
		fmt.Fprintf(&from, member, unwrapped, 3+i, current)
		previous = current
	}
	fmt.Fprintf(&from, unwrap, previous, "leaf")

	return fmt.Sprintf(`SELECT TOP (1) p.profile_id
	FROM profiles p JOIN profile_reference r ON r.profile_id = p.profile_id
	WHERE p.org_id = $1 AND r.org_id = $1
	AND r.profile_status = 'REFERENCE_PROFILE'
	AND p.profile_id != $2
	AND ($3 = '' OR COALESCE(p.user_id, '') = '' OR p.user_id = $3)
	AND EXISTS (
		SELECT 1 FROM %s
		WHERE leaf.t = 1 AND leaf.v %s IN (SELECT value %s FROM OPENJSON($%d))
	)
	ORDER BY p.created_at, p.profile_id`, from.String(), binary, binary, 4+keyCount)
}

// oracleLookupStatement builds the attribute lookup for Oracle.
func oracleLookupStatement(column string, keys []string, valueCount int) string {
	var path strings.Builder
	path.WriteString("$")
	for _, key := range keys {
		path.WriteString(`."`)
		path.WriteString(oracleEscapeLiteral(oracleEscapePathKey(key)))
		path.WriteString(`"`)
	}

	predicates := make([]string, valueCount)
	bindings := make([]string, valueCount)
	for i := range predicates {
		predicates[i] = fmt.Sprintf("@ == $v%d", i)
		bindings[i] = fmt.Sprintf(`$%d AS "v%d"`, 4+i, i)
	}

	return fmt.Sprintf(`SELECT p.profile_id
	FROM profiles p JOIN profile_reference r ON r.profile_id = p.profile_id
	WHERE p.org_id = $1 AND r.org_id = $1
	AND r.profile_status = 'REFERENCE_PROFILE'
	AND ($2 IS NULL OR p.profile_id != $2)
	AND ($3 IS NULL OR p.user_id IS NULL OR p.user_id = $3)
	AND JSON_EXISTS(%s, '%s[*]?(@.type() == "string" && (%s))' PASSING %s)
	ORDER BY p.created_at, p.profile_id
	FETCH FIRST 1 ROWS ONLY`, column, path.String(), strings.Join(predicates, " || "), strings.Join(bindings, ", "))
}

// oracleEscapePathKey escapes a key for a quoted name in a JSON path.
func oracleEscapePathKey(key string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(key)
}

// oracleEscapeLiteral escapes text for a single-quoted SQL literal.
func oracleEscapeLiteral(text string) string {
	return strings.ReplaceAll(text, "'", "''")
}
