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

// OrgColumn is a column that the org handle to org ID migration replaces.
type OrgColumn struct {
	Table string
	// Handle is the old column, which holds the org handle.
	Handle string
	// ID is the new column, which holds the org ID.
	ID string
	// Required reports whether the ID column is NOT NULL once the migration's constraints apply.
	Required bool
}

// OrgColumns lists every column of the org handle to org ID migration: 9 tables, 10 columns. The
// organizations table keeps its org_handle: it is the only handle to ID map.
var OrgColumns = []OrgColumn{
	{Table: "profiles", Handle: "org_handle", ID: "org_id", Required: true},
	{Table: "profile_reference", Handle: "org_handle", ID: "org_id", Required: true},
	{Table: "profile_reference", Handle: "reference_profile_org_handle", ID: "reference_profile_org_id"},
	{Table: "profile_schema", Handle: "org_handle", ID: "org_id", Required: true},
	{Table: "unification_rules", Handle: "org_handle", ID: "org_id", Required: true},
	{Table: "applications", Handle: "org_handle", ID: "org_id", Required: true},
	{Table: "consent_categories", Handle: "org_handle", ID: "org_id", Required: true},
	{Table: "cds_config", Handle: "org_handle", ID: "org_id", Required: true},
	{Table: "profile_unification_modes", Handle: "org_handle", ID: "org_id", Required: true},
	{Table: "profile_unification_triggers", Handle: "org_handle", ID: "org_id", Required: true},
}

// OrgTables lists the tables of OrgColumns, each once.
func OrgTables() []string {

	seen := map[string]bool{}
	var tables []string
	for _, column := range OrgColumns {
		if !seen[column.Table] {
			seen[column.Table] = true
			tables = append(tables, column.Table)
		}
	}
	return tables
}
