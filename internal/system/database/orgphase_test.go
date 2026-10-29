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

import "testing"

func Test_ResolveOrgPhase(t *testing.T) {

	for value, want := range map[string]OrgPhase{
		"":              OrgPhaseOrgIdOnly,
		"dual_write":    OrgPhaseDualWrite,
		" READ_ORG_ID ": OrgPhaseReadOrgId,
		"org_id_only":   OrgPhaseOrgIdOnly,
	} {
		got, err := ResolveOrgPhase(value)
		if err != nil || got != want {
			t.Errorf("ResolveOrgPhase(%q) = %q, %v; want %q", value, got, err, want)
		}
	}
	if _, err := ResolveOrgPhase("read_id"); err == nil {
		t.Error("expected an unsupported phase to be refused")
	}
}

func Test_OrgPhaseFor(t *testing.T) {

	t.Cleanup(func() { SetOrgPhase(DefaultOrgPhase) })
	for _, phase := range OrgPhases {
		SetOrgPhase(phase)
		if got := OrgPhaseFor(TypePostgres); got != phase {
			t.Errorf("OrgPhaseFor(postgres) = %q; want %q", got, phase)
		}
		if got := OrgPhaseFor(TypeSQLite); got != OrgPhaseOrgIdOnly {
			t.Errorf("OrgPhaseFor(sqlite) = %q; want %q", got, OrgPhaseOrgIdOnly)
		}
	}
}

func Test_OrgPhasePredicates(t *testing.T) {

	for phase, want := range map[OrgPhase][2]bool{
		OrgPhaseDualWrite: {true, false},
		OrgPhaseReadOrgId: {true, true},
		OrgPhaseOrgIdOnly: {false, true},
	} {
		if phase.WritesOrgHandle() != want[0] || phase.ReadsOrgId() != want[1] {
			t.Errorf("%s: WritesOrgHandle=%v ReadsOrgId=%v", phase, phase.WritesOrgHandle(), phase.ReadsOrgId())
		}
	}
}
