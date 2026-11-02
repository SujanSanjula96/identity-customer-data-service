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

package service

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
func TestDeletePoliciesOfResourceReadsNoTableWhenOrganizationManagementIsDisabled(t *testing.T) {

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
	for _, table := range []string{"resource_sharing_policy_target", "resource_sharing_policy"} {
		if _, err := testDB.DB.Exec("DROP TABLE " + table); err != nil {
			t.Fatalf("failed to drop %s: %v", table, err)
		}
	}

	if err := DeletePoliciesOfResource(context.Background(), "TEST_RESOURCE", "attr-1"); err != nil {
		t.Errorf("want no error, got %v", err)
	}
}
