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

package model

import (
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

const selectOne = `SELECT 1`

func Test_GetQuery(t *testing.T) {
	t.Run("the SQLite override is used when present", func(t *testing.T) {
		query := DBQuery{
			ID:        "CDS-TST-01",
			Query:     `SELECT now()`,
			Overrides: map[string]string{database.TypeSQLite: `SELECT current_timestamp`},
		}
		if statement := query.GetQuery(database.TypeSQLite); statement != `SELECT current_timestamp` {
			t.Fatalf("unexpected SQLite statement: %q", statement)
		}
		if statement := query.GetQuery(database.TypePostgres); statement != `SELECT now()` {
			t.Fatalf("unexpected PostgreSQL statement: %q", statement)
		}
	})

	t.Run("each datasource gets its own override", func(t *testing.T) {
		query := DBQuery{
			ID:    "CDS-TST-08",
			Query: selectOne,
			Overrides: map[string]string{
				database.TypeSQLite: `SELECT 2`,
				database.TypeMySQL:  `SELECT 3`,
				database.TypeMSSQL:  `SELECT 4`,
			},
		}
		expected := map[string]string{
			database.TypePostgres: selectOne,
			database.TypeSQLite:   `SELECT 2`,
			database.TypeMySQL:    `SELECT 3`,
			database.TypeMSSQL:    `SELECT 4`,
			"unknown":             selectOne,
		}
		for dbType, want := range expected {
			if got := query.GetQuery(dbType); got != want {
				t.Errorf("GetQuery(%q) = %q, expected %q", dbType, got, want)
			}
		}
	})

	t.Run("an empty override falls back to the base statement", func(t *testing.T) {
		query := DBQuery{
			ID:        "CDS-TST-09",
			Query:     selectOne,
			Overrides: map[string]string{database.TypeSQLite: ""},
		}
		if statement := query.GetQuery(database.TypeSQLite); statement != selectOne {
			t.Fatalf("an empty override must not be used, got %q", statement)
		}
	})

	t.Run("a populated query never resolves to an empty statement", func(t *testing.T) {
		query := DBQuery{ID: "CDS-TST-02", Query: `SELECT 1`}
		for _, dbType := range []string{database.TypePostgres, database.TypeSQLite, "mysql", ""} {
			if statement := query.GetQuery(dbType); statement != `SELECT 1` {
				t.Errorf("GetQuery(%q) = %q, expected the base statement", dbType, statement)
			}
		}
	})
}

// Test_TemplateHelpersPreserveTheFallback covers the invariant that makes the
// override optional: a statement with no override must still have none after
// being completed.
func Test_TemplateHelpersPreserveTheFallback(t *testing.T) {
	t.Run("Format leaves an absent override absent", func(t *testing.T) {
		formatted := DBQuery{ID: "CDS-TST-03", Query: `SELECT %s`}.Format("1")
		if len(formatted.Overrides) != 0 {
			t.Fatalf("expected no overrides, got %v", formatted.Overrides)
		}
		if formatted.GetQuery(database.TypeSQLite) != `SELECT 1` {
			t.Fatalf("unexpected SQLite statement: %q", formatted.GetQuery(database.TypeSQLite))
		}
		if formatted.ID != "CDS-TST-03" {
			t.Fatalf("the id was not carried over: %q", formatted.ID)
		}
	})

	t.Run("Format applies to both variants when an override exists", func(t *testing.T) {
		formatted := DBQuery{
			ID:        "CDS-TST-04",
			Query:     `SELECT %s FROM t`,
			Overrides: map[string]string{database.TypeSQLite: `SELECT %s FROM t2`},
		}.Format("a")
		if formatted.GetQuery(database.TypePostgres) != `SELECT a FROM t` {
			t.Errorf("unexpected PostgreSQL statement: %q", formatted.GetQuery(database.TypePostgres))
		}
		if formatted.GetQuery(database.TypeSQLite) != `SELECT a FROM t2` {
			t.Errorf("unexpected SQLite statement: %q", formatted.GetQuery(database.TypeSQLite))
		}
	})

	t.Run("Format applies to every override", func(t *testing.T) {
		formatted := DBQuery{
			ID:    "CDS-TST-10",
			Query: `SELECT %s FROM t`,
			Overrides: map[string]string{
				database.TypeSQLite: `SELECT %s FROM s`,
				database.TypeMySQL:  `SELECT %s FROM m`,
				database.TypeMSSQL:  `SELECT %s FROM q`,
			},
		}.Format("a")
		expected := map[string]string{
			database.TypePostgres: `SELECT a FROM t`,
			database.TypeSQLite:   `SELECT a FROM s`,
			database.TypeMySQL:    `SELECT a FROM m`,
			database.TypeMSSQL:    `SELECT a FROM q`,
		}
		for dbType, want := range expected {
			if got := formatted.GetQuery(dbType); got != want {
				t.Errorf("GetQuery(%q) = %q, expected %q", dbType, got, want)
			}
		}
	})

	t.Run("Append follows the same rule", func(t *testing.T) {
		appended := DBQuery{ID: "CDS-TST-05", Query: `SELECT 1`}.Append(` FROM t`)
		if len(appended.Overrides) != 0 {
			t.Fatalf("expected no overrides, got %v", appended.Overrides)
		}

		appended = DBQuery{
			ID:        "CDS-TST-06",
			Query:     `SELECT 1`,
			Overrides: map[string]string{database.TypeSQLite: `SELECT 2`},
		}.Append(` FROM t`)
		if appended.GetQuery(database.TypeSQLite) != `SELECT 2 FROM t` {
			t.Fatalf("unexpected SQLite statement: %q", appended.GetQuery(database.TypeSQLite))
		}
	})

	t.Run("Format and Append do not change or share the original", func(t *testing.T) {
		original := DBQuery{
			ID:        "CDS-TST-11",
			Query:     `SELECT %s`,
			Overrides: map[string]string{database.TypeSQLite: `SELECT %s`},
		}
		formatted := original.Format("1")
		formatted.Overrides[database.TypeSQLite] = `changed`

		if original.Overrides[database.TypeSQLite] != `SELECT %s` {
			t.Fatalf("the original override was modified: %q", original.Overrides[database.TypeSQLite])
		}
		if original.Query != `SELECT %s` {
			t.Fatalf("the original statement was modified: %q", original.Query)
		}
	})

	t.Run("WithSQL replaces both variants with already-resolved text", func(t *testing.T) {
		resolved := DBQuery{
			ID:        "CDS-TST-07",
			Query:     `SELECT 1`,
			Overrides: map[string]string{database.TypeSQLite: `SELECT 2`},
		}.WithSQL(`SELECT 3 WHERE a LIKE $1`)

		for _, dbType := range []string{database.TypePostgres, database.TypeSQLite} {
			if statement := resolved.GetQuery(dbType); statement != `SELECT 3 WHERE a LIKE $1` {
				t.Errorf("GetQuery(%q) = %q, expected the assembled statement", dbType, statement)
			}
		}
		if resolved.ID != "CDS-TST-07" {
			t.Fatalf("the id was not carried over: %q", resolved.ID)
		}
	})
}
