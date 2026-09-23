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
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"
	ruleModel "github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	unificationService "github.com/wso2/identity-customer-data-service/internal/unification_rules/service"
)

// insertTestProfile puts one profile in the store, without going through the
// service, so that a merge test starts from a known row and enqueues nothing.
func insertTestProfile(t *testing.T, org string, traits map[string]interface{}) profileModel.Profile {

	t.Helper()

	now := time.Now().UTC()
	profile := profileModel.Profile{
		ProfileId: uuid.New().String(),
		OrgHandle: org,
		CreatedAt: now,
		UpdatedAt: now,
		Traits:    traits,
		ProfileStatus: &profileModel.ProfileStatus{
			ListProfile: true,
		},
	}
	profile.Location = fmt.Sprintf("/profiles/%s", profile.ProfileId)

	require.NoError(t, profileStore.InsertProfile(context.Background(), profile))
	return profile
}

// Test_ProfileMerge_writesNothingWhenItDoesNotFinish covers a merge that stops
// after it has already written. A merge used to write through four to six
// separate calls, each one committing on its own, so an instance that stopped
// in the middle left the references moved and the merged data missing.
func Test_ProfileMerge_writesNothingWhenItDoesNotFinish(t *testing.T) {

	ctx := context.Background()
	org := fmt.Sprintf("merge-atomic-%d", time.Now().UnixNano())

	master := insertTestProfile(t, org, map[string]interface{}{"interests": []interface{}{"music"}})
	child := insertTestProfile(t, org, map[string]interface{}{"interests": []interface{}{"sports"}})

	stopped := errors.New("the merge stopped before it finished")

	err := profileStore.WithProfileMerge(ctx, []string{master.ProfileId, child.ProfileId},
		func(ctx context.Context) error {
			children := []profileModel.Reference{{ProfileId: child.ProfileId, Reason: "test"}}
			if err := profileStore.UpdateProfileReferences(ctx, master, children); err != nil {
				return err
			}

			master.Traits = map[string]interface{}{"interests": []interface{}{"music", "sports"}}
			if err := profileStore.UpdateProfile(ctx, master); err != nil {
				return err
			}

			return stopped
		})
	require.ErrorIs(t, err, stopped)

	refs, err := profileStore.FetchReferencedProfiles(ctx, master.ProfileId)
	require.NoError(t, err)
	require.Empty(t, refs, "the child was left attached to the master by a merge that did not finish")

	storedChild, err := profileStore.GetProfile(ctx, child.ProfileId)
	require.NoError(t, err)
	require.Equal(t, "", storedChild.ProfileStatus.ReferenceProfileId)

	storedMaster, err := profileStore.GetProfile(ctx, master.ProfileId)
	require.NoError(t, err)
	require.Equal(t, []interface{}{"music"}, storedMaster.Traits["interests"])
}

// Test_ProfileMerge_doesNotLoseTheMergeThatRanFirst covers two merges of the
// same profile at the same time. Each one reads the profile, changes it and
// writes the whole document back, so without a lock the second write used to
// overwrite the first one and no error said so.
//
// PostgreSQL holds the profile rows the merge locks. The inbuilt database takes
// its write lock when the transaction begins. Both make the second merge wait,
// and read what the first one wrote.
func Test_ProfileMerge_doesNotLoseTheMergeThatRanFirst(t *testing.T) {

	ctx := context.Background()
	org := fmt.Sprintf("merge-lock-%d", time.Now().UnixNano())

	profile := insertTestProfile(t, org, map[string]interface{}{"interests": []interface{}{}})

	addInterest := func(interest string) error {
		return profileStore.WithProfileMerge(ctx, []string{profile.ProfileId},
			func(ctx context.Context) error {
				stored, err := profileStore.GetProfile(ctx, profile.ProfileId)
				if err != nil {
					return err
				}

				interests, _ := stored.Traits["interests"].([]interface{})
				// The gap between the read and the write is what a lost update
				// needs. This test opens it on purpose.
				time.Sleep(200 * time.Millisecond)

				stored.Traits["interests"] = append(interests, interest)
				return profileStore.UpdateProfile(ctx, *stored)
			})
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, interest := range []string{"music", "sports"} {
		wg.Add(1)
		go func(index int, interest string) {
			defer wg.Done()
			errs[index] = addInterest(interest)
		}(i, interest)
	}
	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])

	stored, err := profileStore.GetProfile(ctx, profile.ProfileId)
	require.NoError(t, err)
	require.ElementsMatch(t, []interface{}{"music", "sports"}, stored.Traits["interests"],
		"one merge overwrote the other")
}

// Test_ProfileMerge_repeatsWithoutBuildingASecondMaster covers the message that
// arrives a second time. A merge that is not acknowledged comes back, so the
// second run must reach the same store as the first one.
func Test_ProfileMerge_repeatsWithoutBuildingASecondMaster(t *testing.T) {

	ctx := context.Background()
	org := fmt.Sprintf("merge-repeat-%d", time.Now().UnixNano())

	schemaSvc := schemaService.GetProfileSchemaService()
	_, err := schemaSvc.AddProfileSchemaAttributesForScope(ctx, []schemaModel.ProfileSchemaAttribute{
		{OrgId: org, AttributeId: uuid.New().String(), AttributeName: "identity_attributes.email",
			ValueType: constants.StringDataType, MergeStrategy: "combine",
			Mutability: constants.MutabilityReadWrite, MultiValued: true},
	}, constants.IdentityAttributes, org)
	require.NoError(t, err)

	_, err = schemaSvc.AddProfileSchemaAttributesForScope(ctx, []schemaModel.ProfileSchemaAttribute{
		{OrgId: org, AttributeId: uuid.New().String(), AttributeName: "traits.interests",
			ValueType: constants.StringDataType, MergeStrategy: "combine",
			Mutability: constants.MutabilityReadWrite, MultiValued: true},
	}, constants.Traits, org)
	require.NoError(t, err)

	require.NoError(t, unificationService.GetUnificationRuleService().AddUnificationRule(ctx, ruleModel.UnificationRule{
		RuleName:     "email_based",
		RuleId:       uuid.New().String(),
		OrgHandle:    org,
		PropertyName: "identity_attributes.email",
		Priority:     1,
		IsActive:     true,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}, org))

	profileSvc := profileService.GetProfilesService()
	first, err := profileSvc.CreateProfile(ctx,
		mustUnmarshalProfile(`{"identity_attributes":{"email":["repeat@wso2.com"]},"traits":{"interests":["music"]}}`), org)
	require.NoError(t, err)
	second, err := profileSvc.CreateProfile(ctx,
		mustUnmarshalProfile(`{"identity_attributes":{"email":["repeat@wso2.com"]},"traits":{"interests":["sports"]}}`), org)
	require.NoError(t, err)

	masterId := waitForMaster(t, profileSvc, second.ProfileId)
	require.Equal(t, masterId, waitForMaster(t, profileSvc, first.ProfileId))

	master, err := profileSvc.GetProfile(ctx, masterId)
	require.NoError(t, err)
	require.Len(t, master.MergedFrom, 2)
	traitsAfterFirstRun := master.Traits["interests"]

	// The same message again. The profile is already a child of the master, so
	// the second run has nothing left to do.
	storedSecond, err := profileStore.GetProfile(ctx, second.ProfileId)
	require.NoError(t, err)
	workers.EnqueueProfileForProcessing(*storedSecond)
	time.Sleep(2 * time.Second)

	repeated, err := profileSvc.GetProfile(ctx, second.ProfileId)
	require.NoError(t, err)
	require.Equal(t, masterId, repeated.MergedTo.ProfileId, "the repeated merge moved the profile to another master")

	masterAgain, err := profileSvc.GetProfile(ctx, masterId)
	require.NoError(t, err)
	require.Len(t, masterAgain.MergedFrom, 2, "the repeated merge attached the children again")
	require.Equal(t, traitsAfterFirstRun, masterAgain.Traits["interests"])

	var profileCount int
	require.NoError(t, suiteDB.QueryRow("SELECT count(*) FROM profiles WHERE org_handle = $1", org).
		Scan(&profileCount))
	require.Equal(t, 3, profileCount, "the repeated merge built a second master")
}

// waitForMaster waits until the unification worker has given the profile a
// master, and returns its identifier.
func waitForMaster(t *testing.T, profileSvc profileService.ProfilesServiceInterface, profileId string) string {

	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		profile, err := profileSvc.GetProfile(context.Background(), profileId)
		if err == nil && profile.MergedTo != nil && profile.MergedTo.ProfileId != "" {
			return profile.MergedTo.ProfileId
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("profile %s was not merged into a master", profileId)
	return ""
}
