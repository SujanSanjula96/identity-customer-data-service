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

func Test_RenderOrgTokens(t *testing.T) {

	statement := `INSERT INTO t (id, {{org_cols}}, {{ref_org_cols}}) VALUES ($1, {{org_val $2}}, {{ref_org_val $3}})
		ON CONFLICT ({{org}}, id) DO UPDATE SET {{org_set}}
		RETURNING {{org_handle_of t}}, p.{{org}}`

	expected := map[database.OrgPhase]string{
		database.OrgPhaseDualWrite: `INSERT INTO t (id, org_handle, org_id, reference_profile_org_handle, reference_profile_org_id) VALUES ($1, $2::VARCHAR, (SELECT org_id FROM organizations WHERE org_handle = $2::VARCHAR), $3::VARCHAR, (SELECT org_id FROM organizations WHERE org_handle = $3::VARCHAR))
		ON CONFLICT (org_handle, id) DO UPDATE SET org_handle = EXCLUDED.org_handle, org_id = EXCLUDED.org_id
		RETURNING t.org_handle, p.org_handle`,
		database.OrgPhaseReadOrgId: `INSERT INTO t (id, org_id, org_handle, reference_profile_org_id, reference_profile_org_handle) VALUES ($1, $2::VARCHAR, (SELECT org_handle FROM organizations WHERE org_id = $2::VARCHAR), $3::VARCHAR, (SELECT org_handle FROM organizations WHERE org_id = $3::VARCHAR))
		ON CONFLICT (org_id, id) DO UPDATE SET org_id = EXCLUDED.org_id, org_handle = EXCLUDED.org_handle
		RETURNING (SELECT o.org_handle FROM organizations o WHERE o.org_id = t.org_id) AS org_handle, p.org_id`,
		database.OrgPhaseOrgIdOnly: `INSERT INTO t (id, org_id, reference_profile_org_id) VALUES ($1, $2, $3)
		ON CONFLICT (org_id, id) DO UPDATE SET org_id = EXCLUDED.org_id
		RETURNING (SELECT o.org_handle FROM organizations o WHERE o.org_id = t.org_id) AS org_handle, p.org_id`,
	}
	for phase, want := range expected {
		t.Run(string(phase), func(t *testing.T) {
			if got := RenderOrgTokens(statement, phase); got != want {
				t.Fatalf("unexpected statement:\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

func Test_RenderOrgTokensKeepsACast(t *testing.T) {

	got := RenderOrgTokens(`SELECT {{org_val $3::VARCHAR}}`, database.OrgPhaseDualWrite)
	want := `SELECT $3::VARCHAR, (SELECT org_id FROM organizations WHERE org_handle = $3::VARCHAR)`
	if got != want {
		t.Fatalf("unexpected statement:\n got: %s\nwant: %s", got, want)
	}
}

func Test_RenderOrgTokensLeavesOtherText(t *testing.T) {

	t.Run("a statement without a token is returned as it is", func(t *testing.T) {
		statement := `SELECT '{}'::jsonb FROM t WHERE org_handle = $1`
		if got := RenderOrgTokens(statement, database.OrgPhaseOrgIdOnly); got != statement {
			t.Fatalf("unexpected statement: %s", got)
		}
	})

	t.Run("an unknown token stays, and UnknownOrgToken finds it", func(t *testing.T) {
		got := RenderOrgTokens(`SELECT {{org_colz}} FROM t`, database.OrgPhaseOrgIdOnly)
		if UnknownOrgToken(got) != "{{org_colz}}" {
			t.Fatalf("expected the unknown token to stay: %s", got)
		}
	})
}

func Test_GetQueryRendersTheOrgTokensOfThePhase(t *testing.T) {

	t.Cleanup(func() { database.SetOrgPhase(database.DefaultOrgPhase) })
	query := DBQuery{ID: "CDS-TST-03", Query: `SELECT 1 FROM t WHERE {{org}} = $1`}

	database.SetOrgPhase(database.OrgPhaseDualWrite)
	if got := query.GetQuery(database.TypePostgres); got != `SELECT 1 FROM t WHERE org_handle = $1` {
		t.Fatalf("unexpected PostgreSQL statement: %s", got)
	}
	// The inbuilt datasource always runs as org_id_only.
	if got := query.GetQuery(database.TypeSQLite); got != `SELECT 1 FROM t WHERE org_id = $1` {
		t.Fatalf("unexpected SQLite statement: %s", got)
	}
}
