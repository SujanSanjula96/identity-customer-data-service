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

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// OrgPhase is the phase of the org handle to org ID migration. It selects which org column the
// statements write, and which one they read, filter and join by.
type OrgPhase string

// Values of the `org_id_migration.phase` configuration, in the order a deployment moves through
// them. Only adjacent phases may run against one database at the same time.
const (
	// OrgPhaseDualWrite writes org_handle and org_id, and reads by org_handle.
	OrgPhaseDualWrite OrgPhase = "dual_write"
	// OrgPhaseReadOrgId writes org_handle and org_id, and reads by org_id.
	OrgPhaseReadOrgId OrgPhase = "read_org_id"
	// OrgPhaseOrgIdOnly writes and reads org_id only. No statement names an org_handle column of
	// the migrated tables, so the columns can be dropped.
	OrgPhaseOrgIdOnly OrgPhase = "org_id_only"
)

// DefaultOrgPhase is the phase of a deployment that does not configure one. A fresh install uses
// the final schema, which has no org_handle columns.
const DefaultOrgPhase = OrgPhaseOrgIdOnly

// OrgPhases lists every value `org_id_migration.phase` accepts, in order.
var OrgPhases = []OrgPhase{OrgPhaseDualWrite, OrgPhaseReadOrgId, OrgPhaseOrgIdOnly}

var postgresOrgPhase atomic.Value

func init() {
	postgresOrgPhase.Store(DefaultOrgPhase)
}

// ResolveOrgPhase normalizes a configured phase. An empty value means DefaultOrgPhase.
func ResolveOrgPhase(value string) (OrgPhase, error) {

	normalized := OrgPhase(strings.ToLower(strings.TrimSpace(value)))
	if normalized == "" {
		return DefaultOrgPhase, nil
	}
	for _, phase := range OrgPhases {
		if normalized == phase {
			return phase, nil
		}
	}
	names := make([]string, len(OrgPhases))
	for i, phase := range OrgPhases {
		names[i] = string(phase)
	}
	return "", fmt.Errorf("unsupported org_id_migration.phase %q: supported phases are %s", value,
		strings.Join(names, ", "))
}

// SetOrgPhase sets the phase of the PostgreSQL datasource. The server sets it once at start-up;
// tests change it to run the statements of two adjacent phases against one database.
func SetOrgPhase(phase OrgPhase) {

	postgresOrgPhase.Store(phase)
}

// OrgPhaseFor returns the phase that the statements of dbType use. The inbuilt datasource
// migrates at start-up and always uses OrgPhaseOrgIdOnly.
func OrgPhaseFor(dbType string) OrgPhase {

	if dbType == TypeSQLite {
		return OrgPhaseOrgIdOnly
	}
	return postgresOrgPhase.Load().(OrgPhase)
}

// WritesOrgHandle reports whether the phase still writes the org_handle columns.
func (p OrgPhase) WritesOrgHandle() bool {

	return p == OrgPhaseDualWrite || p == OrgPhaseReadOrgId
}

// ReadsOrgId reports whether the phase reads, filters and joins by org_id.
func (p OrgPhase) ReadsOrgId() bool {

	return p == OrgPhaseReadOrgId || p == OrgPhaseOrgIdOnly
}
