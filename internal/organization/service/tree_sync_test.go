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
	"net/http"
	"sync"
	"testing"
	"time"

	adminConfigStore "github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	"github.com/wso2/identity-customer-data-service/internal/organization/model"
	"github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

const (
	primaryHandle = "acme"
	primaryId     = "primary-id"
)

// fakeIdP is an identity provider with a fixed tree. onList runs during ListDescendants.
type fakeIdP struct {
	mu        sync.Mutex
	orgs      []idp.Organization
	listErr   error
	onList    func()
	release   chan struct{}
	listCalls int
	getCalls  int
}

func (f *fakeIdP) GetPrimaryOrganization(_ context.Context, handle string) (idp.Organization, error) {
	return idp.Organization{Id: primaryId, Handle: handle, Name: "Acme"}, nil
}

func (f *fakeIdP) ListDescendants(_ context.Context, _ string) ([]idp.Organization, error) {

	f.mu.Lock()
	f.listCalls++
	onList, release, listErr := f.onList, f.release, f.listErr
	orgs := append([]idp.Organization{}, f.orgs...)
	f.mu.Unlock()
	if release != nil {
		<-release
	}
	if onList != nil {
		onList()
	}
	if listErr != nil {
		return nil, listErr
	}
	return orgs, nil
}

func (f *fakeIdP) GetOrganization(_ context.Context, _, orgId string) (*idp.Organization, error) {

	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	for _, o := range f.orgs {
		if o.Id == orgId {
			org := o
			return &org, nil
		}
	}
	return nil, nil
}

func (f *fakeIdP) IdentityAttributeSourceHandle(_, primaryHandle string) string {
	return primaryHandle
}

func (f *fakeIdP) calls() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls, f.getCalls
}

// setUp prepares an empty database, a primary organization with CDS enabled, and the fake
// identity provider.
func setUp(t *testing.T, fake *fakeIdP) context.Context {

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
	idp.SetAdapter(fake)
	t.Cleanup(func() {
		waitIdle(t)
		idp.SetAdapter(nil)
		provider.SetTestDB(nil, "")
		testDB.Terminate()
	})
	if err := setup.InsertTestOrganization(testDB.DB, primaryId, primaryHandle); err != nil {
		t.Fatalf("failed to store the primary organization: %v", err)
	}
	if _, err := testDB.DB.Exec(`INSERT INTO cds_config (org_id, config, value) VALUES (?, 'cds_enabled', 'true')`,
		primaryId); err != nil {
		t.Fatalf("failed to enable CDS: %v", err)
	}
	return context.Background()
}

// waitIdle waits until no background sync runs.
func waitIdle(t *testing.T) {

	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		treeSyncMu.Lock()
		idle := len(treeSyncRuns) == 0
		treeSyncMu.Unlock()
		if idle {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the background sync did not end")
}

func waitState(t *testing.T, ctx context.Context, want string) *model.TreeSyncStatus {

	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := GetTreeSyncStatus(ctx, primaryHandle)
		if err != nil {
			t.Fatalf("failed to read the sync state: %v", err)
		}
		if status.State == want {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the sync state did not become %s", want)
	return nil
}

func tree() []idp.Organization {
	return []idp.Organization{
		{Id: "region1", Handle: "region1", Name: "Region1", ParentId: primaryId, Status: model.StatusActive},
		{Id: "teama", Handle: "teama", Name: "TeamA", ParentId: "region1", Status: model.StatusActive},
		{Id: "region2", Handle: "region2", Name: "Region2", ParentId: primaryId, Status: model.StatusActive},
	}
}

func orgStatus(t *testing.T, ctx context.Context, orgId string) string {

	t.Helper()
	org, err := store.GetOrganizationById(ctx, orgId)
	if err != nil {
		t.Fatalf("failed to read organization %s: %v", orgId, err)
	}
	if org == nil {
		return ""
	}
	return org.Status
}

func clientErrorCode(err error) string {

	var clientErr *errors2.ClientError
	if errors.As(err, &clientErr) {
		return clientErr.ErrorMessage.Code
	}
	return ""
}

func TestEnsurePrimaryStoresOnlyThePrimary(t *testing.T) {

	fake := &fakeIdP{orgs: tree()}
	ctx := setUp(t, fake)

	primary, err := EnsurePrimary(ctx, primaryHandle)
	if err != nil {
		t.Fatalf("EnsurePrimary failed: %v", err)
	}
	if !primary.IsPrimary() || primary.OrgHandle != primaryHandle {
		t.Fatalf("unexpected primary org: %+v", primary)
	}
	orgs, err := store.GetOrganizationsByPrimaryOrg(ctx, primaryId)
	if err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 1 {
		t.Fatalf("expected only the primary org, got %d rows", len(orgs))
	}
	if list, _ := fake.calls(); list != 0 {
		t.Fatalf("EnsurePrimary must not read the tree, but it listed %d times", list)
	}
}

func TestStartTreeSyncStoresTheTreeInTheBackground(t *testing.T) {

	fake := &fakeIdP{orgs: tree(), release: make(chan struct{})}
	ctx := setUp(t, fake)

	status, err := StartTreeSync(ctx, primaryHandle)
	if err != nil {
		t.Fatalf("StartTreeSync failed: %v", err)
	}
	if status.State != model.TreeSyncSyncing || status.StartedAt == nil {
		t.Fatalf("expected SYNCING with a start time, got %+v", status)
	}
	if _, err := StartTreeSync(ctx, primaryHandle); clientErrorCode(err) != errors2.TREE_SYNC_RUNNING.Code {
		t.Fatalf("expected TREE_SYNC_RUNNING while a sync runs, got %v", err)
	}
	close(fake.release)

	status = waitState(t, ctx, model.TreeSyncSynced)
	if status.LastSyncedAt == nil || status.FinishedAt == nil {
		t.Fatalf("unexpected end state: %+v", status)
	}
	for _, id := range []string{"region1", "teama", "region2"} {
		if got := orgStatus(t, ctx, id); got != model.StatusActive {
			t.Fatalf("expected %s to be ACTIVE, got %q", id, got)
		}
	}

	// A new sync can start after a sync completes.
	waitIdle(t)
	if _, err := StartTreeSync(ctx, primaryHandle); err != nil {
		t.Fatalf("a re-sync failed: %v", err)
	}
	waitState(t, ctx, model.TreeSyncSynced)
}

func TestFailedSyncKeepsTheStoredOrgs(t *testing.T) {

	fake := &fakeIdP{orgs: tree()}
	ctx := setUp(t, fake)
	if _, err := StartTreeSync(ctx, primaryHandle); err != nil {
		t.Fatal(err)
	}
	synced := waitState(t, ctx, model.TreeSyncSynced)
	waitIdle(t)

	fake.mu.Lock()
	fake.orgs = nil
	fake.listErr = errors.New("page 3 failed")
	fake.mu.Unlock()
	if _, err := StartTreeSync(ctx, primaryHandle); err != nil {
		t.Fatal(err)
	}
	status := waitState(t, ctx, model.TreeSyncFailed)
	if status.Error == "" {
		t.Fatal("expected the error in the state")
	}
	if status.LastSyncedAt == nil || !status.LastSyncedAt.Equal(*synced.LastSyncedAt) {
		t.Fatalf("a failed sync must keep the time of the last completed sync: %+v", status)
	}
	for _, id := range []string{"region1", "teama", "region2"} {
		if got := orgStatus(t, ctx, id); got != model.StatusActive {
			t.Fatalf("a failed sync must not delete %s, got %q", id, got)
		}
	}
}

func TestSyncMarksMissingOrgsDeleted(t *testing.T) {

	fake := &fakeIdP{orgs: tree()}
	ctx := setUp(t, fake)
	if _, err := syncTree(ctx, primaryHandle); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.orgs = fake.orgs[:2]
	fake.mu.Unlock()

	result, err := syncTree(ctx, primaryHandle)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Deleted) != 1 || result.Deleted[0] != "region2" {
		t.Fatalf("expected region2 deleted, got %+v", result.Deleted)
	}
	if got := orgStatus(t, ctx, "region2"); got != model.StatusDeleted {
		t.Fatalf("expected region2 DELETED, got %q", got)
	}
}

func TestSyncDoesNotDeleteOrgsThatAnEventChanged(t *testing.T) {

	fake := &fakeIdP{orgs: tree()}
	ctx := setUp(t, fake)
	if _, err := syncTree(ctx, primaryHandle); err != nil {
		t.Fatal(err)
	}
	// The source tree of the next sync has no region2 and no teamb. During the sync, an event
	// stores teamb and updates region2.
	fake.mu.Lock()
	fake.orgs = fake.orgs[:2]
	fake.onList = func() {
		time.Sleep(5 * time.Millisecond)
		for _, org := range []model.Organization{
			{OrgId: "teamb", OrgHandle: "teamb", ParentOrgId: "region1", PrimaryOrgId: primaryId,
				Status: model.StatusActive},
			{OrgId: "region2", OrgHandle: "region2", ParentOrgId: primaryId, PrimaryOrgId: primaryId,
				Status: model.StatusActive},
		} {
			if err := store.UpsertOrganization(ctx, org); err != nil {
				t.Errorf("failed to store %s: %v", org.OrgId, err)
			}
		}
	}
	fake.mu.Unlock()

	result, err := syncTree(ctx, primaryHandle)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Deleted) != 0 {
		t.Fatalf("expected no deletes, got %+v", result.Deleted)
	}
	for _, id := range []string{"teamb", "region2"} {
		if got := orgStatus(t, ctx, id); got != model.StatusActive {
			t.Fatalf("expected %s ACTIVE, got %q", id, got)
		}
	}
}

func TestEventsAreIgnoredBeforeTheFirstSync(t *testing.T) {

	fake := &fakeIdP{orgs: tree()}
	ctx := setUp(t, fake)
	if _, err := EnsurePrimary(ctx, primaryHandle); err != nil {
		t.Fatal(err)
	}

	err := HandleSyncEvent(ctx, primaryHandle, model.SyncEvent{Event: model.EventOrgCreated, OrgId: "region1"})
	if err != nil {
		t.Fatalf("expected the event to be ignored, got %v", err)
	}
	if got := orgStatus(t, ctx, "region1"); got != "" {
		t.Fatalf("expected no row for region1, got %q", got)
	}
	if _, get := fake.calls(); get != 0 {
		t.Fatalf("an ignored event must not call the IdP, got %d calls", get)
	}
	if err := HandleSyncEvent(ctx, primaryHandle, model.SyncEvent{Event: "ORG_MOVED", OrgId: "region1"}); err == nil {
		t.Fatal("expected an error for an unsupported event")
	}
}

func TestEventsApplyAfterTheFirstSync(t *testing.T) {

	fake := &fakeIdP{orgs: tree()}
	ctx := setUp(t, fake)
	if _, err := StartTreeSync(ctx, primaryHandle); err != nil {
		t.Fatal(err)
	}
	waitState(t, ctx, model.TreeSyncSynced)
	waitIdle(t)

	// A known parent: the event stores the organization.
	fake.mu.Lock()
	fake.orgs = append(fake.orgs, idp.Organization{Id: "teamb", Handle: "teamb", ParentId: "region1",
		Status: model.StatusActive})
	fake.mu.Unlock()
	listBefore, _ := fake.calls()
	if err := HandleSyncEvent(ctx, primaryHandle, model.SyncEvent{Event: model.EventOrgCreated,
		OrgId: "teamb"}); err != nil {
		t.Fatal(err)
	}
	if got := orgStatus(t, ctx, "teamb"); got != model.StatusActive {
		t.Fatalf("expected teamb ACTIVE, got %q", got)
	}
	if list, _ := fake.calls(); list != listBefore {
		t.Fatal("an event with a known parent must not read the tree")
	}

	// An unknown parent: the event is ignored, and no sync starts.
	fake.mu.Lock()
	fake.orgs = append(fake.orgs,
		idp.Organization{Id: "region3", Handle: "region3", ParentId: primaryId, Status: model.StatusActive},
		idp.Organization{Id: "teamc", Handle: "teamc", ParentId: "region3", Status: model.StatusActive})
	fake.mu.Unlock()
	if err := HandleSyncEvent(ctx, primaryHandle, model.SyncEvent{Event: model.EventOrgCreated,
		OrgId: "teamc"}); err != nil {
		t.Fatal(err)
	}
	waitIdle(t)
	if got := orgStatus(t, ctx, "teamc"); got != "" {
		t.Fatalf("expected no row for teamc, got %q", got)
	}
	if list, _ := fake.calls(); list != listBefore {
		t.Fatal("an event with an unknown parent must not read the tree")
	}

	// The next tree sync adds the organization.
	if _, err := StartTreeSync(ctx, primaryHandle); err != nil {
		t.Fatal(err)
	}
	waitState(t, ctx, model.TreeSyncSynced)
	if got := orgStatus(t, ctx, "teamc"); got != model.StatusActive {
		t.Fatalf("expected teamc ACTIVE after the tree sync, got %q", got)
	}
}

func TestDeleteEventAppliesOnlyToSubOrgsOfThePathTree(t *testing.T) {

	ctx := setUp(t, &fakeIdP{orgs: tree()})
	if _, err := StartTreeSync(ctx, primaryHandle); err != nil {
		t.Fatal(err)
	}
	waitState(t, ctx, model.TreeSyncSynced)
	waitIdle(t)
	for _, org := range []model.Organization{
		{OrgId: "other-primary", OrgHandle: "other.com", PrimaryOrgId: "other-primary", Status: model.StatusActive},
		{OrgId: "other-team", OrgHandle: "other-team", ParentOrgId: "other-primary", PrimaryOrgId: "other-primary",
			Status: model.StatusActive},
	} {
		if err := store.UpsertOrganization(ctx, org); err != nil {
			t.Fatal(err)
		}
	}

	for _, orgId := range []string{"other-team", "other-primary", primaryId} {
		if err := HandleSyncEvent(ctx, primaryHandle, model.SyncEvent{Event: model.EventOrgDeleted,
			OrgId: orgId}); err != nil {
			t.Fatal(err)
		}
		if got := orgStatus(t, ctx, orgId); got != model.StatusActive {
			t.Errorf("a delete event on the path of %s must not change %s, got %q", primaryHandle, orgId, got)
		}
	}
	if err := HandleSyncEvent(ctx, primaryHandle, model.SyncEvent{Event: model.EventOrgDeleted,
		OrgId: "teama"}); err != nil {
		t.Fatal(err)
	}
	if got := orgStatus(t, ctx, "teama"); got != model.StatusDeleted {
		t.Fatalf("expected teama DELETED, got %q", got)
	}
}

// requestSyncAgain requests one more run of the running sync.
func requestSyncAgain() {

	treeSyncMu.Lock()
	defer treeSyncMu.Unlock()
	scheduleLocked(primaryHandle)
}

func TestATriggerDuringASyncRunsItAgain(t *testing.T) {

	fake := &fakeIdP{orgs: tree(), release: make(chan struct{})}
	ctx := setUp(t, fake)
	if _, err := StartTreeSync(ctx, primaryHandle); err != nil {
		t.Fatal(err)
	}
	// Two triggers during the run ask for one more run.
	for i := 0; i < 2; i++ {
		requestSyncAgain()
	}
	close(fake.release)
	waitIdle(t)
	if list, _ := fake.calls(); list != 2 {
		t.Fatalf("expected 2 tree reads, got %d", list)
	}
	waitState(t, ctx, model.TreeSyncSynced)
}

func TestUnknownOrgsAreNotLookedUp(t *testing.T) {

	fake := &fakeIdP{orgs: tree()}
	ctx := setUp(t, fake)
	if _, err := EnsurePrimary(ctx, primaryHandle); err != nil {
		t.Fatal(err)
	}

	var notFound *errors2.ClientError
	if _, err := SubOrgHandleOf(ctx, primaryHandle, "region1"); !errors.As(err, &notFound) ||
		notFound.StatusCode != http.StatusNotFound {
		t.Fatalf("expected an unknown org ID to be not found, got %v", err)
	}
	if isSub, err := IsSubOrgHandle(ctx, "region1"); err != nil || isSub {
		t.Fatal("an unknown handle is not a sub org")
	}
	if _, known := PrimaryHandleOf(ctx, "region1"); known {
		t.Fatal("an unknown handle has no primary org")
	}
	if list, get := fake.calls(); list != 0 || get != 0 {
		t.Fatalf("expected no IdP calls, got %d lists and %d reads", list, get)
	}
}

// setCDSEnabled sets the CDS enabled flag of the primary organization.
func setCDSEnabled(t *testing.T, ctx context.Context, enabled bool) {

	t.Helper()
	config, err := adminConfigStore.GetAdminConfig(ctx, primaryHandle)
	if err != nil || config == nil {
		t.Fatalf("failed to read the config: %v", err)
	}
	config.CDSEnabled = enabled
	if err := adminConfigStore.UpdateAdminConfig(ctx, *config, primaryHandle); err != nil {
		t.Fatalf("failed to update the config: %v", err)
	}
}

func TestResetTreeSyncNeedsANewSync(t *testing.T) {

	fake := &fakeIdP{orgs: tree()}
	ctx := setUp(t, fake)
	if _, err := StartTreeSync(ctx, primaryHandle); err != nil {
		t.Fatal(err)
	}
	waitState(t, ctx, model.TreeSyncSynced)
	waitIdle(t)

	setCDSEnabled(t, ctx, false)
	if err := ResetTreeSync(ctx, primaryHandle); err != nil {
		t.Fatalf("ResetTreeSync failed: %v", err)
	}
	status, err := GetTreeSyncStatus(ctx, primaryHandle)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != model.TreeSyncNotSynced || status.LastSyncedAt != nil ||
		status.StartedAt != nil || status.FinishedAt != nil || status.Error != "" {
		t.Fatalf("expected a cleared sync state, got %+v", status)
	}
	// The stored organizations stay. The next sync updates them.
	if got := orgStatus(t, ctx, "region1"); got != model.StatusActive {
		t.Fatalf("expected region1 to stay ACTIVE, got %q", got)
	}

	setCDSEnabled(t, ctx, true)
	// Organization events are ignored until the next sync.
	fake.mu.Lock()
	fake.orgs = append(fake.orgs, idp.Organization{Id: "region3", Handle: "region3", Name: "Region3",
		ParentId: primaryId, Status: model.StatusActive})
	fake.mu.Unlock()
	event := model.SyncEvent{Event: model.EventOrgCreated, OrgId: "region3"}
	if err := HandleSyncEvent(ctx, primaryHandle, event); err != nil {
		t.Fatal(err)
	}
	if got := orgStatus(t, ctx, "region3"); got != "" {
		t.Fatalf("expected the event to be ignored before the sync, got %q", got)
	}
	if _, err := StartTreeSync(ctx, primaryHandle); err != nil {
		t.Fatal(err)
	}
	waitState(t, ctx, model.TreeSyncSynced)
	if got := orgStatus(t, ctx, "region3"); got != model.StatusActive {
		t.Fatalf("expected region3 ACTIVE after the sync, got %q", got)
	}
}

func TestASyncThatEndsAfterADisableStoresNoState(t *testing.T) {

	fake := &fakeIdP{orgs: tree(), release: make(chan struct{})}
	ctx := setUp(t, fake)
	if _, err := StartTreeSync(ctx, primaryHandle); err != nil {
		t.Fatal(err)
	}
	// A trigger during the run asks for one more run, but the disable cancels it.
	requestSyncAgain()
	setCDSEnabled(t, ctx, false)
	if err := ResetTreeSync(ctx, primaryHandle); err != nil {
		t.Fatal(err)
	}
	close(fake.release)
	waitIdle(t)

	status, err := GetTreeSyncStatus(ctx, primaryHandle)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != model.TreeSyncNotSynced || status.LastSyncedAt != nil {
		t.Fatalf("expected NOT_SYNCED after a disable during the sync, got %+v", status)
	}
	if list, _ := fake.calls(); list != 1 {
		t.Fatalf("expected 1 tree read, got %d", list)
	}
}

func TestResetTreeSyncWithoutASyncWritesNothing(t *testing.T) {

	fake := &fakeIdP{orgs: tree()}
	ctx := setUp(t, fake)
	if err := ResetTreeSync(ctx, primaryHandle); err != nil {
		t.Fatal(err)
	}
	status, err := GetTreeSyncStatus(ctx, primaryHandle)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != model.TreeSyncNotSynced {
		t.Fatalf("expected NOT_SYNCED, got %+v", status)
	}
}
