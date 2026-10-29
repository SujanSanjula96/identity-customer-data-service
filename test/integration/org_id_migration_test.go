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

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	adminConfigStore "github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	appModel "github.com/wso2/identity-customer-data-service/internal/application/model"
	appStore "github.com/wso2/identity-customer-data-service/internal/application/store"
	consentStore "github.com/wso2/identity-customer-data-service/internal/consent/store"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/orgkey"
	"github.com/wso2/identity-customer-data-service/test/integration/utils"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

// Test_OrgIdMigration drives one PostgreSQL database through the steps of the org handle to org ID
// migration (docs/guides/org-id-migration.md), with the scripts of dbscripts/migrations. Between the
// steps it switches the org_id_migration phase from one store call to the next, the way two servers
// in adjacent phases share the database during a rolling restart, and checks that each one reads
// what the other wrote.
func Test_OrgIdMigration(t *testing.T) {

	if suiteDBType != database.TypePostgres {
		t.Skip("the migration scripts are for PostgreSQL; the inbuilt database migrates at start-up")
	}
	ctx := context.Background()
	db := migrationDatabase(t)

	// A database from before the migration: acme is stored in organizations, as organization
	// management stores an org that uses CDS.
	applyScript(t, db, filepath.Join(utils.GetTestHome(), "test", "setup", "legacy-schema.sql"))
	mustExec(t, db, `INSERT INTO organizations (org_id, org_handle, primary_org_id, status)
		VALUES ('acme-id', 'acme', 'acme-id', 'ACTIVE')`)
	mustExec(t, db, `INSERT INTO cds_config (org_handle, config, value) VALUES ('acme', 'cds_enabled', 'true')`)
	mustExec(t, db, `INSERT INTO profiles (profile_id, org_handle, user_id, location)
		VALUES ('legacy-1', 'acme', 'u-legacy', 'acme/cds/api/v1/profiles/legacy-1')`)
	mustExec(t, db, `INSERT INTO profile_reference (profile_id, org_handle, profile_status, reference_profile_org_handle)
		VALUES ('legacy-1', 'acme', 'REFERENCE_PROFILE', 'acme')`)
	requireSchema(t, db, map[database.OrgPhase]bool{
		database.OrgPhaseDualWrite: false, database.OrgPhaseReadOrgId: false, database.OrgPhaseOrgIdOnly: false})

	// Step 1 and step 2: the columns exist, and dual_write writes both.
	applyMigration(t, db, "003-org-id-add-columns-postgres.sql")
	requireSchema(t, db, map[database.OrgPhase]bool{
		database.OrgPhaseDualWrite: true, database.OrgPhaseReadOrgId: false, database.OrgPhaseOrgIdOnly: false})
	database.SetOrgPhase(database.OrgPhaseDualWrite)
	insertMigrationProfile(t, "dual-1", "globex")
	require.NoError(t, appStore.UpsertApplication(ctx, appModel.Application{AppID: "app-1", OrgHandle: "globex",
		ClientID: "client-1"}))
	require.NoError(t, consentStore.SeedDefaultIdentityDataCategory(ctx, "globex"))
	require.Equal(t, "org-id-globex", queryValue(t, db, `SELECT org_id FROM profiles WHERE profile_id = 'dual-1'`))
	require.Equal(t, "globex", queryValue(t, db, `SELECT org_handle FROM profiles WHERE profile_id = 'dual-1'`))
	require.Equal(t, "org-id-globex", queryValue(t, db,
		`SELECT reference_profile_org_id FROM profile_reference WHERE profile_id = 'dual-1'`))
	require.Equal(t, "org-id-globex", queryValue(t, db, `SELECT org_id FROM applications WHERE app_id = 'app-1'`))
	require.Equal(t, "org-id-globex", queryValue(t, db, `SELECT org_id FROM consent_categories`))
	// The legacy row has no org ID yet: dual_write reads by handle.
	requireProfile(t, "legacy-1", "acme", "acme/cds/api/v1/profiles/legacy-1")

	// Step 3 and step 4: the backfill fills the old rows, then the constraints move to org_id.
	backfill(t, db)
	require.Equal(t, "0", queryValue(t, db, `SELECT count(*) FROM profiles WHERE org_id IS NULL`))
	applyMigration(t, db, "004-org-id-switch-constraints-postgres.sql")
	requireSchema(t, db, map[database.OrgPhase]bool{
		database.OrgPhaseDualWrite: true, database.OrgPhaseReadOrgId: true, database.OrgPhaseOrgIdOnly: true})
	// dual_write still upserts cds_config on (org_handle, config).
	setCDSEnabled(t, "acme", false)
	setCDSEnabled(t, "acme", true)

	// Step 5: dual_write and read_org_id servers read each other's rows.
	database.SetOrgPhase(database.OrgPhaseReadOrgId)
	requireProfile(t, "dual-1", "globex", "globex/cds/api/v1/profiles/dual-1")
	requireProfile(t, "legacy-1", "acme", "acme/cds/api/v1/profiles/legacy-1")
	requireCDSEnabled(t, "acme", true)
	appId, err := appStore.GetAppIdentifierByClientID(ctx, "globex", "client-1")
	require.NoError(t, err)
	require.Equal(t, "app-1", appId)
	insertMigrationProfile(t, "read-1", "acme")
	setCDSEnabled(t, "acme", false)
	database.SetOrgPhase(database.OrgPhaseDualWrite)
	requireProfile(t, "read-1", "acme", "acme/cds/api/v1/profiles/read-1")
	requireCDSEnabled(t, "acme", false)
	setCDSEnabled(t, "acme", true)

	// Step 6: org_id_only rows have no handle; read_org_id servers still read them.
	database.SetOrgPhase(database.OrgPhaseOrgIdOnly)
	insertMigrationProfile(t, "only-1", "acme")
	require.Equal(t, "", queryValue(t, db, `SELECT coalesce(org_handle, '') FROM profiles WHERE profile_id = 'only-1'`))
	require.Equal(t, "", queryValue(t, db, `SELECT coalesce(location, '') FROM profiles WHERE profile_id = 'only-1'`))
	database.SetOrgPhase(database.OrgPhaseReadOrgId)
	requireProfile(t, "only-1", "acme", "acme/cds/api/v1/profiles/only-1")
	requireCDSEnabled(t, "acme", true)

	// A handle that changes in the identity provider keeps the data: it is keyed by org ID, and the
	// responses show the new handle.
	mustExec(t, db, `UPDATE organizations SET org_handle = 'acme-renamed' WHERE org_id = 'acme-id'`)
	orgkey.Invalidate("acme")
	requireProfile(t, "legacy-1", "acme-renamed", "acme-renamed/cds/api/v1/profiles/legacy-1")
	requireCDSEnabled(t, "acme-renamed", true)

	// Step 7: the old columns are dropped. Only org_id_only can start.
	applyMigration(t, db, "005-org-id-drop-org-handle-postgres.sql")
	requireSchema(t, db, map[database.OrgPhase]bool{
		database.OrgPhaseDualWrite: false, database.OrgPhaseReadOrgId: false, database.OrgPhaseOrgIdOnly: true})
	database.SetOrgPhase(database.OrgPhaseOrgIdOnly)
	insertMigrationProfile(t, "only-2", "acme-renamed")
	requireProfile(t, "only-2", "acme-renamed", "acme-renamed/cds/api/v1/profiles/only-2")
	requireProfile(t, "dual-1", "globex", "globex/cds/api/v1/profiles/dual-1")
	setCDSEnabled(t, "acme-renamed", false)
	requireCDSEnabled(t, "acme-renamed", false)
}

// migrationDatabase creates a database of its own for the test and points the stores to it. The
// suite's database and phase come back when the test ends.
func migrationDatabase(t *testing.T) *sql.DB {

	t.Helper()
	name := fmt.Sprintf("cds_org_migration_%d", time.Now().UnixNano())
	mustExec(t, suiteDB, "CREATE DATABASE "+name)

	settings := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		suitePostgres.Host, suitePostgres.Port, suitePostgres.Username, suitePostgres.Password, name)
	db, err := sql.Open("postgres", settings)
	require.NoError(t, err)

	suitePhase := database.OrgPhaseFor(database.TypePostgres)
	provider.SetTestDB(db, database.TypePostgres)
	setup.InstallTestOrgResolver()
	t.Cleanup(func() {
		provider.SetTestDB(suiteDB, suiteDBType)
		database.SetOrgPhase(suitePhase)
		setup.InstallTestOrgResolver()
		_ = db.Close()
		_, _ = suiteDB.Exec("DROP DATABASE IF EXISTS " + name)
	})
	return db
}

func mustExec(t *testing.T, db *sql.DB, statement string, args ...interface{}) {

	t.Helper()
	_, err := db.Exec(statement, args...)
	require.NoError(t, err, statement)
}

func queryValue(t *testing.T, db *sql.DB, query string) string {

	t.Helper()
	var value string
	require.NoError(t, db.QueryRow(query).Scan(&value), query)
	return value
}

// applyScript runs a SQL script one statement at a time, as psql does: a script with CREATE INDEX
// CONCURRENTLY cannot run as one multi-statement call, which is one implicit transaction.
func applyScript(t *testing.T, db *sql.DB, path string) {

	t.Helper()
	script, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, statement := range splitScript(string(script)) {
		_, err := db.Exec(statement)
		require.NoError(t, err, "%s:\n%s", filepath.Base(path), statement)
	}
}

func applyMigration(t *testing.T, db *sql.DB, name string) {

	t.Helper()
	applyScript(t, db, filepath.Join(utils.GetTestHome(), "dbscripts", "migrations", name))
}

// splitScript splits a script into statements at the semicolons that end a line, outside a $$ body.
func splitScript(script string) []string {

	var statements []string
	var current strings.Builder
	inBody := false
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inBody && (trimmed == "" || strings.HasPrefix(trimmed, "--")) {
			continue
		}
		current.WriteString(line)
		current.WriteString("\n")
		if strings.Count(line, "$$")%2 == 1 {
			inBody = !inBody
		}
		if !inBody && strings.HasSuffix(trimmed, ";") {
			statements = append(statements, current.String())
			current.Reset()
		}
	}
	if strings.TrimSpace(current.String()) != "" {
		statements = append(statements, current.String())
	}
	return statements
}

// backfill is step 3 of the migration, which is not part of the server: it fills org_id in the rows
// written before dual_write.
func backfill(t *testing.T, db *sql.DB) {

	t.Helper()
	for _, column := range database.OrgColumns {
		mustExec(t, db, fmt.Sprintf(`UPDATE %[1]s t SET %[2]s = o.org_id FROM organizations o
			WHERE o.org_handle = t.%[3]s AND t.%[2]s IS NULL`, column.Table, column.ID, column.Handle))
	}
}

func requireSchema(t *testing.T, db *sql.DB, starts map[database.OrgPhase]bool) {

	t.Helper()
	for phase, want := range starts {
		err := provider.CheckOrgSchema(context.Background(), db, phase)
		if want {
			require.NoError(t, err, "phase %s", phase)
		} else {
			require.Error(t, err, "phase %s", phase)
		}
	}
}

func insertMigrationProfile(t *testing.T, profileId, org string) {

	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, profileStore.InsertProfile(context.Background(), profileModel.Profile{
		ProfileId: profileId,
		UserId:    "u-" + profileId,
		OrgHandle: org,
		CreatedAt: now,
		UpdatedAt: now,
		Location:  fmt.Sprintf("%s/cds/api/v1/profiles/%s", org, profileId),
		ProfileStatus: &profileModel.ProfileStatus{
			IsReferenceProfile: true,
			ListProfile:        true,
		},
		Traits:             map[string]interface{}{},
		IdentityAttributes: map[string]interface{}{},
	}))
}

func requireProfile(t *testing.T, profileId, org, location string) {

	t.Helper()
	profile, err := profileStore.GetProfileWithOptions(context.Background(), profileId, org,
		profileStore.GetProfileOptions{IncludeApplicationData: false})
	require.NoError(t, err)
	require.NotNil(t, profile, "profile %s of %s in phase %s", profileId, org,
		database.OrgPhaseFor(database.TypePostgres))
	require.Equal(t, org, profile.OrgHandle)
	require.Equal(t, location, profile.Location)
}

func setCDSEnabled(t *testing.T, org string, enabled bool) {

	t.Helper()
	config, err := adminConfigStore.GetAdminConfig(context.Background(), org)
	require.NoError(t, err)
	config.CDSEnabled = enabled
	require.NoError(t, adminConfigStore.UpdateAdminConfig(context.Background(), *config, org))
}

func requireCDSEnabled(t *testing.T, org string, enabled bool) {

	t.Helper()
	config, err := adminConfigStore.GetAdminConfig(context.Background(), org)
	require.NoError(t, err)
	require.Equal(t, enabled, config.CDSEnabled, "cds_enabled of %s in phase %s", org,
		database.OrgPhaseFor(database.TypePostgres))
}

// Test_OrgIdMigrationMatchesTheInstallSchema checks that a database that went through the migration
// scripts has the columns, indexes and constraints of a fresh install, so that later scripts can
// treat both alike. Column order is not compared: a migration adds org_id at the end of a table.
func Test_OrgIdMigrationMatchesTheInstallSchema(t *testing.T) {

	if suiteDBType != database.TypePostgres {
		t.Skip("the migration scripts are for PostgreSQL")
	}
	migrated := migrationDatabase(t)
	applyScript(t, migrated, filepath.Join(utils.GetTestHome(), "test", "setup", "legacy-schema.sql"))
	applyMigration(t, migrated, "003-org-id-add-columns-postgres.sql")
	applyMigration(t, migrated, "004-org-id-switch-constraints-postgres.sql")
	// Each script runs twice, as an operator may run it again after a failure.
	applyMigration(t, migrated, "004-org-id-switch-constraints-postgres.sql")
	applyMigration(t, migrated, "005-org-id-drop-org-handle-postgres.sql")
	applyMigration(t, migrated, "005-org-id-drop-org-handle-postgres.sql")

	fresh := migrationDatabase(t)
	applyScript(t, fresh, filepath.Join(utils.GetTestHome(), "dbscripts", "postgres.sql"))

	for _, query := range []string{
		`SELECT table_name || '.' || column_name || ' ' || is_nullable || ' ' || data_type
			FROM information_schema.columns WHERE table_schema = 'public' ORDER BY 1`,
		`SELECT tablename || ' ' || indexname || ' ' || regexp_replace(indexdef, ' ON public\.', ' ON ')
			FROM pg_indexes WHERE schemaname = 'public' ORDER BY 1`,
		`SELECT conrelid::regclass::text || ' ' || conname || ' ' || contype::text FROM pg_constraint
			WHERE connamespace = 'public'::regnamespace AND contype IN ('p', 'u', 'f') ORDER BY 1`,
	} {
		require.Equal(t, queryLines(t, fresh, query), queryLines(t, migrated, query), query)
	}
}

func queryLines(t *testing.T, db *sql.DB, query string) []string {

	t.Helper()
	rows, err := db.Query(query)
	require.NoError(t, err)
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		lines = append(lines, line)
	}
	require.NoError(t, rows.Err())
	return lines
}
