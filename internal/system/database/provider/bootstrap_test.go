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

package provider

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/dbscripts"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// openTestSQLite opens an empty inbuilt database in a temporary directory.
func openTestSQLite(t *testing.T) (*sql.DB, string) {

	t.Helper()

	path := filepath.Join(t.TempDir(), "cds.db")
	db, err := sql.Open(database.DriverSQLite, path+"?"+database.DefaultSQLiteOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}

	return db, path
}

// tableCount counts the tables the schema created, ignoring SQLite's own.
func tableCount(t *testing.T, db *sql.DB) int {

	t.Helper()

	var count int
	err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}

	return count
}

func Test_initializeSQLiteSchema(t *testing.T) {

	const expectedTables = 18 // 13 core tables and 5 B2B tables

	db, path := openTestSQLite(t)

	if err := initializeSQLiteSchema(db); err != nil {
		t.Fatal(err)
	}
	if actual := tableCount(t, db); actual != expectedTables {
		t.Fatalf("expected %d tables, got %d", expectedTables, actual)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected the database file to exist: %v", err)
	}

	// The server applies the schema on every start, so a second run must be a
	// no-op rather than an error.
	if err := initializeSQLiteSchema(db); err != nil {
		t.Fatalf("expected applying the schema twice to succeed: %v", err)
	}
	if actual := tableCount(t, db); actual != expectedTables {
		t.Fatalf("expected %d tables after the second run, got %d", expectedTables, actual)
	}
}

// Test_applySchemaStatements covers the fallback for drivers that reject a
// multi-statement script.
func Test_applySchemaStatements(t *testing.T) {

	const expectedTables = 18 // 13 core tables and 5 B2B tables

	db, _ := openTestSQLite(t)

	for run := 1; run <= 2; run++ {
		if err := applySchemaStatements(db, dbscripts.SQLiteSchema); err != nil {
			t.Fatalf("run %d failed: %v", run, err)
		}
		if actual := tableCount(t, db); actual != expectedTables {
			t.Fatalf("run %d: expected %d tables, got %d", run, expectedTables, actual)
		}
	}
}

// Test_initializedSchemaAcceptsWrites checks that the schema is usable: an
// insert must round-trip and the foreign keys must be on.
func Test_initializedSchemaAcceptsWrites(t *testing.T) {

	db, _ := openTestSQLite(t)
	if err := initializeSQLiteSchema(db); err != nil {
		t.Fatal(err)
	}

	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Error("expected foreign key enforcement to be on")
	}

	if _, err := db.Exec(
		`INSERT INTO profiles (profile_id, org_handle) VALUES ('p1', 'org')`); err != nil {
		t.Fatal(err)
	}

	// created_at and updated_at have defaults, so they must be readable as times.
	var createdAt, updatedAt string
	if err := db.QueryRow(
		`SELECT created_at, updated_at FROM profiles WHERE profile_id = 'p1'`).Scan(&createdAt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if createdAt == "" || updatedAt == "" {
		t.Fatalf("expected default timestamps, got %q and %q", createdAt, updatedAt)
	}

	// A reference to a profile that does not exist must be rejected.
	if _, err := db.Exec(
		`INSERT INTO application_data (profile_id, app_id, application_data)
		 VALUES ('missing', 'app', '{}')`); err == nil {
		t.Error("expected the foreign key on application_data.profile_id to be enforced")
	}
}

func Test_stripSQLComments(t *testing.T) {

	stripped := stripSQLComments("-- a comment\nCREATE TABLE t (\n  -- another comment\n  a TEXT\n)")

	if !strings.Contains(stripped, "CREATE TABLE t (") || !strings.Contains(stripped, "a TEXT") {
		t.Fatalf("expected the statement to survive, got %q", stripped)
	}
	if strings.Contains(stripped, "--") {
		t.Fatalf("expected the comments to be removed, got %q", stripped)
	}
}
