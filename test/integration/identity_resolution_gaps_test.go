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

package integration

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/test/setup"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	irModel "github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	irService "github.com/wso2/identity-customer-data-service/internal/identity_resolution/service"
	irStore "github.com/wso2/identity-customer-data-service/internal/identity_resolution/store"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"
)

func newOrg(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// insertBareProfile writes a profile directly, bypassing the service so the test controls
// exactly what exists without waiting on the async unification queue. The org is a handle,
// resolved as a request handler does. The profile is a reference profile, as one the service
// creates is, since a merge locks and checks both profiles as reference profiles.
func insertBareProfile(t *testing.T, org, userID string, traits map[string]interface{}) profileModel.Profile {
	t.Helper()

	ref := setup.TestOrg(t, org)

	profile := profileModel.Profile{
		ProfileID:          uuid.New().String(),
		UserID:             userID,
		OrgID:              ref.ID,
		Traits:             traits,
		IdentityAttributes: map[string]interface{}{},
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
		ProfileStatus:      &profileModel.ProfileStatus{IsReferenceProfile: true},
	}
	if err := profileStore.InsertProfile(context.Background(), ref.ID, profile); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	return profile
}

// Test_MergeMatchedProfiles_ReturnsSurvivingMaster covers the three merge shapes. The
// surviving master is not always the profile passed in first: a permanent profile wins over
// a temporary one, and two temporary profiles are both demoted under a newly created
// master. Callers write the audit log and open follow-up review tasks against whatever this
// returns, so a wrong answer here silently attributes merges to a child profile.
func Test_MergeMatchedProfiles_ReturnsSurvivingMaster(t *testing.T) {
	t.Run("existing permanent stays master", func(t *testing.T) {
		org := newOrg("merge-shape-a")
		existing := insertBareProfile(t, org, "user-1", map[string]interface{}{"name": "Ann"})
		incoming := insertBareProfile(t, org, "", map[string]interface{}{"name": "Ann"})

		master, err := workers.MergeMatchedProfiles(
			context.Background(),
			setup.TestOrg(t, org).ID,
			existing,
			incoming,
			constants.MergeReasonAutoMerge,
		)
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if master == nil {
			t.Fatal("expected a surviving master")
		}
		if master.ProfileID != existing.ProfileID {
			t.Errorf("master = %s, want the permanent profile %s", master.ProfileID, existing.ProfileID)
		}
	})

	t.Run("incoming permanent becomes master", func(t *testing.T) {
		org := newOrg("merge-shape-b")
		existing := insertBareProfile(t, org, "", map[string]interface{}{"name": "Ben"})
		incoming := insertBareProfile(t, org, "user-2", map[string]interface{}{"name": "Ben"})

		master, err := workers.MergeMatchedProfiles(
			context.Background(),
			setup.TestOrg(t, org).ID,
			existing,
			incoming,
			constants.MergeReasonAutoMerge,
		)
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if master == nil {
			t.Fatal("expected a surviving master")
		}
		// The profile passed in first is NOT the survivor here.
		if master.ProfileID != incoming.ProfileID {
			t.Errorf("master = %s, want the permanent incoming profile %s",
				master.ProfileID, incoming.ProfileID)
		}
	})

	t.Run("two temporaries get a brand new master", func(t *testing.T) {
		org := newOrg("merge-shape-c")
		existing := insertBareProfile(t, org, "", map[string]interface{}{"name": "Cal"})
		incoming := insertBareProfile(t, org, "", map[string]interface{}{"name": "Cal"})

		master, err := workers.MergeMatchedProfiles(
			context.Background(),
			setup.TestOrg(t, org).ID,
			existing,
			incoming,
			constants.MergeReasonAutoMerge,
		)
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if master == nil {
			t.Fatal("expected a surviving master")
		}
		if master.ProfileID == existing.ProfileID || master.ProfileID == incoming.ProfileID {
			t.Errorf("master = %s, want a newly created neutral master distinct from both inputs",
				master.ProfileID)
		}
	})

	t.Run("unmergeable pair reports no master and no error", func(t *testing.T) {
		org := newOrg("merge-shape-d")
		existing := insertBareProfile(t, org, "user-3", map[string]interface{}{"name": "Dee"})
		incoming := insertBareProfile(t, org, "user-4", map[string]interface{}{"name": "Dee"})

		master, err := workers.MergeMatchedProfiles(
			context.Background(),
			setup.TestOrg(t, org).ID,
			existing,
			incoming,
			constants.MergeReasonAutoMerge,
		)
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if master != nil {
			// A non-nil master here makes callers write an audit row for a merge that
			// never happened.
			t.Errorf("expected no master for two permanents with different user IDs, got %s",
				master.ProfileID)
		}
	})
}

// Test_BlockingKeyLookup_IsScopedToOrg checks the index cannot surface another tenant's
// profile as a merge candidate.
func Test_BlockingKeyLookup_IsScopedToOrg(t *testing.T) {
	orgA, orgB := newOrg("block-a"), newOrg("block-b")

	profileA := insertBareProfile(t, orgA, "", map[string]interface{}{"email": "shared@acme.com"})
	profileB := insertBareProfile(t, orgB, "", map[string]interface{}{"email": "shared@acme.com"})

	refA, refB := setup.TestOrg(t, orgA), setup.TestOrg(t, orgB)
	key := []irModel.BlockingKey{{AttributeName: "traits.email", KeyValue: "shared@acme.com"}}
	if err := irStore.UpsertBlockingKeys(context.Background(), profileA.ProfileID, refA.ID, key); err != nil {
		t.Fatalf("index profile A: %v", err)
	}
	if err := irStore.UpsertBlockingKeys(context.Background(), profileB.ProfileID, refB.ID, key); err != nil {
		t.Fatalf("index profile B: %v", err)
	}

	found, err := irStore.FindCandidateIDsByKeys(context.Background(), refA.ID, "traits.email",
		[]string{"shared@acme.com"}, "none", 100)
	if err != nil {
		t.Fatalf("candidate lookup: %v", err)
	}

	for _, id := range found {
		if id == profileB.ProfileID {
			t.Errorf("org %s lookup returned profile %s from org %s", orgA, id, orgB)
		}
	}
}

// Test_ResolveReviewTask_RejectsForeignOrg checks that a review task can only be resolved
// by the tenant that owns it. The task is fetched by ID alone, so without an explicit
// ownership check an administrator authenticated for one tenant could resolve another
// tenant's task and trigger a merge of profiles they cannot otherwise see.
func Test_ResolveReviewTask_RejectsForeignOrg(t *testing.T) {
	orgA, orgB := newOrg("tenant-a"), newOrg("tenant-b")

	incoming := insertBareProfile(t, orgB, "", map[string]interface{}{"name": "Eve"})
	candidate := insertBareProfile(t, orgB, "", map[string]interface{}{"name": "Eve"})

	refA, refB := setup.TestOrg(t, orgA), setup.TestOrg(t, orgB)
	task := irModel.ReviewTask{
		OrgID:              refB.ID,
		IncomingProfileID:  incoming.ProfileID,
		CandidateProfileID: candidate.ProfileID,
		MatchScore:         0.8,
		Status:             constants.ReviewStatusPending,
		ScoreBreakdown:     map[string]float64{"traits.name": 1.0},
	}
	if err := irStore.InsertReviewTask(context.Background(), refB.ID, task); err != nil {
		t.Fatalf("insert review task: %v", err)
	}

	pending, _, err := irStore.GetPendingReviewTasks(context.Background(), refB.ID, 10)
	if err != nil || len(pending) == 0 {
		t.Fatalf("expected a pending task for %s: %v", orgB, err)
	}

	svc := irService.GetIdentityResolutionService()
	if err := svc.ResolveReviewTask(context.Background(), refA.ID, pending[0].ID, true, "attacker", ""); err == nil {
		t.Errorf("org %s resolved a task belonging to org %s", orgA, orgB)
	}
}

// Test_ResolveReviewTask_WhenProfileMergedSince checks that a review task is never applied to the
// master of a profile merged after the task was raised. The administrator approved evidence about
// the profile alone, and its master holds data the task did not show, so the pair is evaluated
// again instead.
func Test_ResolveReviewTask_WhenProfileMergedSince(t *testing.T) {
	ctx := context.Background()

	// setupPair returns the org, the incoming and candidate profiles of a task, and another
	// permanent profile for the incoming one to merge with. The names differ, so evaluating them
	// again matches nothing.
	setupPair := func(t *testing.T, name string) (string, profileModel.Profile, profileModel.Profile,
		profileModel.Profile) {
		org := newOrg(name)
		incoming := insertBareProfile(t, org, "", map[string]interface{}{"name": "Gil"})
		candidate := insertBareProfile(t, org, "", map[string]interface{}{"name": "Ivy"})
		other := insertBareProfile(t, org, "user-"+name, map[string]interface{}{"name": "Hal"})
		return setup.TestOrg(t, org).ID, incoming, candidate, other
	}
	raiseTask := func(t *testing.T, orgID string, incoming, candidate profileModel.Profile) string {
		require.NoError(t, irStore.InsertReviewTask(ctx, orgID, irModel.ReviewTask{
			OrgID:              orgID,
			IncomingProfileID:  incoming.ProfileID,
			CandidateProfileID: candidate.ProfileID,
			MatchScore:         0.8,
			Status:             constants.ReviewStatusPending,
			ScoreBreakdown:     map[string]float64{"traits.name": 1.0},
		}))
		pending, _, err := irStore.GetPendingReviewTasks(ctx, orgID, 10)
		require.NoError(t, err)
		require.Len(t, pending, 1)
		return pending[0].ID
	}
	assertApart := func(t *testing.T, orgID string, candidate profileModel.Profile) {
		stored, err := profileStore.GetProfile(ctx, candidate.ProfileID, orgID)
		require.NoError(t, err)
		require.NotNil(t, stored)
		assert.Empty(t, stored.ProfileStatus.ReferenceProfileID,
			"the candidate was merged into the master of a profile the task no longer describes")
	}
	assertStatus := func(t *testing.T, taskID, want string) {
		task, err := irStore.GetReviewTaskByID(ctx, taskID)
		require.NoError(t, err)
		require.NotNil(t, task)
		assert.Equal(t, want, task.Status)
	}

	t.Run("the merge cancels the task", func(t *testing.T) {
		orgID, incoming, candidate, other := setupPair(t, "merge-cancels-task")
		taskID := raiseTask(t, orgID, incoming, candidate)

		master, err := workers.MergeMatchedProfiles(ctx, orgID, other, incoming, constants.MergeReasonAutoMerge)
		require.NoError(t, err)
		require.NotNil(t, master)
		assertStatus(t, taskID, constants.ReviewStatusCancelled)

		err = irService.GetIdentityResolutionService().ResolveReviewTask(ctx, orgID, taskID, true, "admin", "")
		assertClientError(t, err, http.StatusConflict, errors2.IRReviewTaskResolved.Code)
		assertApart(t, orgID, candidate)
	})

	t.Run("approving a task still pending withdraws it", func(t *testing.T) {
		// A task raised before merges cancelled them, or one approved while its profile merged.
		orgID, incoming, candidate, other := setupPair(t, "approve-withdraws-task")
		master, err := workers.MergeMatchedProfiles(ctx, orgID, other, incoming, constants.MergeReasonAutoMerge)
		require.NoError(t, err)
		require.NotNil(t, master)
		taskID := raiseTask(t, orgID, incoming, candidate)

		err = irService.GetIdentityResolutionService().ResolveReviewTask(ctx, orgID, taskID, true, "admin", "")
		assertClientError(t, err, http.StatusConflict, errors2.IRCannotMerge.Code)
		assertStatus(t, taskID, constants.ReviewStatusCancelled)
		assertApart(t, orgID, candidate)
	})

	t.Run("rejecting a task still pending withdraws it", func(t *testing.T) {
		// A rejection of the merged profile would never be read again, and its master holds data
		// the administrator did not see.
		orgID, incoming, candidate, other := setupPair(t, "reject-withdraws-task")
		master, err := workers.MergeMatchedProfiles(ctx, orgID, other, incoming, constants.MergeReasonAutoMerge)
		require.NoError(t, err)
		require.NotNil(t, master)
		taskID := raiseTask(t, orgID, incoming, candidate)

		err = irService.GetIdentityResolutionService().ResolveReviewTask(ctx, orgID, taskID, false, "admin", "")
		assertClientError(t, err, http.StatusConflict, errors2.IRCannotMerge.Code)
		assertStatus(t, taskID, constants.ReviewStatusCancelled)
		rejections, err := irStore.GetRejectionsForProfile(ctx, orgID, candidate.ProfileID)
		require.NoError(t, err)
		assert.Empty(t, rejections)
	})

	t.Run("rejecting a task of profiles still apart records the rejection", func(t *testing.T) {
		orgID, incoming, candidate, _ := setupPair(t, "reject-records-rejection")
		taskID := raiseTask(t, orgID, incoming, candidate)

		require.NoError(t, irService.GetIdentityResolutionService().ResolveReviewTask(ctx, orgID, taskID, false,
			"admin", ""))
		assertStatus(t, taskID, constants.ReviewStatusRejected)
		rejections, err := irStore.GetRejectionsForProfile(ctx, orgID, candidate.ProfileID)
		require.NoError(t, err)
		assert.Contains(t, rejections, incoming.ProfileID)
	})
}

// Test_ResolveReviewTask_TwoAdministratorsAtOnce checks two administrators who approve the same task at
// the same moment. The task is approved once: the merge locks the task after the profiles and finds it
// resolved, so the second approval is told so rather than withdrawing the task the first one approved.
func Test_ResolveReviewTask_TwoAdministratorsAtOnce(t *testing.T) {
	ctx := context.Background()
	for round := range 5 {
		org := newOrg(fmt.Sprintf("two-approvals-%d", round))
		incoming := insertBareProfile(t, org, "", map[string]interface{}{"name": "Jo"})
		candidate := insertBareProfile(t, org, "", map[string]interface{}{"name": "Kim"})
		orgID := setup.TestOrg(t, org).ID
		require.NoError(t, irStore.InsertReviewTask(ctx, orgID, irModel.ReviewTask{
			OrgID:              orgID,
			IncomingProfileID:  incoming.ProfileID,
			CandidateProfileID: candidate.ProfileID,
			MatchScore:         0.8,
			Status:             constants.ReviewStatusPending,
			ScoreBreakdown:     map[string]float64{"traits.name": 1.0},
		}))
		pending, _, err := irStore.GetPendingReviewTasks(ctx, orgID, 10)
		require.NoError(t, err)
		require.Len(t, pending, 1)
		taskID := pending[0].ID

		results := make([]error, 2)
		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i] = irService.GetIdentityResolutionService().ResolveReviewTask(ctx, orgID, taskID, true,
					fmt.Sprintf("admin-%d", i), "")
			}()
		}
		wg.Wait()

		succeeded := 0
		for _, err := range results {
			if err == nil {
				succeeded++
				continue
			}
			assertClientError(t, err, http.StatusConflict, errors2.IRReviewTaskResolved.Code)
		}
		assert.Equal(t, 1, succeeded, "round %d: one approval merges the profiles", round)
		task, err := irStore.GetReviewTaskByID(ctx, taskID)
		require.NoError(t, err)
		require.NotNil(t, task)
		assert.Equal(t, constants.ReviewStatusApproved, task.Status, "round %d", round)
	}
}

// Test_ReviewTask_CanBeRecreatedAfterCancellation checks that a pair cancelled by a cascade
// can be proposed again when the profiles still match. A cancellation is a system action,
// not a human decision, so it must not permanently suppress the pair the way a rejection
// does.
func Test_ReviewTask_CanBeRecreatedAfterCancellation(t *testing.T) {
	org := newOrg("cancel-recreate")
	incoming := insertBareProfile(t, org, "", map[string]interface{}{"name": "Fay"})
	candidate := insertBareProfile(t, org, "", map[string]interface{}{"name": "Fay"})

	ref := setup.TestOrg(t, org)
	task := irModel.ReviewTask{
		OrgID:              ref.ID,
		IncomingProfileID:  incoming.ProfileID,
		CandidateProfileID: candidate.ProfileID,
		MatchScore:         0.80,
		Status:             constants.ReviewStatusPending,
	}
	if err := irStore.InsertReviewTask(context.Background(), ref.ID, task); err != nil {
		t.Fatalf("insert review task: %v", err)
	}

	pending, _, err := irStore.GetPendingReviewTasks(context.Background(), ref.ID, 10)
	if err != nil || len(pending) == 0 {
		t.Fatalf("expected a pending task: %v", err)
	}
	if err := irStore.UpdateReviewTaskStatus(context.Background(), pending[0].ID, constants.ReviewStatusCancelled,
		constants.CanceledBySystem, "cascade"); err != nil {
		t.Fatalf("cancel task: %v", err)
	}

	task.MatchScore = 0.91
	if err := irStore.InsertReviewTask(context.Background(), ref.ID, task); err != nil {
		t.Fatalf("re-insert review task: %v", err)
	}

	reopened, _, err := irStore.GetPendingReviewTasks(context.Background(), ref.ID, 10)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(reopened) == 0 {
		t.Error("a still-matching pair could not be re-proposed after its task was cancelled")
	}
}

// Test_BlockingKeys_RemovedWhenProfileDeleted checks that deleting a profile takes it out
// of the resolution index. Nothing cascades to blocking_keys, so the delete path has to
// remove them explicitly or the profile keeps surfacing as a merge candidate.
//
// Note this exercises the service-level delete. The underlying store still leaves the
// profiles row in place — see the separate finding on DeleteProfileByProfileID — so this
// asserts only that the profile is no longer discoverable through the index.
func Test_BlockingKeys_RemovedWhenProfileDeleted(t *testing.T) {
	org := newOrg("delete-index")
	profile := insertBareProfile(t, org, "", map[string]interface{}{"email": "gone@acme.com"})

	ref := setup.TestOrg(t, org)
	key := []irModel.BlockingKey{{AttributeName: "traits.email", KeyValue: "gone@acme.com"}}
	if err := irStore.UpsertBlockingKeys(context.Background(), profile.ProfileID, ref.ID, key); err != nil {
		t.Fatalf("index profile: %v", err)
	}
	if err := profileService.GetProfilesService().DeleteProfile(
		context.Background(), profile.ProfileID, ref.ID,
	); err != nil {
		t.Fatalf("delete profile: %v", err)
	}

	found, err := irStore.FindCandidateIDsByKeys(context.Background(), ref.ID, "traits.email",
		[]string{"gone@acme.com"}, "none", 100)
	if err != nil {
		t.Fatalf("candidate lookup: %v", err)
	}
	for _, id := range found {
		if id == profile.ProfileID {
			t.Errorf("deleted profile %s is still a merge candidate", id)
		}
	}
}
