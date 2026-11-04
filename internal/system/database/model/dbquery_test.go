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

package model

import (
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

func Test_GetQuery(t *testing.T) {

	t.Run("the SQLite override is used when present", func(t *testing.T) {
		query := DBQuery{ID: "CDS-TST-01", Query: `SELECT now()`, SQLiteQuery: `SELECT current_timestamp`}
		if statement := query.GetQuery(database.TypeSQLite); statement != `SELECT current_timestamp` {
			t.Fatalf("unexpected SQLite statement: %q", statement)
		}
		if statement := query.GetQuery(database.TypePostgres); statement != `SELECT now()` {
			t.Fatalf("unexpected PostgreSQL statement: %q", statement)
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
// override optional: a statement with no SQLite text must still have none after
// being completed.
func Test_TemplateHelpersPreserveTheFallback(t *testing.T) {

	t.Run("Format leaves an absent override absent", func(t *testing.T) {
		formatted := DBQuery{ID: "CDS-TST-03", Query: `SELECT %s`}.Format("1")
		if formatted.SQLiteQuery != "" {
			t.Fatalf("expected no SQLite statement, got %q", formatted.SQLiteQuery)
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
			ID:          "CDS-TST-04",
			Query:       `SELECT %s FROM t`,
			SQLiteQuery: `SELECT %s FROM t2`,
		}.Format("a")
		if formatted.GetQuery(database.TypePostgres) != `SELECT a FROM t` {
			t.Errorf("unexpected PostgreSQL statement: %q", formatted.GetQuery(database.TypePostgres))
		}
		if formatted.GetQuery(database.TypeSQLite) != `SELECT a FROM t2` {
			t.Errorf("unexpected SQLite statement: %q", formatted.GetQuery(database.TypeSQLite))
		}
	})

	t.Run("Append follows the same rule", func(t *testing.T) {
		appended := DBQuery{ID: "CDS-TST-05", Query: `SELECT 1`}.Append(` FROM t`)
		if appended.SQLiteQuery != "" {
			t.Fatalf("expected no SQLite statement, got %q", appended.SQLiteQuery)
		}

		appended = DBQuery{ID: "CDS-TST-06", Query: `SELECT 1`, SQLiteQuery: `SELECT 2`}.Append(` FROM t`)
		if appended.GetQuery(database.TypeSQLite) != `SELECT 2 FROM t` {
			t.Fatalf("unexpected SQLite statement: %q", appended.GetQuery(database.TypeSQLite))
		}
	})

	t.Run("WithSQL replaces both variants with already-resolved text", func(t *testing.T) {
		resolved := DBQuery{
			ID:          "CDS-TST-07",
			Query:       `SELECT 1`,
			SQLiteQuery: `SELECT 2`,
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
