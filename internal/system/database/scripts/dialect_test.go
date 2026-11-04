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
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

func Test_newQuery(t *testing.T) {

	t.Run("without an override both dialects share the statement", func(t *testing.T) {
		query := newQuery("CDS-TST-01", `SELECT 1`)
		if query.ID != "CDS-TST-01" {
			t.Fatalf("unexpected id: %q", query.ID)
		}
		if statement := query.GetQuery(database.TypePostgres); statement != `SELECT 1` {
			t.Fatalf("unexpected PostgreSQL statement: %q", statement)
		}
		if statement := query.GetQuery(database.TypeSQLite); statement != `SELECT 1` {
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
		query := newQuery("CDS-TST-03", `SELECT 1`, "")
		if statement := query.GetQuery(database.TypeSQLite); statement != `SELECT 1` {
			t.Fatalf("unexpected SQLite statement: %q", statement)
		}
	})

	t.Run("an unrecognised dialect falls back to the base statement", func(t *testing.T) {
		query := newQuery("CDS-TST-04", `SELECT 1`, `SELECT 2`)
		if statement := query.GetQuery("mysql"); statement != `SELECT 1` {
			t.Fatalf("expected the PostgreSQL statement, got %q", statement)
		}
	})
}

func Test_LikeOperator(t *testing.T) {

	if operator := LikeOperator(database.TypeSQLite); operator != "LIKE" {
		t.Errorf("expected LIKE for the inbuilt database, got %q", operator)
	}
	if operator := LikeOperator(database.TypePostgres); operator != "ILIKE" {
		t.Errorf("expected ILIKE for PostgreSQL, got %q", operator)
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

	t.Run("an invalid key is rejected", func(t *testing.T) {
		for _, dbType := range []string{database.TypeSQLite, database.TypePostgres} {
			if _, _, err := JSONEqCondition(dbType, "p.traits", []string{`e"; DROP TABLE profiles; --`}, "x", 1); err == nil {
				t.Errorf("expected an error for %s", dbType)
			}
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

	t.Run("an invalid key is rejected", func(t *testing.T) {
		if _, err := JSONLikeCondition(database.TypeSQLite, "p.traits", []string{"a'b"}, 1); err == nil {
			t.Error("expected an error")
		}
	})
}

func Test_KeysetCondition(t *testing.T) {

	if actual := KeysetCondition(database.TypeSQLite, "<", 2, 3); actual !=
		`(p.created_at, p.profile_id) < ($2, $3)` {
		t.Errorf("unexpected SQLite condition: %q", actual)
	}
	if actual := KeysetCondition(database.TypePostgres, ">", 2, 3); actual !=
		`(p.created_at, p.profile_id) > ($2::timestamptz, $3::text)` {
		t.Errorf("unexpected PostgreSQL condition: %q", actual)
	}
}

func Test_EncodeStringArray(t *testing.T) {

	t.Run("sqlite stores a JSON array", func(t *testing.T) {
		if encoded := EncodeStringArray(database.TypeSQLite, []string{"a", "b"}); encoded != `["a","b"]` {
			t.Errorf("expected a JSON array, got %#v", encoded)
		}
		if encoded := EncodeStringArray(database.TypeSQLite, nil); encoded != `[]` {
			t.Errorf("expected an empty JSON array, got %#v", encoded)
		}
	})

	t.Run("postgres uses the array type", func(t *testing.T) {
		encoded := EncodeStringArray(database.TypePostgres, []string{"a", "b"})
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

	encoded, ok := EncodeStringArray(database.TypeSQLite, values).(string)
	if !ok {
		t.Fatal("expected the SQLite encoding to be text")
	}
	if decoded := DecodeStringArray(encoded); !reflect.DeepEqual(decoded, values) {
		t.Fatalf("expected %#v, got %#v", values, decoded)
	}
}
