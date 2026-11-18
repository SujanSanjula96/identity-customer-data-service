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
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

func Test_normalizesResults(t *testing.T) {
	expected := map[string]bool{
		database.TypePostgres: false,
		database.TypeSQLite:   true,
		database.TypeMySQL:    true,
		database.TypeMSSQL:    true,
		"":                    false,
	}
	for dbType, want := range expected {
		if got := normalizesResults(dbType); got != want {
			t.Errorf("normalizesResults(%q) = %v, expected %v", dbType, got, want)
		}
	}
}

func Test_normalizeValue_DispatchesByDatasource(t *testing.T) {
	if got := normalizeValue(database.TypePostgres, []byte("x"), "VARCHAR", "c"); !reflect.DeepEqual(got, []byte("x")) {
		t.Errorf("PostgreSQL values must not change, got %#v", got)
	}
	if got := normalizeValue(database.TypeSQLite, int64(1), "BOOLEAN", "c"); got != true {
		t.Errorf("SQLite boolean not converted, got %#v", got)
	}
	if got := normalizeValue(database.TypeMySQL, []byte("x"), "VARCHAR", "c"); got != "x" {
		t.Errorf("MySQL text not converted, got %#v", got)
	}
	if got := normalizeValue(database.TypeMSSQL, "{}", "NVARCHAR", "traits"); !reflect.DeepEqual(got, []byte("{}")) {
		t.Errorf("MSSQL JSON not converted, got %#v", got)
	}
}

func Test_normalizeMySQLValue(t *testing.T) {
	moment := time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.UTC)
	local := time.Date(2026, 3, 4, 7, 6, 7, 0, time.FixedZone("x", 7200))

	cases := []struct {
		name         string
		value        interface{}
		declaredType string
		column       string
		expected     interface{}
	}{
		{"nil stays nil", nil, "VARCHAR", "user_id", nil},
		{"varchar bytes become text", []byte("u-42"), "VARCHAR", "user_id", "u-42"},
		{"text types become text", []byte("long"), "LONGTEXT", "sub_attributes", "long"},
		{"text already a string", "u-42", "VARCHAR", "user_id", "u-42"},
		{"json stays bytes", []byte(`{"a":1}`), "JSON", "traits", []byte(`{"a":1}`)},
		{"json given as text becomes bytes", `{"a":1}`, "JSON", "traits", []byte(`{"a":1}`)},
		{"boolean column as 1", int64(1), "TINYINT", "list_profile", true},
		{"boolean column as 0", int64(0), "TINYINT", "delete_profile", false},
		{"boolean column as text", []byte("1"), "TINYINT", "is_mandatory", true},
		{"boolean column name is case-insensitive", int64(1), "TINYINT", "IS_ACTIVE", true},
		{"other tinyint stays a number", int64(1), "TINYINT", "priority", int64(1)},
		{"other tinyint as text becomes a number", []byte("7"), "TINYINT", "priority", int64(7)},
		{"int as text becomes int64", []byte("42"), "INT", "n", int64(42)},
		{"bigint as int64 stays", int64(42), "BIGINT", "n", int64(42)},
		{"unsigned bigint as text becomes int64", []byte("42"), "UNSIGNED BIGINT", "n", int64(42)},
		{"unparseable integer text is kept", []byte("x"), "INT", "n", []byte("x")},
		{"double as text becomes float64", []byte("0.5"), "DOUBLE", "n", 0.5},
		{"decimal stays bytes", []byte("0.8700"), "DECIMAL", "match_score", []byte("0.8700")},
		{"datetime is returned in UTC", local, "DATETIME", "created_at", local.UTC()},
		{"datetime as text is parsed", []byte("2026-03-04 05:06:07.123456"), "DATETIME", "created_at", moment},
		{"unparseable datetime is kept", []byte("0000-00-00"), "DATETIME", "created_at", []byte("0000-00-00")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeMySQLValue(c.value, c.declaredType, c.column)
			if !reflect.DeepEqual(got, c.expected) {
				t.Fatalf("expected %#v (%T), got %#v (%T)", c.expected, c.expected, got, got)
			}
		})
	}
}

func Test_normalizeMSSQLValue(t *testing.T) {
	local := time.Date(2026, 3, 4, 7, 6, 7, 0, time.FixedZone("x", 7200))

	cases := []struct {
		name         string
		value        interface{}
		declaredType string
		column       string
		expected     interface{}
	}{
		{"nil stays nil", nil, "NVARCHAR", "traits", nil},
		{"json column given as text becomes bytes", `{"a":1}`, "NVARCHAR", "traits", []byte(`{"a":1}`)},
		{"json column name is case-insensitive", `{}`, "NVARCHAR", "Identity_Attributes", []byte(`{}`)},
		{"application data becomes bytes", `{}`, "NVARCHAR", "application_data", []byte(`{}`)},
		{"json column already bytes stays", []byte(`{}`), "NVARCHAR", "traits", []byte(`{}`)},
		{"ordinary text stays a string", "u-42", "NVARCHAR", "user_id", "u-42"},
		{"text given as bytes becomes a string", []byte("u-42"), "VARCHAR", "user_id", "u-42"},
		{"bit stays a bool", true, "BIT", "list_profile", true},
		{"bit as a number becomes a bool", int64(1), "BIT", "list_profile", true},
		{"int stays int64", int64(7), "INT", "priority", int64(7)},
		{"decimal stays bytes", []byte("0.8700"), "DECIMAL", "match_score", []byte("0.8700")},
		{"datetime2 is returned in UTC", local, "DATETIME2", "created_at", local.UTC()},
		{"datetimeoffset is returned in UTC", local, "DATETIMEOFFSET", "created_at", local.UTC()},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeMSSQLValue(c.value, c.declaredType, c.column)
			if !reflect.DeepEqual(got, c.expected) {
				t.Fatalf("expected %#v (%T), got %#v (%T)", c.expected, c.expected, got, got)
			}
		})
	}
}

// Test_columnSetsMatchTheSchema holds the two named sets to the schema, which
// is their source of truth: a BOOLEAN column added to the schema without being
// named here would be read back as a number.
func Test_columnSetsMatchTheSchema(t *testing.T) {
	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "dbscripts", "postgres.sql"))
	if err != nil {
		t.Fatalf("cannot read the schema: %v", err)
	}

	declaration := regexp.MustCompile(`(?im)^\s*([a-z_]+)\s+(BOOLEAN|JSONB)\b`)
	schemaBooleans := map[string]bool{}
	schemaJSON := map[string]bool{}
	for _, match := range declaration.FindAllStringSubmatch(string(schema), -1) {
		column, columnType := strings.ToLower(match[1]), strings.ToUpper(match[2])
		if columnType == "BOOLEAN" {
			schemaBooleans[column] = true
		} else {
			schemaJSON[column] = true
		}
	}

	if !reflect.DeepEqual(schemaBooleans, booleanColumns) {
		t.Errorf("booleanColumns %v does not match the schema's BOOLEAN columns %v",
			sortedKeys(booleanColumns), sortedKeys(schemaBooleans))
	}

	// The stores read these three as JSON in a []byte. The schema also holds JSONB
	// in columns that are only read as text, so this is a subset check.
	for column := range jsonColumns {
		if !schemaJSON[column] {
			t.Errorf("jsonColumns names %q, which is not a JSONB column in the schema", column)
		}
	}
}

// The MySQL schema declares the same columns as the PostgreSQL one with MySQL's types,
// and the sets are shared, so it is held to them as well.
func Test_columnSetsMatchTheMySQLSchema(t *testing.T) {
	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "dbscripts", "mysql.sql"))
	if err != nil {
		t.Fatalf("cannot read the schema: %v", err)
	}

	declaration := regexp.MustCompile(`(?im)^\s*([a-z_]+)\s+(BOOLEAN|JSON)\b`)
	schemaBooleans := map[string]bool{}
	schemaJSON := map[string]bool{}
	for _, match := range declaration.FindAllStringSubmatch(string(schema), -1) {
		column, columnType := strings.ToLower(match[1]), strings.ToUpper(match[2])
		if columnType == "BOOLEAN" {
			schemaBooleans[column] = true
		} else {
			schemaJSON[column] = true
		}
	}

	if !reflect.DeepEqual(schemaBooleans, booleanColumns) {
		t.Errorf("booleanColumns %v does not match the MySQL schema's BOOLEAN columns %v",
			sortedKeys(booleanColumns), sortedKeys(schemaBooleans))
	}
	if !reflect.DeepEqual(schemaJSON, jsonColumns) {
		t.Errorf("jsonColumns %v does not match the MySQL schema's JSON columns %v",
			sortedKeys(jsonColumns), sortedKeys(schemaJSON))
	}
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
