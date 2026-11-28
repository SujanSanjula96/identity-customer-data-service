/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
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
	"testing"
	"time"

	"github.com/google/uuid"

	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/rows"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
)

// TestOrgID is the org ID that the test identity provider gives a primary organization. Like the
// ID the identity server gives, it is a UUID, which also fits the 36-character org_id columns. It
// derives from the handle, so a handle always has the same ID.
func TestOrgID(handle string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(handle)).String()
}

// TestOrg returns the organization of the handle, as the organization service resolves it with the
// test identity provider: a handle without an organizations row gets a primary organization with the
// ID TestOrgID(handle). Rows that a test stores itself, for example a sub organization, are used as
// they are.
//
// It reads and writes the organizations table through the statements, not the organization service,
// because the organization packages' own tests import this package.
func TestOrg(t testing.TB, handle string) orgModel.Ref {
	t.Helper()
	ref, err := ensureTestOrg(context.Background(), handle)
	if err != nil {
		t.Fatalf("failed to store the organization %s: %v", handle, err)
	}
	return ref
}

// InstallTestIdentityProvider installs an identity provider adapter that needs no identity server: it
// knows every handle as a primary organization with the ID TestOrgID(handle), with no descendants.
func InstallTestIdentityProvider() {
	idp.SetAdapter(testIdentityProvider{})
}

type testIdentityProvider struct{}

func (testIdentityProvider) GetPrimaryOrganization(_ context.Context, primaryHandle string) (idp.Organization,
	error) {
	return idp.Organization{ID: TestOrgID(primaryHandle), Handle: primaryHandle, Name: primaryHandle,
		Status: orgModel.StatusActive}, nil
}

func (testIdentityProvider) ListDescendants(context.Context, string) ([]idp.Organization, error) {
	return nil, nil
}

func (testIdentityProvider) GetOrganization(context.Context, string, string) (*idp.Organization, error) {
	return nil, nil
}

func (testIdentityProvider) IdentityAttributeSourceHandle(_ string, primaryHandle string) string {
	return primaryHandle
}

func ensureTestOrg(ctx context.Context, handle string) (orgModel.Ref, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return orgModel.Ref{}, err
	}
	defer func() { _ = dbClient.Close() }()

	results, err := dbClient.ExecuteQueryContext(ctx, scripts.GetOrganizationByHandle, handle)
	if err != nil {
		return orgModel.Ref{}, err
	}
	if len(results) > 0 {
		return orgModel.Ref{ID: rows.String(results[0], "org_id"), Handle: handle}, nil
	}
	orgID := TestOrgID(handle)
	if _, err := dbClient.ExecuteQueryContext(ctx, scripts.UpsertOrganization, orgID, handle, handle, nil, orgID,
		orgModel.StatusActive, time.Now().UTC()); err != nil {
		return orgModel.Ref{}, err
	}
	return orgModel.Ref{ID: orgID, Handle: handle}, nil
}

// InsertTestOrganization stores a primary organization with the ID and the handle, for a test that
// writes rows keyed by org ID itself. It does nothing when the organization is stored.
func InsertTestOrganization(db *sql.DB, orgID, handle string) error {
	_, err := db.Exec(`INSERT INTO organizations (org_id, org_handle, primary_org_id, status)
		VALUES ($1, $2, $1, 'ACTIVE') ON CONFLICT DO NOTHING`, orgID, handle)
	return err
}
