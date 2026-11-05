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

package setup

import (
	"context"
	"database/sql"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/rows"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	"github.com/wso2/identity-customer-data-service/internal/system/orgkey"
)

// TestOrgId is the org ID that the test resolver stores for a handle that has no organizations row.
func TestOrgId(handle string) string {

	return "org-id-" + handle
}

// InstallTestOrgResolver installs an org key resolver that needs no identity provider: a handle
// without an organizations row gets a primary organization with the ID TestOrgId(handle). Rows
// that a test stores itself, for example a sub organization, are used as they are.
//
// It reads and writes the organizations table through the statements, not the organization store,
// because the store's own tests import this package.
func InstallTestOrgResolver() {

	orgkey.SetResolver(testOrgResolver)
}

func testOrgResolver(ctx context.Context, handle string, store bool) (orgkey.Ref, bool, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return orgkey.Ref{}, false, err
	}
	defer dbClient.Close()

	results, err := dbClient.ExecuteQueryContext(ctx, scripts.GetOrganizationByHandle, handle)
	if err != nil {
		return orgkey.Ref{}, false, err
	}
	if len(results) > 0 {
		return orgkey.Ref{ID: rows.String(results[0], "org_id"), Handle: handle}, true, nil
	}
	if !store {
		return orgkey.Ref{}, false, nil
	}

	orgId := TestOrgId(handle)
	if _, err := dbClient.ExecuteQueryContext(ctx, scripts.UpsertOrganization, orgId, handle, handle, nil, orgId,
		"ACTIVE", time.Now().UTC()); err != nil {
		return orgkey.Ref{}, false, err
	}
	return orgkey.Ref{ID: orgId, Handle: handle}, true, nil
}

// InsertTestOrganization stores a primary organization with the ID and the handle, for a test that
// writes rows keyed by org ID itself. It does nothing when the organization is stored.
func InsertTestOrganization(db *sql.DB, orgId, handle string) error {

	_, err := db.Exec(`INSERT INTO organizations (org_id, org_handle, primary_org_id, status)
		VALUES ($1, $2, $1, 'ACTIVE') ON CONFLICT DO NOTHING`, orgId, handle)
	return err
}
