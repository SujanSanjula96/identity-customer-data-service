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
	"context"
	"testing"

	adminModel "github.com/wso2/identity-customer-data-service/internal/admin_config/model"
	adminStore "github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

func TestLoadThresholdsUsesThePrimaryOrganizationInASubOrganization(t *testing.T) {

	if err := log.Init("ERROR"); err != nil {
		t.Fatalf("failed to set up the logger: %v", err)
	}
	setup.EnableOrganizationManagement()
	testDB, err := setup.SetupTestSQLite()
	if err != nil {
		t.Fatalf("failed to set up the test database: %v", err)
	}
	provider.SetTestDB(testDB.DB, database.TypeSQLite)
	t.Cleanup(func() {
		provider.SetTestDB(nil, "")
		testDB.Terminate()
	})
	for _, org := range [][]string{
		{"primary-id", "primary", "", "primary-id"},
		{"sub-id", "sub", "primary-id", "primary-id"},
	} {
		if _, err := testDB.DB.Exec(`INSERT INTO organizations (org_id, org_handle, parent_org_id, primary_org_id,
			status) VALUES ($1, $2, $3, $4, 'ACTIVE')`, org[0], org[1], org[2], org[3]); err != nil {
			t.Fatalf("failed to insert the organization %s: %v", org[1], err)
		}
	}
	ctx := context.Background()
	primaryConfig := adminModel.AdminConfig{
		OrgHandle:                  "primary",
		CDSEnabled:                 true,
		SystemApplications:         []string{},
		AutoMergeEnabled:           false,
		AutoMergeThreshold:         0.95,
		ManualReviewThreshold:      0.6,
		DeterministicMatchDecisive: false,
	}
	if err := adminStore.UpdateAdminConfig(ctx, primaryConfig, "primary"); err != nil {
		t.Fatalf("failed to store the configuration of the primary organization: %v", err)
	}

	got := LoadThresholds(ctx, "sub")
	want := Thresholds{AutoMergeEnabled: false, AutoMerge: 0.95, ManualReview: 0.6, DeterministicMatchDecisive: false}
	if got != want {
		t.Errorf("sub organization: got %+v, want %+v", got, want)
	}
	if got := LoadThresholds(ctx, "primary"); got != want {
		t.Errorf("primary organization: got %+v, want %+v", got, want)
	}
}
