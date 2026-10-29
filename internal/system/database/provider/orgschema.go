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
	"strings"

	"github.com/lib/pq"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
)

const (
	scriptOrganizations = "dbscripts/migrations/002-organization-management-postgres.sql"
	scriptAddColumns    = "dbscripts/migrations/003-org-id-add-columns-postgres.sql"
	scriptConstraints   = "dbscripts/migrations/004-org-id-switch-constraints-postgres.sql"
)

// orgSchemaColumn is the nullability of one org column.
type orgSchemaColumn struct {
	nullable bool
}

// orgSchemaIndex is a valid index of a table, with its columns in order.
type orgSchemaIndex struct {
	table   string
	name    string
	unique  bool
	primary bool
	columns string
}

// orgSchema is what the catalog reports about the tables of the org ID migration.
type orgSchema struct {
	// columns maps table to column to its state. A table without an org column is absent.
	columns map[string]map[string]orgSchemaColumn
	indexes []orgSchemaIndex
}

// orgSchemaKey is an index that a phase needs. Indexes are matched by table and columns, not by
// name, so that an index which an operator built under another name also counts.
type orgSchemaKey struct {
	table   string
	columns string
	unique  bool
	primary bool
}

// orgIdKeys are the keys and indexes on org_id that the read_org_id and org_id_only phases need.
var orgIdKeys = []orgSchemaKey{
	{table: "profiles", columns: "org_id,created_at,profile_id"},
	{table: "profiles", columns: "org_id,user_id"},
	{table: "profile_reference", columns: "org_id,profile_status,profile_id"},
	{table: "profile_schema", columns: "org_id,scope"},
	{table: "profile_schema", columns: "org_id,attribute_name"},
	{table: "unification_rules", columns: "org_id,is_active,priority"},
	{table: "applications", columns: "org_id,client_id", unique: true},
	{table: "profile_unification_modes", columns: "org_id,merge_type,rule", unique: true},
	{table: "profile_unification_triggers", columns: "org_id", unique: true},
	{table: "cds_config", columns: "org_id,config", unique: true, primary: true},
}

// dualWriteKeys are the keys that the dual_write phase needs: its cds_config upsert names the
// conflict target (org_handle, config).
var dualWriteKeys = []orgSchemaKey{
	{table: "cds_config", columns: "org_handle,config", unique: true},
}

// CheckOrgSchema reports whether the PostgreSQL schema matches the org_id_migration phase. The
// server does not start when it does not: a statement of the phase would fail on every request.
func CheckOrgSchema(ctx context.Context, db *sql.DB, phase database.OrgPhase) error {

	schema, err := readOrgSchema(ctx, db)
	if err != nil {
		return err
	}
	problems := orgSchemaProblems(phase, schema)
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("the database schema does not match org_id_migration.phase %q:\n  - %s", phase,
		strings.Join(problems, "\n  - "))
}

func readOrgSchema(ctx context.Context, db *sql.DB) (orgSchema, error) {

	tables := append(database.OrgTables(), "organizations")
	schema := orgSchema{columns: map[string]map[string]orgSchemaColumn{}}

	columnRows, err := db.QueryContext(ctx, scripts.GetOrgColumns.GetQuery(database.TypePostgres),
		pq.Array(tables))
	if err != nil {
		return schema, fmt.Errorf("failed to read the org columns from the catalog: %v", err)
	}
	defer columnRows.Close()
	for columnRows.Next() {
		var table, column, nullable string
		if err := columnRows.Scan(&table, &column, &nullable); err != nil {
			return schema, err
		}
		if schema.columns[table] == nil {
			schema.columns[table] = map[string]orgSchemaColumn{}
		}
		schema.columns[table][column] = orgSchemaColumn{nullable: nullable == "YES"}
	}
	if err := columnRows.Err(); err != nil {
		return schema, err
	}

	indexRows, err := db.QueryContext(ctx, scripts.GetValidIndexes.GetQuery(database.TypePostgres),
		pq.Array(tables))
	if err != nil {
		return schema, fmt.Errorf("failed to read the indexes from the catalog: %v", err)
	}
	defer indexRows.Close()
	for indexRows.Next() {
		var index orgSchemaIndex
		if err := indexRows.Scan(&index.table, &index.name, &index.unique, &index.primary,
			&index.columns); err != nil {
			return schema, err
		}
		schema.indexes = append(schema.indexes, index)
	}
	return schema, indexRows.Err()
}

// orgSchemaProblems lists every way in which the schema does not match the phase, each with the
// action that fixes it.
func orgSchemaProblems(phase database.OrgPhase, schema orgSchema) []string {

	var problems []string
	if _, ok := schema.columns["organizations"]["org_id"]; !ok {
		problems = append(problems, "the organizations table is missing: run "+scriptOrganizations)
	}

	for _, column := range database.OrgColumns {
		id, hasId := schema.columns[column.Table][column.ID]
		handle, hasHandle := schema.columns[column.Table][column.Handle]
		name := column.Table + "."

		if !hasId {
			problems = append(problems, name+column.ID+" is missing: run "+scriptAddColumns)
			continue
		}
		if phase.WritesOrgHandle() && !hasHandle {
			problems = append(problems, fmt.Sprintf("%s%s is missing, so the database is past step 7 of the "+
				"migration: only the %q phase can run on it", name, column.Handle, database.OrgPhaseOrgIdOnly))
		}
		if !phase.ReadsOrgId() {
			continue
		}
		if column.Required && id.nullable {
			problems = append(problems, name+column.ID+" is nullable: run "+scriptConstraints)
		}
		if hasHandle && !handle.nullable {
			problems = append(problems, name+column.Handle+" is NOT NULL: run "+scriptConstraints)
		}
	}

	keys := dualWriteKeys
	if phase.ReadsOrgId() {
		keys = orgIdKeys
	}
	for _, key := range keys {
		if !schema.hasKey(key) {
			kind := "index"
			switch {
			case key.primary:
				kind = "primary key"
			case key.unique:
				kind = "unique index"
			}
			problems = append(problems, fmt.Sprintf("no valid %s on %s (%s): run %s", kind, key.table,
				strings.ReplaceAll(key.columns, ",", ", "), scriptConstraints))
		}
	}
	return problems
}

func (s orgSchema) hasKey(key orgSchemaKey) bool {

	for _, index := range s.indexes {
		if index.table != key.table || index.columns != key.columns {
			continue
		}
		if key.unique && !index.unique {
			continue
		}
		if key.primary && !index.primary {
			continue
		}
		return true
	}
	return false
}
