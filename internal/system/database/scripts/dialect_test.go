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
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

const selectOne = "SELECT 1"

func Test_newQuery(t *testing.T) {
	t.Run("without an override both dialects share the statement", func(t *testing.T) {
		query := newQuery("CDS-TST-01", selectOne)
		if query.ID != "CDS-TST-01" {
			t.Fatalf("unexpected id: %q", query.ID)
		}
		if statement := query.GetQuery(database.TypePostgres); statement != selectOne {
			t.Fatalf("unexpected PostgreSQL statement: %q", statement)
		}
		if statement := query.GetQuery(database.TypeSQLite); statement != selectOne {
			t.Fatalf("unexpected SQLite statement: %q", statement)
		}
	})

	t.Run("an override applies to SQLite only", func(t *testing.T) {
		query := newQuery("CDS-TST-02", `SELECT now()`, `SELECT current_timestamp`)
		if statement := query.GetQuery(database.TypePostgres); statement != `SELECT now()` {
			t.Fatalf("the override leaked into the PostgreSQL statement: %q", statement)
		}
		if statement := query.GetQuery(database.TypeSQLite); statement != `SELECT current_timestamp` {
			t.Fatalf("unexpected SQLite statement: %q", statement)
		}
	})

	t.Run("an empty override falls back to the base statement", func(t *testing.T) {
		query := newQuery("CDS-TST-03", selectOne, "")
		if statement := query.GetQuery(database.TypeSQLite); statement != selectOne {
			t.Fatalf("unexpected SQLite statement: %q", statement)
		}
	})

	t.Run("a dialect without an override falls back to the base statement", func(t *testing.T) {
		query := newQuery("CDS-TST-04", selectOne, `SELECT 2`)
		if statement := query.GetQuery(database.TypeMySQL); statement != selectOne {
			t.Fatalf("expected the PostgreSQL statement, got %q", statement)
		}
	})
}

func Test_LikeOperator(t *testing.T) {
	expected := map[string]string{
		database.TypePostgres: "ILIKE",
		database.TypeSQLite:   "LIKE",
		database.TypeMySQL:    "LIKE",
		database.TypeMSSQL:    "LIKE",
		database.TypeOracle:   "LIKE",
	}
	for dbType, want := range expected {
		operator, err := LikeOperator(dbType)
		if err != nil {
			t.Fatalf("LikeOperator(%q) failed: %v", dbType, err)
		}
		if operator != want {
			t.Errorf("LikeOperator(%q) = %q, expected %q", dbType, operator, want)
		}
	}

	if _, err := LikeOperator("db2"); err == nil {
		t.Error("expected an error for an unsupported datasource")
	}
	if _, err := LikeOperator(""); err == nil {
		t.Error("expected an error for an empty datasource")
	}
}

func Test_ValidateJSONKey(t *testing.T) {
	valid := []string{"email", "app_specific_data", "ui.mode", "a-b", "A1"}
	for _, key := range valid {
		if err := ValidateJSONKey(key); err != nil {
			t.Errorf("expected %q to be accepted: %v", key, err)
		}
	}

	invalid := []string{"", "'", `"`, "a'; DROP TABLE profiles; --", "a b", "a$b", "a\\b"}
	for _, key := range invalid {
		if err := ValidateJSONKey(key); err == nil {
			t.Errorf("expected %q to be rejected", key)
		}
	}
}

func Test_JSONEqCondition(t *testing.T) {
	t.Run("sqlite extracts the value and binds it directly", func(t *testing.T) {
		condition, arg, err := JSONEqCondition(database.TypeSQLite, "p.traits", []string{"email"}, "a@b.com", 3)
		if err != nil {
			t.Fatal(err)
		}
		if expected := `json_extract(p.traits, '$."email"') = $3`; condition != expected {
			t.Errorf("expected %q, got %q", expected, condition)
		}
		if arg != "a@b.com" {
			t.Errorf("expected the bare value, got %#v", arg)
		}
	})

	t.Run("sqlite quotes each key so a dotted key is one key", func(t *testing.T) {
		condition, _, err := JSONEqCondition(database.TypeSQLite, "p.traits",
			[]string{"app_specific_data", "ui.mode"}, "dark", 1)
		if err != nil {
			t.Fatal(err)
		}
		if expected := `json_extract(p.traits, '$."app_specific_data"."ui.mode"') = $1`; condition != expected {
			t.Errorf("expected %q, got %q", expected, condition)
		}
	})

	t.Run("postgres tests for containment", func(t *testing.T) {
		condition, arg, err := JSONEqCondition(database.TypePostgres, "p.traits",
			[]string{"app_specific_data", "email"}, "a@b.com", 2)
		if err != nil {
			t.Fatal(err)
		}
		if expected := `p.traits @> $2::jsonb`; condition != expected {
			t.Errorf("expected %q, got %q", expected, condition)
		}
		if expected := `{"app_specific_data": {"email": "a@b.com"}}`; arg != expected {
			t.Errorf("expected %q, got %#v", expected, arg)
		}
	})

	t.Run("a value with quotes is escaped, not interpolated", func(t *testing.T) {
		_, arg, err := JSONEqCondition(database.TypePostgres, "p.traits", []string{"email"}, `a"b`, 1)
		if err != nil {
			t.Fatal(err)
		}
		if expected := `{"email": "a\"b"}`; arg != expected {
			t.Errorf("expected %q, got %#v", expected, arg)
		}
	})

	t.Run("mysql reads the value as text and compares it", func(t *testing.T) {
		condition, arg, err := JSONEqCondition(database.TypeMySQL, "p.traits", []string{"email"}, "a@b.com", 3)
		if err != nil {
			t.Fatal(err)
		}
		if expected := `JSON_UNQUOTE(JSON_EXTRACT(p.traits, '$."email"')) = $3`; condition != expected {
			t.Errorf("expected %q, got %q", expected, condition)
		}
		if arg != "a@b.com" {
			t.Errorf("expected the bare value, got %#v", arg)
		}
	})

	t.Run("mssql reads the value as text and compares it", func(t *testing.T) {
		condition, arg, err := JSONEqCondition(database.TypeMSSQL, "a.application_data",
			[]string{"app_specific_data", "ui.mode"}, "dark", 2)
		if err != nil {
			t.Fatal(err)
		}
		if expected := `JSON_VALUE(a.application_data, '$."app_specific_data"."ui.mode"') = $2`; condition != expected {
			t.Errorf("expected %q, got %q", expected, condition)
		}
		if arg != "dark" {
			t.Errorf("expected the bare value, got %#v", arg)
		}
	})

	t.Run("oracle reads the value as text and compares it", func(t *testing.T) {
		condition, arg, err := JSONEqCondition(database.TypeOracle, "p.traits",
			[]string{"app_specific_data", "ui.mode"}, "dark", 2)
		if err != nil {
			t.Fatal(err)
		}
		if expected := `JSON_VALUE(p.traits, '$."app_specific_data"."ui.mode"') = $2`; condition != expected {
			t.Errorf("expected %q, got %q", expected, condition)
		}
		if arg != "dark" {
			t.Errorf("expected the bare value, got %#v", arg)
		}
	})

	t.Run("an invalid key is rejected", func(t *testing.T) {
		for _, dbType := range supportedDatasources {
			if _, _, err := JSONEqCondition(
				dbType, "p.traits", []string{`e"; DROP TABLE profiles; --`}, "x", 1,
			); err == nil {
				t.Errorf("expected an error for %s", dbType)
			}
		}
	})

	t.Run("an unsupported datasource is rejected", func(t *testing.T) {
		if _, _, err := JSONEqCondition("db2", "p.traits", []string{"email"}, "x", 1); err == nil {
			t.Error("expected an error")
		}
	})

	t.Run("an empty path is rejected", func(t *testing.T) {
		if _, _, err := JSONEqCondition(database.TypeSQLite, "p.traits", nil, "x", 1); err == nil {
			t.Error("expected an error for an empty path")
		}
	})
}

func Test_JSONLikeCondition(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		condition, err := JSONLikeCondition(database.TypeSQLite, "a.application_data",
			[]string{"app_specific_data", "email"}, 4)
		if err != nil {
			t.Fatal(err)
		}
		expected := `json_extract(a.application_data, '$."app_specific_data"."email"') LIKE $4`
		if condition != expected {
			t.Errorf("expected %q, got %q", expected, condition)
		}
	})

	t.Run("postgres", func(t *testing.T) {
		condition, err := JSONLikeCondition(database.TypePostgres, "a.application_data",
			[]string{"app_specific_data", "email"}, 4)
		if err != nil {
			t.Fatal(err)
		}
		expected := `a.application_data -> 'app_specific_data' ->> 'email' ILIKE $4`
		if condition != expected {
			t.Errorf("expected %q, got %q", expected, condition)
		}
	})

	t.Run("mysql", func(t *testing.T) {
		condition, err := JSONLikeCondition(database.TypeMySQL, "a.application_data",
			[]string{"app_specific_data", "email"}, 4)
		if err != nil {
			t.Fatal(err)
		}
		expected := `JSON_UNQUOTE(JSON_EXTRACT(a.application_data, '$."app_specific_data"."email"')) LIKE $4`
		if condition != expected {
			t.Errorf("expected %q, got %q", expected, condition)
		}
	})

	t.Run("mssql", func(t *testing.T) {
		condition, err := JSONLikeCondition(database.TypeMSSQL, "p.traits", []string{"email"}, 2)
		if err != nil {
			t.Fatal(err)
		}
		expected := `JSON_VALUE(p.traits, '$."email"') LIKE $2`
		if condition != expected {
			t.Errorf("expected %q, got %q", expected, condition)
		}
	})

	t.Run("an invalid key is rejected", func(t *testing.T) {
		for _, dbType := range supportedDatasources {
			if _, err := JSONLikeCondition(dbType, "p.traits", []string{"a'b"}, 1); err == nil {
				t.Errorf("expected an error for %s", dbType)
			}
		}
	})

	t.Run("an unsupported datasource is rejected", func(t *testing.T) {
		if _, err := JSONLikeCondition("db2", "p.traits", []string{"email"}, 1); err == nil {
			t.Error("expected an error")
		}
	})
}

var supportedDatasources = []string{
	database.TypePostgres, database.TypeSQLite, database.TypeMySQL, database.TypeMSSQL, database.TypeOracle,
}

func Test_KeysetCondition(t *testing.T) {
	cases := []struct {
		dbType, operator, expected string
	}{
		{database.TypeSQLite, "<", `(p.created_at, p.profile_id) < ($2, $3)`},
		{database.TypePostgres, ">", `(p.created_at, p.profile_id) > ($2::timestamptz, $3::text)`},
		{database.TypeMySQL, "<", `(p.created_at, p.profile_id) < ($2, $3)`},
		{database.TypeMySQL, ">=", `(p.created_at, p.profile_id) >= ($2, $3)`},
		{database.TypeMSSQL, "<", `(p.created_at < $2 OR (p.created_at = $2 AND p.profile_id < $3))`},
		{database.TypeOracle, "<", `(p.created_at < $2 OR (p.created_at = $2 AND p.profile_id < $3))`},
		{database.TypeOracle, ">=", `(p.created_at > $2 OR (p.created_at = $2 AND p.profile_id >= $3))`},
		{database.TypeMSSQL, ">", `(p.created_at > $2 OR (p.created_at = $2 AND p.profile_id > $3))`},
		{database.TypeMSSQL, "<=", `(p.created_at < $2 OR (p.created_at = $2 AND p.profile_id <= $3))`},
		{database.TypeMSSQL, ">=", `(p.created_at > $2 OR (p.created_at = $2 AND p.profile_id >= $3))`},
	}
	for _, c := range cases {
		actual, err := KeysetCondition(c.dbType, c.operator, 2, 3)
		if err != nil {
			t.Fatalf("KeysetCondition(%q, %q) failed: %v", c.dbType, c.operator, err)
		}
		if actual != c.expected {
			t.Errorf("KeysetCondition(%q, %q): expected %q, got %q", c.dbType, c.operator, c.expected, actual)
		}
	}

	t.Run("an operator that is not a comparison is rejected", func(t *testing.T) {
		for _, dbType := range supportedDatasources {
			if _, err := KeysetCondition(dbType, "= 1 OR 1 =", 2, 3); err == nil {
				t.Errorf("expected an error for %s", dbType)
			}
		}
	})

	t.Run("an unsupported datasource is rejected", func(t *testing.T) {
		if _, err := KeysetCondition("db2", "<", 2, 3); err == nil {
			t.Error("expected an error")
		}
	})
}

func Test_RowLimit(t *testing.T) {
	cases := map[string]string{
		database.TypePostgres: "LIMIT $4",
		database.TypeSQLite:   "LIMIT $4",
		database.TypeMySQL:    "LIMIT $4",
		database.TypeMSSQL:    "OFFSET 0 ROWS FETCH NEXT $4 ROWS ONLY",
		database.TypeOracle:   "FETCH FIRST $4 ROWS ONLY",
	}
	for _, dbType := range supportedDatasources {
		actual, err := RowLimit(dbType, 4)
		if err != nil {
			t.Fatalf("RowLimit(%q) failed: %v", dbType, err)
		}
		if actual != cases[dbType] {
			t.Errorf("RowLimit(%q): expected %q, got %q", dbType, cases[dbType], actual)
		}
	}

	if _, err := RowLimit("db2", 4); err == nil {
		t.Error("expected an error for an unsupported datasource")
	}
}

func Test_EncodeStringArray(t *testing.T) {
	t.Run("the datasources without an array type store a JSON array", func(t *testing.T) {
		for _, dbType := range []string{database.TypeSQLite, database.TypeMySQL, database.TypeMSSQL} {
			encoded, err := EncodeStringArray(dbType, []string{"a", "b"})
			if err != nil {
				t.Fatal(err)
			}
			if encoded != `["a","b"]` {
				t.Errorf("%s: expected a JSON array, got %#v", dbType, encoded)
			}
			encoded, err = EncodeStringArray(dbType, nil)
			if err != nil {
				t.Fatal(err)
			}
			if encoded != `[]` {
				t.Errorf("%s: expected an empty JSON array, got %#v", dbType, encoded)
			}
		}
	})

	t.Run("an unsupported datasource is rejected", func(t *testing.T) {
		if _, err := EncodeStringArray("db2", []string{"a"}); err == nil {
			t.Error("expected an error")
		}
	})

	t.Run("postgres uses the array type", func(t *testing.T) {
		encoded, err := EncodeStringArray(database.TypePostgres, []string{"a", "b"})
		if err != nil {
			t.Fatal(err)
		}
		valuer, ok := encoded.(driver.Valuer)
		if !ok {
			t.Fatalf("expected a driver.Valuer, got %#v", encoded)
		}
		value, err := valuer.Value()
		if err != nil {
			t.Fatal(err)
		}
		if value != `{"a","b"}` {
			t.Errorf("expected a PostgreSQL array literal, got %#v", value)
		}
	})
}

func Test_DecodeStringArray(t *testing.T) {
	testCases := []struct {
		name     string
		raw      interface{}
		expected []string
	}{
		{"json array as written for sqlite", `["a","b"]`, []string{"a", "b"}},
		{"empty json array", `[]`, []string{}},
		{"json array with a comma inside a value", `["a,b","c"]`, []string{"a,b", "c"}},
		{"postgres array literal", `{a,b}`, []string{"a", "b"}},
		{"postgres array literal as bytes", []byte(`{a,b}`), []string{"a", "b"}},
		{"quoted postgres array literal", `{"a","b"}`, []string{"a", "b"}},
		{"empty postgres array literal", `{}`, nil},
		{"empty string", ``, nil},
		{"nil", nil, nil},
		{"an unexpected type", 7, nil},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := DecodeStringArray(testCase.raw)
			if !reflect.DeepEqual(actual, testCase.expected) {
				t.Fatalf("expected %#v, got %#v", testCase.expected, actual)
			}
		})
	}
}

// Test_EncodeDecodeStringArrayRoundTrip checks that what is stored is what is
// read back.
func Test_EncodeDecodeStringArrayRoundTrip(t *testing.T) {
	values := []string{"crm", "marketing-tool", "a,b"}

	for _, dbType := range []string{database.TypeSQLite, database.TypeMySQL, database.TypeMSSQL} {
		raw, err := EncodeStringArray(dbType, values)
		if err != nil {
			t.Fatal(err)
		}
		encoded, ok := raw.(string)
		if !ok {
			t.Fatalf("%s: expected the encoding to be text", dbType)
		}
		if decoded := DecodeStringArray(encoded); !reflect.DeepEqual(decoded, values) {
			t.Fatalf("%s: expected %#v, got %#v", dbType, values, decoded)
		}
	}
}
