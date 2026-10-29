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
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// openLegacySQLite opens an inbuilt database with the schema from before the org ID migration and a
// few rows of the organizations acme (stored) and globex (not stored).
func openLegacySQLite(t *testing.T) *sql.DB {

	t.Helper()
	_ = log.Init("ERROR")
	db, _ := openTestSQLite(t)
	legacy, err := os.ReadFile("testdata/legacy-sqlite.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(legacy)); err != nil {
		t.Fatalf("failed to apply the legacy schema: %v", err)
	}
	for _, statement := range []string{
		`INSERT INTO organizations (org_id, org_handle, primary_org_id, status) VALUES ('acme-id', 'acme', 'acme-id', 'ACTIVE')`,
		`INSERT INTO profiles (profile_id, org_handle, user_id) VALUES ('p1', 'acme', 'u1'), ('p2', 'globex', 'u2')`,
		`INSERT INTO profile_reference (profile_id, org_handle, profile_status, reference_profile_org_handle)
			VALUES ('p1', 'acme', 'REFERENCE_PROFILE', 'acme')`,
		`INSERT INTO cds_config (org_handle, config, value) VALUES ('acme', 'cds_enabled', 'true'),
			('globex', 'cds_enabled', 'true')`,
		`INSERT INTO applications (app_id, org_handle, client_id) VALUES ('a1', 'globex', 'c1')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("failed to insert legacy rows: %v\n%s", err, statement)
		}
	}
	return db
}

func lookupFrom(orgs map[string]string, calls *int) PrimaryOrganizationLookup {

	return func(_ context.Context, handle string) (PrimaryOrganization, bool, error) {
		*calls++
		id, ok := orgs[handle]
		return PrimaryOrganization{Id: id, Name: handle}, ok, nil
	}
}

func withLookup(t *testing.T, lookup PrimaryOrganizationLookup) {

	t.Helper()
	SetPrimaryOrganizationLookup(lookup)
	t.Cleanup(func() { SetPrimaryOrganizationLookup(nil) })
}

func queryString(t *testing.T, db *sql.DB, query string) string {

	t.Helper()
	var value sql.NullString
	if err := db.QueryRow(query).Scan(&value); err != nil {
		t.Fatalf("%v\n%s", err, query)
	}
	return value.String
}

func TestSQLiteMigrationMovesALegacyDatabaseToOrgIds(t *testing.T) {

	db := openLegacySQLite(t)
	calls := 0
	withLookup(t, lookupFrom(map[string]string{"globex": "globex-id"}, &calls))

	if err := initializeSQLiteSchema(db); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	// Only the handle without an organizations row is read from the identity provider.
	if calls != 1 {
		t.Fatalf("expected one identity provider lookup, got %d", calls)
	}

	for query, want := range map[string]string{
		`SELECT org_id FROM profiles WHERE profile_id = 'p1'`:                                "acme-id",
		`SELECT org_id FROM profiles WHERE profile_id = 'p2'`:                                "globex-id",
		`SELECT reference_profile_org_id FROM profile_reference WHERE profile_id = 'p1'`:     "acme-id",
		`SELECT value FROM cds_config WHERE org_id = 'globex-id' AND config = 'cds_enabled'`: "true",
		`SELECT org_id FROM applications WHERE app_id = 'a1'`:                                "globex-id",
		`SELECT org_handle FROM organizations WHERE org_id = 'globex-id'`:                    "globex",
		`PRAGMA user_version`: "1",
	} {
		if got := queryString(t, db, query); got != want {
			t.Errorf("%s = %q; want %q", query, got, want)
		}
	}

	for _, column := range database.OrgColumns {
		if present, _ := sqliteHasColumn(db, column.Table, column.Handle); present {
			t.Errorf("%s.%s is still present", column.Table, column.Handle)
		}
	}
	if got := queryString(t, db, `SELECT count(*) FROM sqlite_master WHERE type = 'index'
		AND name IN ('idx_profiles_org_created_profile', 'idx_profiles_org_id_created_profile')`); got != "1" {
		t.Errorf("expected only the new profiles index, got %s", got)
	}

	// A second start finds nothing to do.
	calls = 0
	if err := initializeSQLiteSchema(db); err != nil || calls != 0 {
		t.Fatalf("second start: %v, %d lookups", err, calls)
	}
}

func TestSQLiteMigrationRefusesAnUnknownHandle(t *testing.T) {

	db := openLegacySQLite(t)
	calls := 0
	withLookup(t, lookupFrom(map[string]string{}, &calls))

	err := initializeSQLiteSchema(db)
	if err == nil || !strings.Contains(err.Error(), "globex") {
		t.Fatalf("expected the unknown handle to stop the start, got %v", err)
	}
	if present, _ := sqliteHasColumn(db, "profiles", "org_handle"); !present {
		t.Fatal("the database changed although the migration stopped")
	}
}

func TestSQLiteMigrationStopsWhenTheIdentityProviderFails(t *testing.T) {

	db := openLegacySQLite(t)
	withLookup(t, func(context.Context, string) (PrimaryOrganization, bool, error) {
		return PrimaryOrganization{}, false, errors.New("connection refused")
	})

	if err := initializeSQLiteSchema(db); err == nil {
		t.Fatal("expected the start to fail")
	}
	if present, _ := sqliteHasColumn(db, "profiles", "org_handle"); !present {
		t.Fatal("the database changed although the migration stopped")
	}
}

func TestSQLiteNewDatabaseGetsTheCurrentVersion(t *testing.T) {

	db, _ := openTestSQLite(t)
	if err := initializeSQLiteSchema(db); err != nil {
		t.Fatal(err)
	}
	if got := queryString(t, db, `PRAGMA user_version`); got != "1" {
		t.Fatalf("user_version = %s; want 1", got)
	}
	if present, _ := sqliteHasColumn(db, "cds_config", "org_id"); !present {
		t.Fatal("expected the org_id column")
	}
}
