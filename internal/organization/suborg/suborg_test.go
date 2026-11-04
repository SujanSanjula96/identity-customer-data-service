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

package suborg

import (
	"context"
	"net/http"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/organization/model"
	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

func setUp(t *testing.T) context.Context {

	t.Helper()
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
		{OrgId: "sub-id", OrgHandle: "sub-handle", ParentOrgId: "primary-id", PrimaryOrgId: "primary-id",
			Status: model.StatusActive},
	} {
		if err := orgStore.UpsertOrganization(ctx, org); err != nil {
			t.Fatalf("failed to store the org %s: %v", org.OrgHandle, err)
		}
	}
	return ctx
}

func TestRejectWrite(t *testing.T) {

	ctx := setUp(t)
	if err := RejectWrite(ctx, "acme"); err != nil {
		t.Fatalf("a primary org must write, got %v", err)
	}
	if err := RejectWrite(ctx, "unknown"); err != nil {
		t.Fatalf("an org that CDS does not know must write, got %v", err)
	}
	err := RejectWrite(ctx, "sub-handle")
	clientErr, ok := err.(*errors.ClientError)
	if !ok {
		t.Fatalf("a sub org must get a client error, got %v", err)
	}
	if clientErr.StatusCode != http.StatusForbidden {
		t.Errorf("status: want %d, got %d", http.StatusForbidden, clientErr.StatusCode)
	}
	if clientErr.ErrorMessage.Code != errors.SUB_ORG_WRITE_NOT_ALLOWED.Code {
		t.Errorf("code: want %s, got %s", errors.SUB_ORG_WRITE_NOT_ALLOWED.Code, clientErr.ErrorMessage.Code)
	}
}
