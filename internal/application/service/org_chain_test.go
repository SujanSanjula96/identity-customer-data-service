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
	"reflect"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/organization/model"
	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

func TestOrgChainIsNearestFirst(t *testing.T) {

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
	ctx := context.Background()
	for _, org := range []model.Organization{
		{OrgId: "primary-id", OrgHandle: "acme", PrimaryOrgId: "primary-id", Status: model.StatusActive},
		{OrgId: "r1-id", OrgHandle: "r1", ParentOrgId: "primary-id", PrimaryOrgId: "primary-id",
			Status: model.StatusActive},
		{OrgId: "t1-id", OrgHandle: "t1", ParentOrgId: "r1-id", PrimaryOrgId: "primary-id", Status: model.StatusActive},
	} {
		if err := orgStore.UpsertOrganization(ctx, org); err != nil {
			t.Fatalf("failed to store the org %s: %v", org.OrgHandle, err)
		}
	}

	cases := map[string][]string{
		"t1":      {"t1", "r1", "acme"},
		"r1":      {"r1", "acme"},
		"acme":    {"acme"},
		"unknown": {"unknown"},
	}
	for handle, want := range cases {
		got, err := orgChain(ctx, handle)
		if err != nil {
			t.Fatalf("orgChain(%s): %v", handle, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("orgChain(%s): want %v, got %v", handle, want, got)
		}
	}
}
