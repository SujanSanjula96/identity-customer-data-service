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
	"github.com/stretchr/testify/require"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	unificationService "github.com/wso2/identity-customer-data-service/internal/unification_rules/service"
)

// Test_ProfileMerge_leavesNothingBehindWhenItFails fails the last write of a
// merge that builds a new master, and checks that the merge wrote nothing.
// It then lets the same message through twice, and checks that the profiles
// end with exactly one master.
func Test_ProfileMerge_leavesNothingBehindWhenItFails(t *testing.T) {

	org := newMergeTestOrg(t)
	first := insertMergeTestProfile(t, org, "", "shared@wso2.com", "music")
	second := insertMergeTestProfile(t, org, "", "shared@wso2.com", "art")

	removeFault := failProfileUpdatesOf(t, org)
	workers.EnqueueProfileForProcessing(second)
	waitForEarlierUnificationJobs(t)

	require.Equal(t, 2, countProfilesOf(t, org), "the failed merge left a master behind")
	require.Empty(t, masterOf(t, first.ProfileId), "the failed merge left a profile attached")
	require.Empty(t, masterOf(t, second.ProfileId), "the failed merge left a profile attached")

	removeFault()
	workers.EnqueueProfileForProcessing(second)
	master := waitForMaster(t, second.ProfileId)
	require.Equal(t, master, masterOf(t, first.ProfileId))
	require.Equal(t, 3, countProfilesOf(t, org))

	// A second delivery of the same message changes nothing.
	workers.EnqueueProfileForProcessing(second)
	waitForEarlierUnificationJobs(t)
	require.Equal(t, 3, countProfilesOf(t, org), "a repeated merge built a second master")
	require.Equal(t, master, masterOf(t, first.ProfileId))
	require.Equal(t, master, masterOf(t, second.ProfileId))
}

// Test_ProfileMerge_keepsAWriteThatCommitsDuringTheMerge holds a write to the
// master open while a merge into that master starts, and checks that the
// merged master keeps that write.
func Test_ProfileMerge_keepsAWriteThatCommitsDuringTheMerge(t *testing.T) {

	org := newMergeTestOrg(t)
	master := insertMergeTestProfile(t, org, "user-"+uuid.New().String(), "shared@wso2.com", "reading")
	child := insertMergeTestProfile(t, org, "", "shared@wso2.com", "travel")

	holdWrite(t, master.ProfileId,
		func(profile *profileModel.Profile) {
			profile.Traits["interests"] = append(profile.Traits["interests"].([]interface{}), "cooking")
		},
		func() { workers.EnqueueProfileForProcessing(child) })

	require.Equal(t, master.ProfileId, waitForMaster(t, child.ProfileId))
	merged, err := profileStore.GetProfile(context.Background(), master.ProfileId)
	require.NoError(t, err)
	require.ElementsMatch(t, []interface{}{"reading", "cooking", "travel"}, merged.Traits["interests"])
}

// Test_PatchProfile_keepsAWriteThatCommitsDuringThePatch holds a write to a
// profile open while a patch of another attribute starts, and checks that the
// profile keeps both.
func Test_PatchProfile_keepsAWriteThatCommitsDuringThePatch(t *testing.T) {

	org := newMergeTestOrg(t)
	profile := insertMergeTestProfile(t, org, "", "patch@wso2.com", "reading")

	patched := make(chan error, 1)
	holdWrite(t, profile.ProfileId,
		func(profile *profileModel.Profile) {
			profile.Traits["nickname"] = "sam"
		},
		func() {
			go func() {
				_, err := profileService.GetProfilesService().PatchProfile(context.Background(), profile.ProfileId,
					org, map[string]interface{}{"traits": map[string]interface{}{"interests": []interface{}{"travel"}}})
				patched <- err
			}()
		})
	require.NoError(t, <-patched)

	stored, err := profileStore.GetProfile(context.Background(), profile.ProfileId)
	require.NoError(t, err)
	require.Equal(t, "sam", stored.Traits["nickname"], "the patch overwrote the write that committed first")
	require.Contains(t, stored.Traits["interests"], "travel")
}

// holdWrite changes the profile inside a transaction that holds its lock, runs
// start while that transaction is still open, and commits a moment later. The
// pause gives the work that start begins time to read the profile, so work
// that does not wait for the lock reads the value from before the write.
func holdWrite(t *testing.T, profileId string, change func(profile *profileModel.Profile), start func()) {

	t.Helper()
	locked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- profileStore.WithProfilesLocked(context.Background(), []string{profileId},
			func(ctx context.Context) error {
				profile, err := profileStore.GetProfile(ctx, profileId)
				if err != nil {
					return err
				}
				change(profile)
				if err := profileStore.UpdateProfile(ctx, *profile); err != nil {
					return err
				}
				close(locked)
				<-release
				return nil
			})
	}()

	select {
	case <-locked:
	case err := <-done:
		t.Fatalf("the write did not take the lock: %v", err)
	}
	start()
	time.Sleep(300 * time.Millisecond)
	close(release)
	require.NoError(t, <-done)
}

// newMergeTestOrg creates an organization with an email unification rule and
// the schema the merge tests use.
func newMergeTestOrg(t *testing.T) string {

	t.Helper()
	ctx := context.Background()
	org := fmt.Sprintf("merge-%s", uuid.New().String())

	attribute := func(name string, strategy string, multiValued bool) schemaModel.ProfileSchemaAttribute {
		return schemaModel.ProfileSchemaAttribute{OrgId: org, AttributeId: uuid.New().String(),
			AttributeName: name, ValueType: constants.StringDataType, MergeStrategy: strategy,
			Mutability: constants.MutabilityReadWrite, MultiValued: multiValued}
	}
	schemaSvc := schemaService.GetProfileSchemaService()
	_, err := schemaSvc.AddProfileSchemaAttributesForScope(ctx,
		[]schemaModel.ProfileSchemaAttribute{attribute("identity_attributes.email", "combine", true)},
		constants.IdentityAttributes, org)
	require.NoError(t, err)
	_, err = schemaSvc.AddProfileSchemaAttributesForScope(ctx,
		[]schemaModel.ProfileSchemaAttribute{
			attribute("traits.interests", "combine", true),
			attribute("traits.nickname", "overwrite", false),
		}, constants.Traits, org)
	require.NoError(t, err)

	ruleSvc := unificationService.GetUnificationRuleService()
	require.NoError(t, ruleSvc.AddUnificationRule(ctx, model.UnificationRule{
		RuleId: uuid.New().String(), RuleName: "email_based", OrgHandle: org,
		PropertyName: "identity_attributes.email", Priority: 1, IsActive: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}, org))

	t.Cleanup(func() {
		rules, _ := ruleSvc.GetUnificationRules(ctx, org)
		for _, rule := range rules {
			_ = ruleSvc.DeleteUnificationRule(ctx, rule.RuleId)
		}
		rows, err := suiteDB.Query(`SELECT profile_id FROM profiles WHERE org_handle = $1`, org)
		if err == nil {
			var ids []string
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			_ = rows.Close()
			for _, id := range ids {
				_ = profileStore.DeleteProfile(ctx, id)
			}
		}
		_ = schemaSvc.DeleteProfileSchema(ctx, org)
	})
	return org
}

// insertMergeTestProfile writes a profile straight to the store, which queues
// no unification job, so each test decides when the worker sees it.
func insertMergeTestProfile(t *testing.T, org, userId, email, interest string) profileModel.Profile {

	t.Helper()
	now := time.Now().UTC()
	profileId := uuid.New().String()
	profile := profileModel.Profile{
		ProfileId:          profileId,
		OrgHandle:          org,
		UserId:             userId,
		Traits:             map[string]interface{}{"interests": []interface{}{interest}},
		IdentityAttributes: map[string]interface{}{"email": []interface{}{email}},
		ProfileStatus:      &profileModel.ProfileStatus{IsReferenceProfile: true, ListProfile: true},
		CreatedAt:          now,
		UpdatedAt:          now,
		Location:           utils.BuildProfileLocation(org, profileId),
	}
	require.NoError(t, profileStore.InsertProfile(context.Background(), profile))
	return profile
}

// waitForEarlierUnificationJobs returns once the worker has finished every job
// queued before the call. The worker runs its jobs one at a time and in order,
// so a merge queued now finishes after all of them.
func waitForEarlierUnificationJobs(t *testing.T) {

	t.Helper()
	org := newMergeTestOrg(t)
	insertMergeTestProfile(t, org, "", "marker@wso2.com", "one")
	last := insertMergeTestProfile(t, org, "", "marker@wso2.com", "two")
	workers.EnqueueProfileForProcessing(last)
	waitForMaster(t, last.ProfileId)
}

// waitForMaster waits until the profile is merged into a master, and returns
// that master.
func waitForMaster(t *testing.T, profileId string) string {

	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if master := masterOf(t, profileId); master != "" {
			return master
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("profile %s was not merged within 10s", profileId)
	return ""
}

// masterOf returns the master the profile is merged into, or "".
func masterOf(t *testing.T, profileId string) string {

	t.Helper()
	profile, err := profileStore.GetProfile(context.Background(), profileId)
	require.NoError(t, err)
	require.NotNil(t, profile)
	return profile.ProfileStatus.ReferenceProfileId
}

func countProfilesOf(t *testing.T, org string) int {

	t.Helper()
	var count int
	require.NoError(t, suiteDB.QueryRow(`SELECT count(*) FROM profiles WHERE org_handle = $1`, org).Scan(&count))
	return count
}

// failProfileUpdatesOf makes every update of a profile row of the organization
// fail, until the function it returns is called. A merge updates the master row
// last, so the fault lands after the merge has written the references.
func failProfileUpdatesOf(t *testing.T, org string) func() {

	t.Helper()
	name := "fail_updates_" + uuid.New().String()[:8]
	var create, drop []string
	if suiteDBType == database.TypeSQLite {
		create = []string{fmt.Sprintf(`CREATE TRIGGER %s BEFORE UPDATE ON profiles WHEN OLD.org_handle = '%s'
			BEGIN SELECT RAISE(ABORT, 'injected failure'); END`, name, org)}
		drop = []string{fmt.Sprintf(`DROP TRIGGER IF EXISTS %s`, name)}
	} else {
		create = []string{
			fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger AS $$
				BEGIN RAISE EXCEPTION 'injected failure'; END; $$ LANGUAGE plpgsql`, name),
			fmt.Sprintf(`CREATE TRIGGER %s BEFORE UPDATE ON profiles FOR EACH ROW
				WHEN (OLD.org_handle = '%s') EXECUTE FUNCTION %s()`, name, org, name),
		}
		drop = []string{
			fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON profiles`, name),
			fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, name),
		}
	}
	for _, statement := range create {
		_, err := suiteDB.Exec(statement)
		require.NoError(t, err)
	}

	removed := false
	remove := func() {
		if removed {
			return
		}
		removed = true
		for _, statement := range drop {
			_, err := suiteDB.Exec(statement)
			require.NoError(t, err)
		}
	}
	t.Cleanup(remove)
	return remove
}
