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
	"crypto/rand"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	profileSchema "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

// writeDuringMergeRounds is how many times each test races a write against a merge. Where the two
// interleave varies from round to round, so a write that is not serialized with the merge loses an
// update in some of them.
const writeDuringMergeRounds = 40

// Test_PatchDuringMerge_KeepsBothChanges patches a master while another profile is merged into it.
// A merge reads both profiles, combines them and writes the master back whole, and a patch reads the
// master, changes it and writes it back whole. Unless one waits for the other, the one that writes
// last puts back a copy without the other's change: the patched value, or the merged profile's data.
func Test_PatchDuringMerge_KeepsBothChanges(t *testing.T) {
	ctx := context.Background()
	for round := range writeDuringMergeRounds {
		master, merged, orgID := setUpWriteDuringMerge(t, fmt.Sprintf("patch-during-merge-%d", round))

		mergeErr, writeErr := raceWriteWithMerge(ctx, orgID, master, merged, func() error {
			_, err := profileService.GetProfilesService().PatchProfile(ctx, master.ProfileID, orgID,
				profileModel.ProfilePatchRequest{Traits: map[string]interface{}{"color": "red"}})
			return err
		})
		if mergeErr != nil || writeErr != nil {
			t.Fatalf("round %d: merge error %v, patch error %v", round, mergeErr, writeErr)
		}

		stored := storedProfile(t, orgID, master.ProfileID)
		if stored.Traits["color"] != "red" {
			t.Errorf("round %d: color is %v, want red: the merge wrote back a copy without the patch",
				round, stored.Traits["color"])
		}
		if stored.Traits["origin"] != "merged" {
			t.Errorf("round %d: origin is %v, want merged: the patch wrote back a copy without the merged "+
				"profile's data", round, stored.Traits["origin"])
		}
	}
}

// Test_PutDuringMerge_KeepsThePut replaces a master while another profile is merged into it. A PUT
// replaces the traits, so the merged profile's data is kept only when the PUT runs first, but the
// value the PUT wrote is never lost.
func Test_PutDuringMerge_KeepsThePut(t *testing.T) {
	ctx := context.Background()
	for round := range writeDuringMergeRounds {
		master, merged, orgID := setUpWriteDuringMerge(t, fmt.Sprintf("put-during-merge-%d", round))

		mergeErr, writeErr := raceWriteWithMerge(ctx, orgID, master, merged, func() error {
			_, err := profileService.GetProfilesService().UpdateProfile(ctx, master.ProfileID, orgID,
				profileModel.ProfileRequest{
					UserID: master.UserID,
					Traits: map[string]interface{}{"email": "bob@writemerge.io", "color": "red"},
				})
			return err
		})
		if mergeErr != nil || writeErr != nil {
			t.Fatalf("round %d: merge error %v, put error %v", round, mergeErr, writeErr)
		}

		stored := storedProfile(t, orgID, master.ProfileID)
		if stored.Traits["color"] != "red" {
			t.Errorf("round %d: color is %v, want red: the merge wrote back a copy without the PUT",
				round, stored.Traits["color"])
		}
		if stored.ProfileStatus == nil || !stored.ProfileStatus.IsReferenceProfile {
			t.Errorf("round %d: the PUT changed the master's status", round)
		}
	}
}

// setUpWriteDuringMerge creates a permanent profile and a temporary profile with the same email,
// which a merge combines into the permanent one.
func setUpWriteDuringMerge(t *testing.T, prefix string) (master, merged profileModel.Profile, orgID string) {
	t.Helper()
	org := newOrg(prefix)
	ref := setup.TestOrg(t, org)
	addStringTraits(t, ref.ID, "email", "color", "origin")
	master = insertBareProfile(t, org, "u-bob-"+uuid.NewString(),
		map[string]interface{}{"email": "bob@writemerge.io", "color": "blue"})
	merged = insertBareProfile(t, org, "",
		map[string]interface{}{"email": "bob@writemerge.io", "origin": "merged"})
	return master, merged, ref.ID
}

// raceWriteWithMerge merges the profiles and runs the write at the same moment, the write a random
// moment later so that it lands at a different point of the merge in each round.
func raceWriteWithMerge(ctx context.Context, orgID string, master, merged profileModel.Profile,
	write func() error) (mergeErr, writeErr error) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, mergeErr = workers.MergeMatchedProfiles(ctx, orgID, master, merged, constants.MergeReasonAutoMerge)
	}()
	go func() {
		defer wg.Done()
		<-start
		time.Sleep(randomDelay(3 * time.Millisecond))
		writeErr = write()
	}()
	close(start)
	wg.Wait()
	return mergeErr, writeErr
}

// addStringTraits declares string traits in the organization's schema, so that writes of them pass
// validation.
func addStringTraits(t *testing.T, orgID string, names ...string) {
	t.Helper()
	attrs := make([]profileSchema.ProfileSchemaAttribute, 0, len(names))
	for _, name := range names {
		attrs = append(attrs, profileSchema.ProfileSchemaAttribute{
			OrgID:         orgID,
			AttributeID:   uuid.NewString(),
			AttributeName: "traits." + name,
			ValueType:     constants.StringDataType,
			MergeStrategy: "overwrite",
			Mutability:    constants.MutabilityReadWrite,
		})
	}
	if _, err := schemaService.GetProfileSchemaService().AddProfileSchemaAttributesForScope(context.Background(),
		attrs, constants.Traits, orgID); err != nil {
		t.Fatalf("add traits to the schema: %v", err)
	}
}

// storedProfile reads a profile from the store, failing the test when it is not there.
func storedProfile(t *testing.T, orgID, profileID string) *profileModel.Profile {
	t.Helper()
	stored, err := profileStore.GetProfile(context.Background(), profileID, orgID)
	if err != nil || stored == nil {
		t.Fatalf("read profile %s: %v", profileID, err)
	}
	return stored
}

// randomDelay returns a random delay below limit.
func randomDelay(limit time.Duration) time.Duration {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(limit)))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
}
