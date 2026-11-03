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
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// schemaState builds the catalog view of a database at one step of the migration.
type schemaState struct {
	handles  bool // the org_handle columns exist
	ids      bool // the org_id columns exist
	required bool // org_id is NOT NULL, org_handle is nullable
	idKeys   bool // the keys and indexes on org_id exist
	dropKey  string
}

func (s schemaState) build() orgSchema {

	schema := orgSchema{columns: map[string]map[string]orgSchemaColumn{
		"organizations": {"org_id": {}, "org_handle": {}},
	}}
	for _, column := range database.OrgColumns {
		if schema.columns[column.Table] == nil {
			schema.columns[column.Table] = map[string]orgSchemaColumn{}
		}
		if s.handles {
			schema.columns[column.Table][column.Handle] = orgSchemaColumn{nullable: s.required}
		}
		if s.ids {
			schema.columns[column.Table][column.ID] = orgSchemaColumn{nullable: !(s.required && column.Required)}
		}
	}
	if s.handles {
		primary := !s.idKeys
		schema.indexes = append(schema.indexes, orgSchemaIndex{table: "cds_config", unique: true,
			primary: primary, columns: "org_handle,config"})
	}
	if s.idKeys {
		for _, key := range orgIdKeys {
			if key.table+":"+key.columns == s.dropKey {
				continue
			}
			schema.indexes = append(schema.indexes, orgSchemaIndex{table: key.table, unique: key.unique,
				primary: key.primary, columns: key.columns})
		}
	}
	return schema
}

func TestOrgSchemaProblemsPerPhaseAndStep(t *testing.T) {

	states := map[string]schemaState{
		"legacy":           {handles: true},
		"after step 1":     {handles: true, ids: true},
		"after step 4":     {handles: true, ids: true, required: true, idKeys: true},
		"after step 7":     {ids: true, required: true, idKeys: true},
		"step 4, no index": {handles: true, ids: true, required: true, idKeys: true, dropKey: "profiles:org_id,user_id"},
	}
	// The phases that start on each state, in the order of database.OrgPhases.
	starts := map[string][3]bool{
		"legacy":           {false, false, false},
		"after step 1":     {true, false, false},
		"after step 4":     {true, true, true},
		"after step 7":     {false, false, true},
		"step 4, no index": {true, false, false},
	}
	for name, state := range states {
		for i, phase := range database.OrgPhases {
			problems := orgSchemaProblems(phase, state.build())
			if want := starts[name][i]; (len(problems) == 0) != want {
				t.Errorf("%s, %s: start = %v, want %v; problems:\n%s", name, phase, len(problems) == 0, want,
					strings.Join(problems, "\n"))
			}
		}
	}
}

func TestOrgSchemaProblemsNameTheScript(t *testing.T) {

	problems := orgSchemaProblems(database.OrgPhaseReadOrgId, schemaState{handles: true, ids: true}.build())
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"profiles.org_id is nullable", "004-org-id-switch-constraints-postgres.sql",
		"no valid primary key on cds_config (org_id, config)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in:\n%s", want, joined)
		}
	}

	schema := schemaState{handles: true, ids: true}.build()
	delete(schema.columns, "organizations")
	joined = strings.Join(orgSchemaProblems(database.OrgPhaseDualWrite, schema), "\n")
	if !strings.Contains(joined, "002-organization-management-postgres.sql") {
		t.Errorf("expected the organizations script in:\n%s", joined)
	}
}
