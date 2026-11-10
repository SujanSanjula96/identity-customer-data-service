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
	"errors"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/admin_config/model"
	"github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

// failingIdP fails to read the primary organization.
type failingIdP struct{}

func (failingIdP) GetPrimaryOrganization(_ context.Context, _ string) (idp.Organization, error) {
	return idp.Organization{}, errors.New("identity provider is not reachable")
}

func (failingIdP) ListDescendants(_ context.Context, _ string) ([]idp.Organization, error) {
	return nil, nil
}

func (failingIdP) GetOrganization(_ context.Context, _, _ string) (*idp.Organization, error) {
	return nil, nil
}

func (failingIdP) IdentityAttributeSourceHandle(orgHandle, _ string) string {
	return orgHandle
}

// An enable after a disable that cannot store the primary organization leaves CDS disabled, so
// the next enable stores it.
func TestEnableKeepsCDSDisabledWhenThePrimaryOrganizationIsNotStored(t *testing.T) {

	if err := log.Init("ERROR"); err != nil {
		t.Fatalf("failed to set up the logger: %v", err)
	}
	setup.EnableOrganizationManagement()
	testDB, err := setup.SetupTestSQLite()
	if err != nil {
		t.Fatalf("failed to set up the test database: %v", err)
	}
	provider.SetTestDB(testDB.DB, database.TypeSQLite)
	idp.SetAdapter(failingIdP{})
	t.Cleanup(func() {
		idp.SetAdapter(nil)
		provider.SetTestDB(nil, "")
		testDB.Terminate()
	})
	ctx := context.Background()
	acme := orgModel.Ref{ID: "acme-id", Handle: "acme"}
	if err := store.UpdateAdminConfig(ctx, model.AdminConfig{
		OrgId:              "acme",
		SystemApplications: []string{},
	}, acme.ID); err != nil {
		t.Fatalf("failed to store the configuration: %v", err)
	}
	if err := store.UpdateInitialSchemaSyncConfig(ctx, true, acme.ID); err != nil {
		t.Fatalf("failed to mark the schema sync done: %v", err)
	}

	err = AdminConfigService{}.UpdateAdminConfig(ctx, &model.AdminConfig{
		OrgId:              "acme",
		CDSEnabled:         true,
		SystemApplications: []string{},
	}, acme)
	if err == nil {
		t.Fatal("UpdateAdminConfig: expected the error of the identity provider, got nil")
	}
	stored, err := store.GetAdminConfig(ctx, acme.ID)
	if err != nil {
		t.Fatalf("GetAdminConfig: %v", err)
	}
	if stored != nil && stored.CDSEnabled {
		t.Errorf("CDS is enabled after a failed enable, want disabled")
	}
}
