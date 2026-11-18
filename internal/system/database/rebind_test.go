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

package database

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func Test_Rebind_LeavesNumberedDatasourcesUnchanged(t *testing.T) {
	sqlText := `SELECT a FROM t WHERE x = $1 AND y = $2 AND z <> $1`
	args := []interface{}{"one", 2}

	for _, dbType := range []string{TypePostgres, TypeSQLite, "", "unknown"} {
		gotSQL, gotArgs, err := Rebind(dbType, sqlText, args)
		if err != nil {
			t.Fatalf("Rebind(%q) failed: %v", dbType, err)
		}
		if gotSQL != sqlText {
			t.Errorf("Rebind(%q) changed the statement: %q", dbType, gotSQL)
		}
		if !reflect.DeepEqual(gotArgs, args) {
			t.Errorf("Rebind(%q) changed the arguments: %#v", dbType, gotArgs)
		}
	}
}

func Test_Rebind_MySQL(t *testing.T) {
	t.Run("each placeholder becomes a question mark", func(t *testing.T) {
		gotSQL, gotArgs, err := Rebind(TypeMySQL, `WHERE a = $1 AND b = $2`, []interface{}{"x", "y"})
		if err != nil {
			t.Fatal(err)
		}
		if gotSQL != `WHERE a = ? AND b = ?` {
			t.Errorf("unexpected statement: %q", gotSQL)
		}
		if !reflect.DeepEqual(gotArgs, []interface{}{"x", "y"}) {
			t.Errorf("unexpected arguments: %#v", gotArgs)
		}
	})

	t.Run("a repeated number repeats its argument", func(t *testing.T) {
		gotSQL, gotArgs, err := Rebind(TypeMySQL,
			`WHERE org_id = $1 AND p != $2 AND org_id <> $1`, []interface{}{"org-1", "p-7"})
		if err != nil {
			t.Fatal(err)
		}
		if gotSQL != `WHERE org_id = ? AND p != ? AND org_id <> ?` {
			t.Errorf("unexpected statement: %q", gotSQL)
		}
		if !reflect.DeepEqual(gotArgs, []interface{}{"org-1", "p-7", "org-1"}) {
			t.Errorf("unexpected arguments: %#v", gotArgs)
		}
	})

	t.Run("arguments follow the order of appearance, not the numbering", func(t *testing.T) {
		gotSQL, gotArgs, err := Rebind(TypeMySQL, `SET b = $2, a = $1`, []interface{}{"first", "second"})
		if err != nil {
			t.Fatal(err)
		}
		if gotSQL != `SET b = ?, a = ?` {
			t.Errorf("unexpected statement: %q", gotSQL)
		}
		if !reflect.DeepEqual(gotArgs, []interface{}{"second", "first"}) {
			t.Errorf("unexpected arguments: %#v", gotArgs)
		}
	})

	t.Run("numbers of ten or more are read whole", func(t *testing.T) {
		args := make([]interface{}, 12)
		for i := range args {
			args[i] = i + 1
		}
		sqlText := `VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

		gotSQL, gotArgs, err := Rebind(TypeMySQL, sqlText, args)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(gotSQL, "?") != 12 || strings.Contains(gotSQL, "$") {
			t.Errorf("unexpected statement: %q", gotSQL)
		}
		if !reflect.DeepEqual(gotArgs, args) {
			t.Errorf("unexpected arguments: %#v", gotArgs)
		}

		reversed, reversedArgs, err := Rebind(TypeMySQL, `VALUES ($10, $9, $8, $7, $6, $5, $4, $3, $2, $1)`,
			[]interface{}{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(reversed, "?") != 10 {
			t.Errorf("unexpected statement: %q", reversed)
		}
		if !reflect.DeepEqual(reversedArgs,
			[]interface{}{"j", "i", "h", "g", "f", "e", "d", "c", "b", "a"}) {
			t.Errorf("$10 was not read as a whole number: %#v", reversedArgs)
		}
	})

	t.Run("a statement without placeholders needs no arguments", func(t *testing.T) {
		gotSQL, gotArgs, err := Rebind(TypeMySQL, `SELECT 1`, nil)
		if err != nil {
			t.Fatal(err)
		}
		if gotSQL != `SELECT 1` || len(gotArgs) != 0 {
			t.Errorf("unexpected result: %q %#v", gotSQL, gotArgs)
		}
	})
}

func Test_Rebind_MSSQL(t *testing.T) {
	t.Run("each placeholder becomes a named parameter", func(t *testing.T) {
		gotSQL, gotArgs, err := Rebind(TypeMSSQL,
			`WHERE org_id = $1 AND p != $2 AND org_id <> $1`, []interface{}{"org-1", "p-7"})
		if err != nil {
			t.Fatal(err)
		}
		if gotSQL != `WHERE org_id = @p1 AND p != @p2 AND org_id <> @p1` {
			t.Errorf("unexpected statement: %q", gotSQL)
		}
		if !reflect.DeepEqual(gotArgs, []interface{}{"org-1", "p-7"}) {
			t.Errorf("the arguments must not change: %#v", gotArgs)
		}
	})

	t.Run("numbers of ten or more are read whole", func(t *testing.T) {
		args := make([]interface{}, 11)
		for i := range args {
			args[i] = i
		}
		gotSQL, _, err := Rebind(TypeMSSQL,
			`SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11`, args)
		if err != nil {
			t.Fatal(err)
		}
		expected := `SELECT @p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10, @p11`
		if gotSQL != expected {
			t.Errorf("expected %q, got %q", expected, gotSQL)
		}
	})
}

func Test_Rebind_SkipsQuotesAndComments(t *testing.T) {
	cases := map[string]string{
		"single quoted literal":    `SELECT 'costs $1' WHERE a = $1`,
		"escaped quote":            `SELECT 'it''s $1 here' WHERE a = $1`,
		"double quoted identifier": `SELECT "col$1" FROM t WHERE a = $1`,
		"backtick identifier":      "SELECT `col$1` FROM t WHERE a = $1",
		"line comment":             "SELECT 1 -- a $1 comment\nWHERE a = $1",
		"block comment":            `SELECT 1 /* a $1 comment */ WHERE a = $1`,
		"json path literal":        `SELECT json_extract(c, '$."name"') WHERE a = $1`,
		"placeholder in format":    `LIMIT $%d WHERE a = $1`,
	}

	for name, sqlText := range cases {
		t.Run(name, func(t *testing.T) {
			mysqlSQL, mysqlArgs, err := Rebind(TypeMySQL, sqlText, []interface{}{"v"})
			if err != nil {
				t.Fatal(err)
			}
			if len(mysqlArgs) != 1 {
				t.Errorf("expected exactly one placeholder to be rewritten, got %d: %q", len(mysqlArgs), mysqlSQL)
			}

			mssqlSQL, _, err := Rebind(TypeMSSQL, sqlText, []interface{}{"v"})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(mssqlSQL, "@p1") != 1 {
				t.Errorf("expected exactly one named parameter, got: %q", mssqlSQL)
			}
		})
	}

	t.Run("quoted text is kept exactly", func(t *testing.T) {
		gotSQL, _, err := Rebind(TypeMySQL, `SELECT 'costs $1', $1`, []interface{}{"v"})
		if err != nil {
			t.Fatal(err)
		}
		if gotSQL != `SELECT 'costs $1', ?` {
			t.Errorf("unexpected statement: %q", gotSQL)
		}
	})

	t.Run("an unterminated quote runs to the end", func(t *testing.T) {
		gotSQL, _, err := Rebind(TypeMySQL, `SELECT 'open $1`, nil)
		if err != nil {
			t.Fatal(err)
		}
		if gotSQL != `SELECT 'open $1` {
			t.Errorf("unexpected statement: %q", gotSQL)
		}
	})
}

func Test_Rebind_RejectsMismatchedArguments(t *testing.T) {
	for _, dbType := range []string{TypeMySQL, TypeMSSQL} {
		t.Run(dbType+" placeholder without an argument", func(t *testing.T) {
			if _, _, err := Rebind(dbType, `WHERE a = $1 AND b = $2`, []interface{}{"x"}); err == nil {
				t.Fatal("expected an error for a missing argument")
			}
		})

		t.Run(dbType+" argument without a placeholder", func(t *testing.T) {
			if _, _, err := Rebind(dbType, `WHERE a = $1`, []interface{}{"x", "unused"}); err == nil {
				t.Fatal("expected an error for an unused argument")
			}
		})

		t.Run(dbType+" placeholder zero", func(t *testing.T) {
			if _, _, err := Rebind(dbType, `WHERE a = $0`, []interface{}{"x"}); err == nil {
				t.Fatal("expected an error for $0")
			}
		})

		t.Run(dbType+" placeholder too large to read", func(t *testing.T) {
			if _, _, err := Rebind(dbType, `WHERE a = $99999999999999999999999`, nil); err == nil {
				t.Fatal("expected an error for an overflowing number")
			}
		})
	}
}

func Test_BindStatement(t *testing.T) {
	t.Run("MySQL rewrites and converts together", func(t *testing.T) {
		moment := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
		gotSQL, gotArgs, err := BindStatement(TypeMySQL, `UPDATE t SET j = $2, at = $3 WHERE id = $1`,
			[]interface{}{"id-1", []byte(`{"a":1}`), moment})
		if err != nil {
			t.Fatal(err)
		}
		if gotSQL != `UPDATE t SET j = ?, at = ? WHERE id = ?` {
			t.Errorf("unexpected statement: %q", gotSQL)
		}
		expected := []interface{}{`{"a":1}`, moment.UTC(), "id-1"}
		if !reflect.DeepEqual(gotArgs, expected) {
			t.Errorf("expected %#v, got %#v", expected, gotArgs)
		}
	})

	t.Run("PostgreSQL is untouched", func(t *testing.T) {
		raw := []byte(`{"a":1}`)
		gotSQL, gotArgs, err := BindStatement(TypePostgres, `UPDATE t SET j = $1`, []interface{}{raw})
		if err != nil {
			t.Fatal(err)
		}
		if gotSQL != `UPDATE t SET j = $1` {
			t.Errorf("unexpected statement: %q", gotSQL)
		}
		if !reflect.DeepEqual(gotArgs, []interface{}{raw}) {
			t.Errorf("the arguments must not change: %#v", gotArgs)
		}
	})

	t.Run("SQLite keeps its byte conversion", func(t *testing.T) {
		_, gotArgs, err := BindStatement(TypeSQLite, `UPDATE t SET j = $1`, []interface{}{[]byte(`{"a":1}`)})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotArgs, []interface{}{`{"a":1}`}) {
			t.Errorf("unexpected arguments: %#v", gotArgs)
		}
	})

	t.Run("an argument mismatch is reported", func(t *testing.T) {
		if _, _, err := BindStatement(TypeMySQL, `SELECT $2`, []interface{}{"only one"}); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func Test_NormalizeArgs_MySQLAndMSSQL(t *testing.T) {
	moment := time.Date(2026, 5, 6, 7, 8, 9, 0, time.FixedZone("x", -7200))

	for _, dbType := range []string{TypeMySQL, TypeMSSQL} {
		t.Run(dbType, func(t *testing.T) {
			got := NormalizeArgs(dbType, []interface{}{
				[]byte(`{"a":1}`), json.RawMessage(`[1]`), moment, "plain", 7, true, nil,
			})
			expected := []interface{}{`{"a":1}`, `[1]`, moment.UTC(), "plain", 7, true, nil}
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("expected %#v, got %#v", expected, got)
			}
			if got[2].(time.Time).Location() != time.UTC {
				t.Errorf("the time must be in UTC")
			}
		})
	}

	t.Run("no arguments", func(t *testing.T) {
		if got := NormalizeArgs(TypeMySQL, nil); len(got) != 0 {
			t.Fatalf("expected no arguments, got %#v", got)
		}
	})

	t.Run("PostgreSQL arguments are returned as given", func(t *testing.T) {
		args := []interface{}{[]byte(`{"a":1}`), moment}
		got := NormalizeArgs(TypePostgres, args)
		if !reflect.DeepEqual(got, args) {
			t.Fatalf("expected %#v, got %#v", args, got)
		}
	})

	t.Run("the input is not modified", func(t *testing.T) {
		args := []interface{}{[]byte(`x`)}
		NormalizeArgs(TypeMySQL, args)
		if _, stillBytes := args[0].([]byte); !stillBytes {
			t.Fatal("the caller's argument list was changed")
		}
	})
}

func Benchmark_Rebind(b *testing.B) {
	sqlText := `SELECT p.profile_id FROM profiles p JOIN profile_reference r ON r.profile_id = p.profile_id
		WHERE p.org_id = $1 AND r.org_id = $1 AND r.profile_status = 'REFERENCE_PROFILE'
		AND p.profile_id != $2 AND ($3 = '' OR COALESCE(p.user_id, '') = '' OR p.user_id = $3)
		AND p.traits IS NOT NULL AND p.identity_attributes IS NOT NULL
		ORDER BY p.created_at, p.profile_id LIMIT $4`
	args := []interface{}{"org-1", "p-7", "user-1", 1}

	for _, dbType := range []string{TypeMySQL, TypeMSSQL} {
		b.Run(dbType, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, _, err := Rebind(dbType, sqlText, args); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func Test_MaxRowsPerStatement(t *testing.T) {
	cases := []struct {
		dbType       string
		paramsPerRow int
		want         int
	}{
		{TypeMSSQL, 4, 525},
		{TypeMSSQL, 13, 161},
		{TypeMSSQL, 1, 1000},
		{TypeMSSQL, 0, 1000},
		{TypePostgres, 4, math.MaxInt},
		{TypeMySQL, 13, math.MaxInt},
		{TypeSQLite, 4, math.MaxInt},
	}
	for _, tc := range cases {
		if got := MaxRowsPerStatement(tc.dbType, tc.paramsPerRow); got != tc.want {
			t.Errorf("MaxRowsPerStatement(%q, %d) = %d, expected %d", tc.dbType, tc.paramsPerRow, got, tc.want)
		}
	}
}
