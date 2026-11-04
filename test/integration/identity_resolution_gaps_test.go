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

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	irModel "github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	irService "github.com/wso2/identity-customer-data-service/internal/identity_resolution/service"
	irStore "github.com/wso2/identity-customer-data-service/internal/identity_resolution/store"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"
)

func newOrg(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// insertBareProfile writes a profile directly, bypassing the service so the test controls
// exactly what exists without waiting on the async unification queue.
func insertBareProfile(t *testing.T, org, userID string, traits map[string]interface{}) profileModel.Profile {
	t.Helper()

	profile := profileModel.Profile{
		ProfileId:          uuid.New().String(),
		UserId:             userID,
		OrgHandle:          org,
		Traits:             traits,
		IdentityAttributes: map[string]interface{}{},
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
		ProfileStatus:      &profileModel.ProfileStatus{},
	}
	if err := profileStore.InsertProfile(context.Background(), profile); err != nil {
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

		master, err := workers.MergeMatchedProfiles(context.Background(), existing, incoming, constants.MergeReasonAutoMerge)
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if master == nil {
			t.Fatal("expected a surviving master")
		}
		if master.ProfileId != existing.ProfileId {
			t.Errorf("master = %s, want the permanent profile %s", master.ProfileId, existing.ProfileId)
		}
	})

	t.Run("incoming permanent becomes master", func(t *testing.T) {
		org := newOrg("merge-shape-b")
		existing := insertBareProfile(t, org, "", map[string]interface{}{"name": "Ben"})
		incoming := insertBareProfile(t, org, "user-2", map[string]interface{}{"name": "Ben"})

		master, err := workers.MergeMatchedProfiles(context.Background(), existing, incoming, constants.MergeReasonAutoMerge)
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if master == nil {
			t.Fatal("expected a surviving master")
		}
		// The profile passed in first is NOT the survivor here.
		if master.ProfileId != incoming.ProfileId {
			t.Errorf("master = %s, want the permanent incoming profile %s",
				master.ProfileId, incoming.ProfileId)
		}
	})

	t.Run("two temporaries get a brand new master", func(t *testing.T) {
		org := newOrg("merge-shape-c")
		existing := insertBareProfile(t, org, "", map[string]interface{}{"name": "Cal"})
		incoming := insertBareProfile(t, org, "", map[string]interface{}{"name": "Cal"})

		master, err := workers.MergeMatchedProfiles(context.Background(), existing, incoming, constants.MergeReasonAutoMerge)
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if master == nil {
			t.Fatal("expected a surviving master")
		}
		if master.ProfileId == existing.ProfileId || master.ProfileId == incoming.ProfileId {
			t.Errorf("master = %s, want a newly created neutral master distinct from both inputs",
				master.ProfileId)
		}
	})

	t.Run("unmergeable pair reports no master and no error", func(t *testing.T) {
		org := newOrg("merge-shape-d")
		existing := insertBareProfile(t, org, "user-3", map[string]interface{}{"name": "Dee"})
		incoming := insertBareProfile(t, org, "user-4", map[string]interface{}{"name": "Dee"})

		master, err := workers.MergeMatchedProfiles(context.Background(), existing, incoming, constants.MergeReasonAutoMerge)
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if master != nil {
			// A non-nil master here makes callers write an audit row for a merge that
			// never happened.
			t.Errorf("expected no master for two permanents with different user IDs, got %s",
				master.ProfileId)
		}
	})
}

// Test_BlockingKeyLookup_IsScopedToOrg checks the index cannot surface another tenant's
// profile as a merge candidate.
func Test_BlockingKeyLookup_IsScopedToOrg(t *testing.T) {
	orgA, orgB := newOrg("block-a"), newOrg("block-b")

	profileA := insertBareProfile(t, orgA, "", map[string]interface{}{"email": "shared@acme.com"})
	profileB := insertBareProfile(t, orgB, "", map[string]interface{}{"email": "shared@acme.com"})

	key := []irModel.BlockingKey{{AttributeName: "traits.email", KeyValue: "shared@acme.com"}}
	if err := irStore.UpsertBlockingKeys(context.Background(), profileA.ProfileId, orgA, key); err != nil {
		t.Fatalf("index profile A: %v", err)
	}
	if err := irStore.UpsertBlockingKeys(context.Background(), profileB.ProfileId, orgB, key); err != nil {
		t.Fatalf("index profile B: %v", err)
	}

	found, err := irStore.FindCandidateIDsByKeys(context.Background(), orgA, "traits.email",
		[]string{"shared@acme.com"}, "none", 100)
	if err != nil {
		t.Fatalf("candidate lookup: %v", err)
	}

	for _, id := range found {
		if id == profileB.ProfileId {
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

	task := irModel.ReviewTask{
		OrgHandle:          orgB,
		IncomingProfileID:  incoming.ProfileId,
		CandidateProfileID: candidate.ProfileId,
		MatchScore:         0.8,
		Status:             constants.ReviewStatusPending,
		ScoreBreakdown:     map[string]float64{"traits.name": 1.0},
	}
	if err := irStore.InsertReviewTask(context.Background(), task); err != nil {
		t.Fatalf("insert review task: %v", err)
	}

	pending, _, err := irStore.GetPendingReviewTasks(context.Background(), orgB, 10)
	if err != nil || len(pending) == 0 {
		t.Fatalf("expected a pending task for %s: %v", orgB, err)
	}

	svc := irService.GetIdentityResolutionService()
	if err := svc.ResolveReviewTask(context.Background(), orgA, pending[0].ID, true, "attacker", ""); err == nil {
		t.Errorf("org %s resolved a task belonging to org %s", orgA, orgB)
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

	task := irModel.ReviewTask{
		OrgHandle:          org,
		IncomingProfileID:  incoming.ProfileId,
		CandidateProfileID: candidate.ProfileId,
		MatchScore:         0.80,
		Status:             constants.ReviewStatusPending,
	}
	if err := irStore.InsertReviewTask(context.Background(), task); err != nil {
		t.Fatalf("insert review task: %v", err)
	}

	pending, _, err := irStore.GetPendingReviewTasks(context.Background(), org, 10)
	if err != nil || len(pending) == 0 {
		t.Fatalf("expected a pending task: %v", err)
	}
	if err := irStore.UpdateReviewTaskStatus(context.Background(), pending[0].ID, constants.ReviewStatusCancelled,
		constants.CanceledBySystem, "cascade"); err != nil {
		t.Fatalf("cancel task: %v", err)
	}

	task.MatchScore = 0.91
	if err := irStore.InsertReviewTask(context.Background(), task); err != nil {
		t.Fatalf("re-insert review task: %v", err)
	}

	reopened, _, err := irStore.GetPendingReviewTasks(context.Background(), org, 10)
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
// profiles row in place — see the separate finding on DeleteProfileByProfileId — so this
// asserts only that the profile is no longer discoverable through the index.
func Test_BlockingKeys_RemovedWhenProfileDeleted(t *testing.T) {
	org := newOrg("delete-index")
	profile := insertBareProfile(t, org, "", map[string]interface{}{"email": "gone@acme.com"})

	key := []irModel.BlockingKey{{AttributeName: "traits.email", KeyValue: "gone@acme.com"}}
	if err := irStore.UpsertBlockingKeys(context.Background(), profile.ProfileId, org, key); err != nil {
		t.Fatalf("index profile: %v", err)
	}
	if err := profileService.GetProfilesService().DeleteProfile(context.Background(), profile.ProfileId, org); err != nil {
		t.Fatalf("delete profile: %v", err)
	}

	found, err := irStore.FindCandidateIDsByKeys(context.Background(), org, "traits.email",
		[]string{"gone@acme.com"}, "none", 100)
	if err != nil {
		t.Fatalf("candidate lookup: %v", err)
	}
	for _, id := range found {
		if id == profile.ProfileId {
			t.Errorf("deleted profile %s is still a merge candidate", id)
		}
	}
}
