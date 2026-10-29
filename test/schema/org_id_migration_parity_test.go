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

package schema

import (
	"regexp"
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// The org ID migration moves an existing database to the install schema in three scripts. A fresh
// install gets dbscripts/postgres.sql; an upgraded database gets these scripts, and the two must
// agree. The integration suite compares the results on a real server (Test_OrgIdMigration); these
// checks run without one.
const (
	orgIdAddColumns  = "../../dbscripts/migrations/003-org-id-add-columns-postgres.sql"
	orgIdConstraints = "../../dbscripts/migrations/004-org-id-switch-constraints-postgres.sql"
	orgIdDropHandles = "../../dbscripts/migrations/005-org-id-drop-org-handle-postgres.sql"
)

// orgIdNames are the indexes and keys on org_id that the install schema names.
var orgIdNames = regexp.MustCompile(`\b(idx_[a-z_]*org_id[a-z_]*|[a-z_]+_org_id_[a-z_]*key)\b`)

// TestStep1AddsEveryOrgIdColumn fails when a column of the migration is missing from step 1.
func TestStep1AddsEveryOrgIdColumn(t *testing.T) {
	script := read(t, orgIdAddColumns)

	for _, column := range database.OrgColumns {
		pattern := regexp.MustCompile(`(?i)ALTER\s+TABLE\s+` + column.Table + `\s+ADD\s+COLUMN\s+IF\s+NOT\s+EXISTS\s+` +
			column.ID + `\s+VARCHAR\(255\);`)
		if !pattern.MatchString(script) {
			t.Errorf("step 1 does not add %s.%s as a nullable column", column.Table, column.ID)
		}
	}
}

// TestStep4CreatesEveryOrgIdIndexOfTheInstallSchema keeps the index and key names of an upgraded
// database the same as those of a fresh install.
func TestStep4CreatesEveryOrgIdIndexOfTheInstallSchema(t *testing.T) {
	install := read(t, installSchema)
	script := read(t, orgIdConstraints)

	names := orgIdNames.FindAllString(install, -1)
	if len(names) == 0 {
		t.Fatal("found no org_id index in the install schema")
	}
	for _, name := range names {
		if !strings.Contains(script, name) {
			t.Errorf("step 4 does not create %s of the install schema", name)
		}
	}
	if !strings.Contains(install, "PRIMARY KEY (org_id, config)") {
		t.Error("the install schema does not key cds_config by (org_id, config)")
	}
}

// TestStep7DropsEveryOrgHandleColumn fails when a column of the migration survives step 7.
func TestStep7DropsEveryOrgHandleColumn(t *testing.T) {
	script := read(t, orgIdDropHandles)

	for _, column := range database.OrgColumns {
		pattern := regexp.MustCompile(`(?i)ALTER\s+TABLE\s+` + column.Table + `\s+DROP\s+COLUMN\s+IF\s+EXISTS\s+` +
			column.Handle + `;`)
		if !pattern.MatchString(script) {
			t.Errorf("step 7 does not drop %s.%s", column.Table, column.Handle)
		}
	}
}

// TestInstallSchemasHaveNoOrgHandleColumn keeps the migrated tables of a fresh install on org_id.
func TestInstallSchemasHaveNoOrgHandleColumn(t *testing.T) {
	for _, path := range []string{installSchema, sqliteSchema} {
		schema := read(t, path)
		for _, table := range database.OrgTables() {
			body := tableBody(schema, table)
			if body == "" {
				t.Errorf("%s has no table %s", path, table)
				continue
			}
			if strings.Contains(body, "org_handle") {
				t.Errorf("%s: %s still has an org_handle column", path, table)
			}
			if !strings.Contains(body, "org_id") {
				t.Errorf("%s: %s has no org_id column", path, table)
			}
		}
	}
}

// tableBody returns the column list of the table's CREATE TABLE statement.
func tableBody(schema, table string) string {
	pattern := regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?` + table + `\s*\((.*?)\n\);`)
	match := pattern.FindStringSubmatch(schema)
	if match == nil {
		return ""
	}
	return match[1]
}
