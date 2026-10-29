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
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// sqliteSchemaVersion is the PRAGMA user_version of an inbuilt database that keys its data by org
// ID. Version 0 is a database from before the org ID migration, or a new file.
const sqliteSchemaVersion = 1

// PrimaryOrganization is a primary organization as the identity provider reports it.
type PrimaryOrganization struct {
	Id   string
	Name string
}

// PrimaryOrganizationLookup reads the primary organization of a handle from the identity provider.
// found is false when the identity provider does not know the handle as a primary organization.
type PrimaryOrganizationLookup func(ctx context.Context, handle string) (org PrimaryOrganization, found bool,
	err error)

var (
	primaryOrgLookupMu sync.RWMutex
	primaryOrgLookup   PrimaryOrganizationLookup
)

// SetPrimaryOrganizationLookup installs the lookup that the inbuilt database migration uses for the
// handles that have no organizations row. The server installs it before the database opens.
func SetPrimaryOrganizationLookup(lookup PrimaryOrganizationLookup) {

	primaryOrgLookupMu.Lock()
	defer primaryOrgLookupMu.Unlock()
	primaryOrgLookup = lookup
}

// sqliteOrgHandleIndexes are the indexes of the inbuilt schema from before the org ID migration. A
// column rename keeps them, under their old names; the current schema creates the same indexes
// under new names.
var sqliteOrgHandleIndexes = []string{
	"idx_profiles_org_created_profile",
	"idx_profiles_org_user",
	"idx_profile_reference_org_status_profile",
	"idx_profile_schema_org_scope",
	"idx_profile_schema_org_attr_name",
	"idx_unification_rules_org_active_priority",
}

// sqliteOrganizationsTable is the organizations table of the inbuilt schema. The migration creates
// it first, because a database from before organization management has none.
const sqliteOrganizationsTable = `CREATE TABLE IF NOT EXISTS organizations
(
    org_id         VARCHAR(255) PRIMARY KEY,
    org_handle     VARCHAR(255) NOT NULL UNIQUE,
    org_name       VARCHAR(255),
    parent_org_id  VARCHAR(255),
    primary_org_id    VARCHAR(255) NOT NULL,
    status         VARCHAR(32)  NOT NULL,
    created_at     TIMESTAMP    NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00'),
    updated_at     TIMESTAMP    NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00')
)`

// sqliteUserVersion returns PRAGMA user_version.
func sqliteUserVersion(db *sql.DB) (int, error) {

	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("failed to read the inbuilt database version: %v", err)
	}
	return version, nil
}

// sqliteHasColumn reports whether the table exists and has the column.
func sqliteHasColumn(db *sql.DB, table, column string) (bool, error) {

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, column).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to read the columns of %s: %v", table, err)
	}
	return count > 0, nil
}

// migrateSQLiteOrgIds moves an inbuilt database from org handles to org IDs: it renames each org
// handle column to its org ID column in place, and replaces each handle with the org ID.
//
// The inbuilt database runs on one node, so no other server reads it during the migration, and all
// the steps of the PostgreSQL migration run here at once. A database that has no org handle columns
// is left unchanged.
//
// Handles that have no organizations row are read from the identity provider first, outside the
// transaction. If the identity provider cannot be reached, or does not know a handle, the
// migration stops and the database stays as it was.
func migrateSQLiteOrgIds(db *sql.DB) error {

	legacy, err := sqliteHasColumn(db, "profiles", "org_handle")
	if err != nil || !legacy {
		return err
	}
	logger := log.GetLogger()
	logger.Info("The inbuilt database keys its data by org handle. Migrating it to org IDs.")

	if _, err := db.Exec(sqliteOrganizationsTable); err != nil {
		return fmt.Errorf("failed to create the organizations table: %v", err)
	}

	var columns []database.OrgColumn
	for _, column := range database.OrgColumns {
		present, err := sqliteHasColumn(db, column.Table, column.Handle)
		if err != nil {
			return err
		}
		if present {
			columns = append(columns, column)
		}
	}

	missing, err := sqliteHandlesWithoutOrganization(db, columns)
	if err != nil {
		return err
	}
	resolved, err := lookupPrimaryOrganizations(missing)
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin the org ID migration of the inbuilt database: %v", err)
	}
	if err := migrateSQLiteOrgIdsInTx(tx, columns, resolved); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed to migrate the inbuilt database to org IDs: %v", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit the org ID migration of the inbuilt database: %v", err)
	}
	logger.Info(fmt.Sprintf("Migrated the inbuilt database to org IDs (%d organizations added).", len(resolved)))
	return nil
}

// sqliteHandlesWithoutOrganization returns the distinct handles of the columns that have no
// organizations row, sorted.
func sqliteHandlesWithoutOrganization(db *sql.DB, columns []database.OrgColumn) ([]string, error) {

	if len(columns) == 0 {
		return nil, nil
	}
	selects := make([]string, len(columns))
	for i, column := range columns {
		selects[i] = fmt.Sprintf("SELECT %s AS handle FROM %s WHERE %s IS NOT NULL", column.Handle, column.Table,
			column.Handle)
	}
	query := "SELECT DISTINCT handle FROM (" + strings.Join(selects, " UNION ") + ") " +
		"WHERE handle NOT IN (SELECT org_handle FROM organizations) ORDER BY handle"
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to read the org handles of the inbuilt database: %v", err)
	}
	defer rows.Close()
	var handles []string
	for rows.Next() {
		var handle string
		if err := rows.Scan(&handle); err != nil {
			return nil, err
		}
		handles = append(handles, handle)
	}
	return handles, rows.Err()
}

// lookupPrimaryOrganizations reads each handle from the identity provider. It fails when a handle is
// not known, so that no row is left without an org ID.
func lookupPrimaryOrganizations(handles []string) (map[string]PrimaryOrganization, error) {

	resolved := map[string]PrimaryOrganization{}
	if len(handles) == 0 {
		return resolved, nil
	}
	primaryOrgLookupMu.RLock()
	lookup := primaryOrgLookup
	primaryOrgLookupMu.RUnlock()
	if lookup == nil {
		return nil, fmt.Errorf("the inbuilt database has org handles without an organization (%s), and no "+
			"identity provider lookup is installed", strings.Join(handles, ", "))
	}

	var unknown []string
	for _, handle := range handles {
		org, found, err := lookup(context.Background(), handle)
		if err != nil {
			return nil, fmt.Errorf("failed to read the organization '%s' from the identity provider: %v", handle,
				err)
		}
		if !found {
			unknown = append(unknown, handle)
			continue
		}
		resolved[handle] = org
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("the identity provider does not know these org handles of the inbuilt database: "+
			"%s. Delete their rows, or restore the organizations, and start the server again",
			strings.Join(unknown, ", "))
	}
	return resolved, nil
}

func migrateSQLiteOrgIdsInTx(tx *sql.Tx, columns []database.OrgColumn,
	resolved map[string]PrimaryOrganization) error {

	for handle, org := range resolved {
		if _, err := tx.Exec(`INSERT INTO organizations (org_id, org_handle, org_name, primary_org_id, status)
			VALUES (?, ?, ?, ?, 'ACTIVE')`, org.Id, handle, org.Name, org.Id); err != nil {
			return fmt.Errorf("failed to store the organization '%s': %v", handle, err)
		}
	}
	for _, column := range columns {
		if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", column.Table, column.Handle,
			column.ID)); err != nil {
			return err
		}
		if _, err := tx.Exec(fmt.Sprintf(`UPDATE %[1]s SET %[2]s =
			(SELECT o.org_id FROM organizations o WHERE o.org_handle = %[1]s.%[2]s) WHERE %[2]s IS NOT NULL`,
			column.Table, column.ID)); err != nil {
			return err
		}
	}
	for _, index := range sqliteOrgHandleIndexes {
		if _, err := tx.Exec("DROP INDEX IF EXISTS " + index); err != nil {
			return err
		}
	}
	return nil
}
