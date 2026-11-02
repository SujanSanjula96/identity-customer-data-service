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

package store

import (
	"context"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

// The runtime configuration of this test binary is not initialized, so organization management is
// disabled.

func setUpDB(t *testing.T) *setup.SQLiteTestDB {

	t.Helper()
	if err := log.Init("ERROR"); err != nil {
		t.Fatalf("failed to set up the logger: %v", err)
	}
	testDB, err := setup.SetupTestSQLite()
	if err != nil {
		t.Fatalf("failed to set up the test database: %v", err)
	}
	provider.SetTestDB(testDB.DB, database.TypeSQLite)
	t.Cleanup(func() {
		provider.SetTestDB(nil, "")
		testDB.Terminate()
	})
	return testDB
}

func TestLookupsReadNoTableWhenOrganizationManagementIsDisabled(t *testing.T) {

	testDB := setUpDB(t)
	for _, table := range []string{"resource_sharing_policy_target", "resource_sharing_policy", "organizations"} {
		if _, err := testDB.DB.Exec("DROP TABLE " + table); err != nil {
			t.Fatalf("failed to drop %s: %v", table, err)
		}
	}
	ctx := context.Background()

	if org, err := GetOrganizationByHandle(ctx, "acme"); err != nil || org != nil {
		t.Errorf("GetOrganizationByHandle: want nil, nil, got %v, %v", org, err)
	}
	if org, err := GetOrganizationById(ctx, "primary-id"); err != nil || org != nil {
		t.Errorf("GetOrganizationById: want nil, nil, got %v, %v", org, err)
	}
	if chain, err := GetOrganizationChain(ctx, "primary-id"); err != nil || len(chain) != 0 {
		t.Errorf("GetOrganizationChain: want an empty chain, got %v, %v", chain, err)
	}
}
