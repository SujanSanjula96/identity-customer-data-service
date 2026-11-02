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
	"fmt"
	"reflect"
	"testing"
	"time"

	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/sharing/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

// setUpStoredTree prepares a database with the organizations of the test tree. The tree is not
// synced.
func setUpStoredTree(t *testing.T, orgs []orgModel.Organization) context.Context {

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
	ctx := context.Background()
	for _, org := range orgs {
		if err := orgStore.UpsertOrganization(ctx, org); err != nil {
			t.Fatalf("failed to store organization %s: %v", org.OrgId, err)
		}
	}
	return ctx
}

func markSynced(t *testing.T, ctx context.Context, primary orgModel.Organization) {

	t.Helper()
	values := map[string]string{orgModel.ConfigTreeSyncedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := orgStore.SetTreeSyncValues(ctx, primary.OrgHandle, values); err != nil {
		t.Fatalf("failed to mark the tree synced: %v", err)
	}
}

func orgOf(t *testing.T, orgs []orgModel.Organization, orgId string) orgModel.Organization {

	t.Helper()
	for _, o := range orgs {
		if o.OrgId == orgId {
			return o
		}
	}
	t.Fatalf("no organization %s", orgId)
	return orgModel.Organization{}
}

func clientErrorCode(err error) string {

	var clientErr *errors2.ClientError
	if errors.As(err, &clientErr) {
		return clientErr.ErrorMessage.Code
	}
	return ""
}

func selectedOrgs(reach string, ids ...string) model.PolicyRequest {

	req := model.PolicyRequest{ChildOrgs: &model.ChildOrgs{Mode: model.ChildOrgsModeSelected}}
	for _, id := range ids {
		req.ChildOrgs.Orgs = append(req.ChildOrgs.Orgs, model.ChildOrg{OrgId: id, Reach: reach})
	}
	return req
}

var allChildOrgs = model.PolicyRequest{ChildOrgs: &model.ChildOrgs{Mode: model.ChildOrgsModeAll}}

func TestOrganizationAccessNeedsACompletedSync(t *testing.T) {

	tree := testTree()
	ctx := setUpStoredTree(t, tree)
	primary := orgOf(t, tree, "R")

	if _, err := CreateOrgAccess(ctx, primary, allChildOrgs); clientErrorCode(err) !=
		errors2.ORGANIZATIONS_NOT_SYNCED.Code {
		t.Fatalf("expected ORGANIZATIONS_NOT_SYNCED, got %v", err)
	}
	markSynced(t, ctx, primary)
	if _, err := CreateOrgAccess(ctx, primary, allChildOrgs); err != nil {
		t.Fatalf("expected organization access after the sync, got %v", err)
	}
}

func TestOrganizationAccessChecksTheSelectedOrganizations(t *testing.T) {

	tree := testTree()
	ctx := setUpStoredTree(t, tree)
	primary := orgOf(t, tree, "R")
	markSynced(t, ctx, primary)

	for _, ids := range [][]string{{"A1"}, {"unknown"}, {"A", "A1"}} {
		if _, err := CreateOrgAccess(ctx, primary, selectedOrgs(model.ReachOrg, ids...)); clientErrorCode(err) !=
			errors2.SHARE_BAD_REQUEST.Code {
			t.Fatalf("expected SHARE_BAD_REQUEST for %v, got %v", ids, err)
		}
	}
	created, err := CreateOrgAccess(ctx, primary, selectedOrgs(model.ReachOrg, "A", "B"))
	if err != nil {
		t.Fatalf("expected organization access for the direct children, got %v", err)
	}
	if got := len(created.ChildOrgs.Orgs); got != 2 {
		t.Fatalf("expected 2 selected organizations, got %d", got)
	}
}

func TestOrganizationAccessStoresManySelectedOrganizations(t *testing.T) {

	const count = 2500
	primary := orgModel.Organization{OrgId: "R", OrgHandle: "r", PrimaryOrgId: "R", Status: orgModel.StatusActive}
	tree := []orgModel.Organization{primary}
	var ids []string
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("child%04d", i)
		ids = append(ids, id)
		tree = append(tree, orgModel.Organization{OrgId: id, OrgHandle: id, ParentOrgId: "R", PrimaryOrgId: "R",
			Status: orgModel.StatusActive})
	}
	ctx := setUpStoredTree(t, tree)
	markSynced(t, ctx, primary)

	created, err := CreateOrgAccess(ctx, primary, selectedOrgs(model.ReachOrg, ids...))
	if err != nil {
		t.Fatalf("expected organization access for %d organizations, got %v", count, err)
	}
	stored, err := GetOrgAccess(ctx, primary, created.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(stored.ChildOrgs.Orgs); got != count {
		t.Fatalf("expected %d stored organizations, got %d", count, got)
	}
}

func TestIsOrgEnabled(t *testing.T) {

	tree := testTree()
	ctx := setUpStoredTree(t, tree)
	primary := orgOf(t, tree, "R")
	markSynced(t, ctx, primary)
	if _, err := CreateOrgAccess(ctx, primary, selectedOrgs(model.ReachSubtree, "A")); err != nil {
		t.Fatal(err)
	}

	expected := map[string]bool{"R": true, "A": true, "A1": true, "A1x": true, "A2": false, "B": false}
	for id, want := range expected {
		got, err := IsOrgEnabled(ctx, orgOf(t, tree, id))
		if err != nil || got != want {
			t.Errorf("organization %s: expected enabled %v, got %v (%v)", id, want, got, err)
		}
	}
}

func TestGetSharedResources(t *testing.T) {

	tree := testTree()
	ctx := setUpStoredTree(t, tree)
	primary := orgOf(t, tree, "R")
	markSynced(t, ctx, primary)
	if _, err := CreateOrgAccess(ctx, primary, selectedOrgs(model.ReachSubtree, "A")); err != nil {
		t.Fatal(err)
	}
	if _, err := CreatePolicy(ctx, "TEST_RESOURCE", "res-1", primary, allChildOrgs); err != nil {
		t.Fatal(err)
	}
	if _, err := CreatePolicy(ctx, "TEST_RESOURCE", "res-2", primary,
		selectedOrgs(model.ReachOrg, "A")); err != nil {
		t.Fatal(err)
	}
	if _, err := CreatePolicy(ctx, "OTHER_RESOURCE", "res-3", primary, allChildOrgs); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		orgId    string
		expected []model.SharedResource
	}{
		{"A", []model.SharedResource{
			{ResourceId: "res-1", OwnerOrgId: "R", OwnerOrgHandle: "r", OwnerDepth: 1},
			{ResourceId: "res-2", OwnerOrgId: "R", OwnerOrgHandle: "r", OwnerDepth: 1}}},
		{"A1x", []model.SharedResource{{ResourceId: "res-1", OwnerOrgId: "R", OwnerOrgHandle: "r", OwnerDepth: 3}}},
		// The organization access does not include B, and A2 is not active.
		{"B", nil},
		{"A2", nil},
		// A primary organization owns its resources.
		{"R", nil},
	}
	for _, c := range cases {
		got, err := GetSharedResources(ctx, "TEST_RESOURCE", orgOf(t, tree, c.orgId))
		if err != nil || !reflect.DeepEqual(got, c.expected) {
			t.Errorf("organization %s: expected %+v, got %+v (%v)", c.orgId, c.expected, got, err)
		}
	}
	one, err := GetSharedResource(ctx, "TEST_RESOURCE", "res-2", orgOf(t, tree, "A1"))
	if err != nil || one != nil {
		t.Errorf("expected res-2 not to reach A1, got %+v (%v)", one, err)
	}
}
