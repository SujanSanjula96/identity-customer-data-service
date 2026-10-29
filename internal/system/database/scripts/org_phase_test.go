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

package scripts_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
)

// TestEveryStatementRendersInEveryPhase guards against a misspelt org token: the renderer leaves a
// token it does not know, and the database would refuse the statement on every request.
func TestEveryStatementRendersInEveryPhase(t *testing.T) {

	for name, query := range scripts.AllQueries() {
		for _, text := range []string{query.Query, query.SQLiteQuery} {
			if text == "" {
				continue
			}
			for _, phase := range database.OrgPhases {
				if token := model.UnknownOrgToken(model.RenderOrgTokens(text, phase)); token != "" {
					t.Errorf("%s keeps %s in the %s phase", name, token, phase)
				}
			}
		}
	}
}

// migratedTables are the tables whose org_handle columns step 7 of the migration drops.
var migratedTables = regexp.MustCompile(`\b(profiles|profile_reference|profile_schema|unification_rules|` +
	`applications|consent_categories|cds_config|profile_unification_modes|profile_unification_triggers)\b`)

// handleOfOrganizations is how a statement of the org_id_only phase reads a handle: from the
// organizations table, under the name org_handle.
var handleOfOrganizations = regexp.MustCompile(`o\.org_handle|AS org_handle`)

// TestOrgIdOnlyNamesNoDroppedColumn checks that no statement of the org_id_only phase names an
// org_handle column of the migrated tables, so that step 7 can drop them. The identity resolution
// tables and the organizations table keep their org_handle.
func TestOrgIdOnlyNamesNoDroppedColumn(t *testing.T) {

	for name, query := range scripts.AllQueries() {
		for _, text := range []string{query.Query, query.SQLiteQuery} {
			if text == "" || !migratedTables.MatchString(text) {
				continue
			}
			rendered := model.RenderOrgTokens(text, database.OrgPhaseOrgIdOnly)
			rendered = handleOfOrganizations.ReplaceAllString(rendered, "")
			if strings.Contains(rendered, "org_handle") {
				t.Errorf("%s names org_handle in the org_id_only phase:\n%s", name, rendered)
			}
		}
	}
}

// TestOnlyTheHandlePhasesWriteOrgHandle checks the write side of each phase: dual_write and
// read_org_id write both columns, org_id_only writes org_id only.
func TestOnlyTheHandlePhasesWriteOrgHandle(t *testing.T) {

	insert := scripts.UpdateOrgConfiguration.Query
	for phase, want := range map[database.OrgPhase][]string{
		database.OrgPhaseDualWrite: {"org_handle, org_id", "ON CONFLICT (org_handle, config)"},
		database.OrgPhaseReadOrgId: {"org_id, org_handle", "ON CONFLICT (org_id, config)"},
		database.OrgPhaseOrgIdOnly: {"(org_id, config, value)", "ON CONFLICT (org_id, config)"},
	} {
		rendered := model.RenderOrgTokens(insert, phase)
		for _, part := range want {
			if !strings.Contains(rendered, part) {
				t.Errorf("%s: expected %q in:\n%s", phase, part, rendered)
			}
		}
	}
}
