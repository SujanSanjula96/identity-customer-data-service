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
	"database/sql"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaStore "github.com/wso2/identity-customer-data-service/internal/profile_schema/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

// The profile workers are not started in these tests, so the profiles that the service queues
// for unification are dropped and nothing runs in the background.

var profileOrgCounter atomic.Int64

type profileEnv struct {
	ctx    context.Context
	db     *sql.DB
	orgID  string
	handle string
	svc    *ProfilesService
}

func newProfileEnv(t *testing.T) *profileEnv {
	t.Helper()
	require.NoError(t, log.Init("ERROR"))
	setup.EnableOrganizationManagement()
	testDB, err := setup.SetupTestSQLite()
	require.NoError(t, err)
	provider.SetTestDB(testDB.DB, database.TypeSQLite)
	t.Cleanup(func() {
		provider.SetTestDB(nil, "")
		testDB.Terminate()
	})

	handle := fmt.Sprintf("acme-%d", profileOrgCounter.Add(1))
	env := &profileEnv{ctx: context.Background(), db: testDB.DB, handle: handle, orgID: setup.TestOrgID(handle),
		svc: &ProfilesService{}}
	require.NoError(t, setup.InsertTestOrganization(testDB.DB, env.orgID, handle))

	env.seedSchema(t, constants.IdentityAttributes, "email", constants.StringDataType, constants.MutabilityReadWrite)
	env.seedSchema(t, constants.Traits, "city", constants.StringDataType, constants.MutabilityReadWrite)
	env.seedSchema(t, constants.Traits, "age", constants.IntegerDataType, constants.MutabilityReadWrite)
	env.seedSchema(t, constants.Traits, "ssn", constants.StringDataType, constants.MutabilityWriteOnce)
	env.seedSchema(t, constants.Traits, "tier", constants.StringDataType, constants.MutabilityReadWrite)
	return env
}

func (e *profileEnv) seedSchema(t *testing.T, scope, name, valueType, mutability string) {
	t.Helper()
	full := scope + "." + name
	err := schemaStore.AddProfileSchemaAttributesForScope(e.ctx, []schemaModel.ProfileSchemaAttribute{{
		AttributeID:   "attr-" + full,
		AttributeName: full,
		Scope:         scope,
		ValueType:     valueType,
		MergeStrategy: "overwrite",
		Mutability:    mutability,
	}}, scope, e.orgID)
	require.NoError(t, err)
}

func (e *profileEnv) create(t *testing.T, request profileModel.ProfileRequest) *profileModel.ProfileResponse {
	t.Helper()
	created, err := e.svc.CreateProfile(e.ctx, request, e.orgID)
	require.NoError(t, err)
	require.NotNil(t, created)
	return created
}

func (e *profileEnv) createWithCity(t *testing.T, city string) *profileModel.ProfileResponse {
	t.Helper()
	return e.create(t, profileModel.ProfileRequest{Traits: map[string]interface{}{"city": city}})
}

// addChild stores a profile that was merged into the parent.
func (e *profileEnv) addChild(t *testing.T, parentID, id string) {
	t.Helper()
	now := time.Now().UTC()
	err := profileStore.InsertProfile(e.ctx, e.orgID, profileModel.Profile{
		ProfileID: id,
		OrgID:     e.orgID,
		CreatedAt: now,
		UpdatedAt: now,
		ProfileStatus: &profileModel.ProfileStatus{
			ReferenceProfileID: parentID,
			ReferenceReason:    "email",
		},
	})
	require.NoError(t, err)
}

func requireStatus(t *testing.T, err error, status int) *errors2.ClientError {
	t.Helper()
	var clientError *errors2.ClientError
	require.ErrorAs(t, err, &clientError)
	require.Equal(t, status, clientError.StatusCode)
	return clientError
}

func TestCreateProfileStoresAndReturnsTheProfile(t *testing.T) {
	env := newProfileEnv(t)

	created := env.create(t, profileModel.ProfileRequest{
		UserID:             "u-1",
		IdentityAttributes: map[string]interface{}{"email": "a@b.c"},
		Traits:             map[string]interface{}{"city": "Colombo", "age": float64(30)},
	})

	assert.NotEmpty(t, created.ProfileID)
	assert.Equal(t, "u-1", created.UserID)
	assert.Equal(t, "a@b.c", created.IdentityAttributes["email"])
	assert.Equal(t, "Colombo", created.Traits["city"])
	assert.Contains(t, created.Meta.Location, created.ProfileID)
	assert.Contains(t, created.Meta.Location, env.handle)
	assert.False(t, created.Meta.CreatedAt.IsZero())
	assert.Nil(t, created.MergedFrom)
	assert.Nil(t, created.MergedTo)
}

func TestCreateProfileRejectsDataThatBreaksTheSchema(t *testing.T) {
	env := newProfileEnv(t)
	tests := map[string]profileModel.ProfileRequest{
		"an unknown trait":      {Traits: map[string]interface{}{"nope": "x"}},
		"a wrongly typed trait": {Traits: map[string]interface{}{"age": "old"}},
		"an unknown attribute":  {IdentityAttributes: map[string]interface{}{"nope": "x"}},
		"application data that is not an object": {
			ApplicationData: map[string]interface{}{"app-1": "oops"}},
	}
	for name, request := range tests {
		t.Run(name, func(t *testing.T) {
			created, err := env.svc.CreateProfile(env.ctx, request, env.orgID)

			assert.Nil(t, created)
			_ = requireStatus(t, err, http.StatusBadRequest)
		})
	}
}

func TestCreateProfileStoresApplicationData(t *testing.T) {
	env := newProfileEnv(t)
	env.seedSchemaForApp(t, "app-1")

	created := env.create(t, profileModel.ProfileRequest{
		ApplicationData: map[string]interface{}{"app-1": map[string]interface{}{"theme": "dark"}},
	})

	assert.Equal(t, map[string]map[string]interface{}{"app-1": {"theme": "dark"}}, created.ApplicationData)
}

func (e *profileEnv) seedSchemaForApp(t *testing.T, appID string) {
	t.Helper()
	full := constants.ApplicationData + ".theme"
	err := schemaStore.AddProfileSchemaAttributesForScope(e.ctx, []schemaModel.ProfileSchemaAttribute{{
		AttributeID:           "attr-" + appID + "-theme",
		AttributeName:         full,
		Scope:                 constants.ApplicationData,
		ApplicationIdentifier: appID,
		ValueType:             constants.StringDataType,
		MergeStrategy:         "overwrite",
		Mutability:            constants.MutabilityReadWrite,
	}}, constants.ApplicationData, e.orgID)
	require.NoError(t, err)
}

func TestGetProfile(t *testing.T) {
	env := newProfileEnv(t)
	created := env.createWithCity(t, "Colombo")

	got, err := env.svc.GetProfile(env.ctx, created.ProfileID, env.orgID)

	require.NoError(t, err)
	assert.Equal(t, created.ProfileID, got.ProfileID)
	assert.Equal(t, "Colombo", got.Traits["city"])

	_, err = env.svc.GetProfile(env.ctx, "missing", env.orgID)
	clientError := requireStatus(t, err, http.StatusNotFound)
	assert.Equal(t, errors2.ProfileNotFound.Code, clientError.Code)
}

func TestGetProfileIsScopedToTheOrganization(t *testing.T) {
	env := newProfileEnv(t)
	created := env.createWithCity(t, "Colombo")

	_, err := env.svc.GetProfile(env.ctx, created.ProfileID, "another-org")

	_ = requireStatus(t, err, http.StatusNotFound)
}

func TestFindProfileByUserId(t *testing.T) {
	env := newProfileEnv(t)
	env.create(t, profileModel.ProfileRequest{UserID: "u-7", Traits: map[string]interface{}{"city": "Kandy"}})

	got, err := env.svc.FindProfileByUserID(env.ctx, "u-7", env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "u-7", got.UserID)
	assert.Equal(t, "Kandy", got.Traits["city"])

	_, err = env.svc.FindProfileByUserID(env.ctx, "nobody", env.orgID)
	_ = requireStatus(t, err, http.StatusNotFound)
}

func TestAMergedProfileReadsAsItsMasterAndTheMasterListsIt(t *testing.T) {
	env := newProfileEnv(t)
	master := env.createWithCity(t, "Colombo")
	env.addChild(t, master.ProfileID, "child-1")

	child, err := env.svc.GetProfile(env.ctx, "child-1", env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "child-1", child.ProfileID)
	assert.Equal(t, "Colombo", child.Traits["city"])
	require.NotNil(t, child.MergedTo)
	assert.Equal(t, master.ProfileID, child.MergedTo.ProfileID)
	assert.Equal(t, "email", child.MergedTo.Reason)

	parent, err := env.svc.GetProfile(env.ctx, master.ProfileID, env.orgID)
	require.NoError(t, err)
	require.Len(t, parent.MergedFrom, 1)
	assert.Equal(t, "child-1", parent.MergedFrom[0].ProfileID)
}

func TestUpdateProfileReplacesTheProfileData(t *testing.T) {
	env := newProfileEnv(t)
	created := env.create(t, profileModel.ProfileRequest{
		UserID: "u-1",
		Traits: map[string]interface{}{"city": "Colombo", "tier": "gold"},
	})

	updated, err := env.svc.UpdateProfile(env.ctx, created.ProfileID, env.orgID, profileModel.ProfileRequest{
		UserID: "u-1",
		Traits: map[string]interface{}{"city": "Kandy"},
	})

	require.NoError(t, err)
	assert.Equal(t, "Kandy", updated.Traits["city"])
	// An update is a replacement: the trait that was not sent is gone.
	assert.NotContains(t, updated.Traits, "tier")
	assert.True(t, !updated.Meta.UpdatedAt.Before(created.Meta.UpdatedAt))

	fetched, err := env.svc.GetProfile(env.ctx, created.ProfileID, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "Kandy", fetched.Traits["city"])
}

func TestUpdateProfileRefusals(t *testing.T) {
	env := newProfileEnv(t)
	created := env.create(t, profileModel.ProfileRequest{Traits: map[string]interface{}{"ssn": "111"}})

	_, err := env.svc.UpdateProfile(env.ctx, "missing", env.orgID, profileModel.ProfileRequest{})
	_ = requireStatus(t, err, http.StatusNotFound)

	_, err = env.svc.UpdateProfile(env.ctx, created.ProfileID, env.orgID,
		profileModel.ProfileRequest{Traits: map[string]interface{}{"age": "old"}})
	_ = requireStatus(t, err, http.StatusBadRequest)

	// A write-once attribute cannot change once it has a value.
	_, err = env.svc.UpdateProfile(env.ctx, created.ProfileID, env.orgID,
		profileModel.ProfileRequest{Traits: map[string]interface{}{"ssn": "222"}})
	_ = requireStatus(t, err, http.StatusBadRequest)

	same, err := env.svc.UpdateProfile(env.ctx, created.ProfileID, env.orgID,
		profileModel.ProfileRequest{Traits: map[string]interface{}{"ssn": "111"}})
	require.NoError(t, err)
	assert.Equal(t, "111", same.Traits["ssn"])
}

func TestUpdatingAMergedProfileUpdatesItsMaster(t *testing.T) {
	env := newProfileEnv(t)
	master := env.createWithCity(t, "Colombo")
	env.addChild(t, master.ProfileID, "child-1")

	_, err := env.svc.UpdateProfile(env.ctx, "child-1", env.orgID,
		profileModel.ProfileRequest{Traits: map[string]interface{}{"city": "Galle"}})

	require.NoError(t, err)
	fetched, err := env.svc.GetProfile(env.ctx, master.ProfileID, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "Galle", fetched.Traits["city"])
}

func TestPatchProfileMergesIntoTheExistingData(t *testing.T) {
	env := newProfileEnv(t)
	created := env.create(t, profileModel.ProfileRequest{
		UserID:             "u-1",
		IdentityAttributes: map[string]interface{}{"email": "a@b.c"},
		Traits:             map[string]interface{}{"city": "Colombo", "tier": "gold"},
	})

	patched, err := env.svc.PatchProfile(env.ctx, created.ProfileID, env.orgID, map[string]interface{}{
		"traits": map[string]interface{}{"city": "Kandy"},
	})

	require.NoError(t, err)
	assert.Equal(t, "Kandy", patched.Traits["city"])
	assert.Equal(t, "gold", patched.Traits["tier"], "a trait that was not patched stays")
	assert.Equal(t, "a@b.c", patched.IdentityAttributes["email"])
	assert.Equal(t, "u-1", patched.UserID)
}

func TestPatchProfileCanPatchIdentityAttributesAndTheUserId(t *testing.T) {
	env := newProfileEnv(t)
	created := env.createWithCity(t, "Colombo")

	patched, err := env.svc.PatchProfile(env.ctx, created.ProfileID, env.orgID, map[string]interface{}{
		"user_id":             "u-9",
		"identity_attributes": map[string]interface{}{"email": "new@b.c"},
	})

	require.NoError(t, err)
	assert.Equal(t, "u-9", patched.UserID)
	assert.Equal(t, "new@b.c", patched.IdentityAttributes["email"])
	assert.Equal(t, "Colombo", patched.Traits["city"])
}

func TestPatchProfileRefusals(t *testing.T) {
	env := newProfileEnv(t)
	created := env.createWithCity(t, "Colombo")

	_, err := env.svc.PatchProfile(env.ctx, "missing", env.orgID, map[string]interface{}{})
	_ = requireStatus(t, err, http.StatusNotFound)

	_, err = env.svc.PatchProfile(env.ctx, created.ProfileID, env.orgID, map[string]interface{}{
		"traits": map[string]interface{}{"age": "old"},
	})
	_ = requireStatus(t, err, http.StatusBadRequest)

	_, err = env.svc.PatchProfile(env.ctx, created.ProfileID, env.orgID, map[string]interface{}{
		"traits": map[string]interface{}{"nope": "x"},
	})
	_ = requireStatus(t, err, http.StatusBadRequest)
}

func TestPatchingAMergedProfilePatchesItsMasterData(t *testing.T) {
	env := newProfileEnv(t)
	master := env.create(t, profileModel.ProfileRequest{
		Traits: map[string]interface{}{"city": "Colombo", "tier": "gold"},
	})
	env.addChild(t, master.ProfileID, "child-1")

	_, err := env.svc.PatchProfile(env.ctx, "child-1", env.orgID, map[string]interface{}{
		"traits": map[string]interface{}{"city": "Galle"},
	})

	require.NoError(t, err)
	fetched, err := env.svc.GetProfile(env.ctx, master.ProfileID, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "Galle", fetched.Traits["city"])
	assert.Equal(t, "gold", fetched.Traits["tier"], "the master's other data is kept")
}

func TestDeleteProfile(t *testing.T) {
	env := newProfileEnv(t)
	created := env.createWithCity(t, "Colombo")

	require.NoError(t, env.svc.DeleteProfile(env.ctx, created.ProfileID, env.orgID))

	_, err := env.svc.GetProfile(env.ctx, created.ProfileID, env.orgID)
	_ = requireStatus(t, err, http.StatusNotFound)
	// Deleting a profile that is gone is not an error.
	assert.NoError(t, env.svc.DeleteProfile(env.ctx, created.ProfileID, env.orgID))
	assert.NoError(t, env.svc.DeleteProfile(env.ctx, "never-existed", env.orgID))
}

func TestDeletingAMasterDeletesItsMergedProfiles(t *testing.T) {
	env := newProfileEnv(t)
	master := env.createWithCity(t, "Colombo")
	env.addChild(t, master.ProfileID, "child-1")
	env.addChild(t, master.ProfileID, "child-2")

	require.NoError(t, env.svc.DeleteProfile(env.ctx, master.ProfileID, env.orgID))

	for _, id := range []string{master.ProfileID, "child-1", "child-2"} {
		_, err := env.svc.GetProfile(env.ctx, id, env.orgID)
		_ = requireStatus(t, err, http.StatusNotFound)
	}
}

func TestDeletingTheOnlyMergedProfileDeletesItsMasterToo(t *testing.T) {
	env := newProfileEnv(t)
	master := env.createWithCity(t, "Colombo")
	env.addChild(t, master.ProfileID, "child-1")

	require.NoError(t, env.svc.DeleteProfile(env.ctx, "child-1", env.orgID))

	for _, id := range []string{master.ProfileID, "child-1"} {
		_, err := env.svc.GetProfile(env.ctx, id, env.orgID)
		_ = requireStatus(t, err, http.StatusNotFound)
	}
}

func TestDeletingOneOfSeveralMergedProfilesKeepsTheMaster(t *testing.T) {
	env := newProfileEnv(t)
	master := env.createWithCity(t, "Colombo")
	env.addChild(t, master.ProfileID, "child-1")
	env.addChild(t, master.ProfileID, "child-2")

	require.NoError(t, env.svc.DeleteProfile(env.ctx, "child-1", env.orgID))

	_, err := env.svc.GetProfile(env.ctx, "child-1", env.orgID)
	_ = requireStatus(t, err, http.StatusNotFound)
	parent, err := env.svc.GetProfile(env.ctx, master.ProfileID, env.orgID)
	require.NoError(t, err)
	require.Len(t, parent.MergedFrom, 1)
	assert.Equal(t, "child-2", parent.MergedFrom[0].ProfileID)
}

func TestGetAllProfilesCursorPagesThroughTheMasters(t *testing.T) {
	env := newProfileEnv(t)
	created := map[string]bool{}
	for i := 0; i < 5; i++ {
		created[env.createWithCity(t, fmt.Sprintf("city-%d", i)).ProfileID] = true
	}

	seen := map[string]bool{}
	var cursor *profileModel.ProfileCursor
	for pages := 0; pages < 10; pages++ {
		page, hasMore, err := env.svc.GetAllProfilesCursor(env.ctx, env.orgID, 2, cursor)
		require.NoError(t, err)
		require.LessOrEqual(t, len(page), 2)
		for _, profile := range page {
			assert.False(t, seen[profile.ProfileID], "no profile appears twice")
			seen[profile.ProfileID] = true
		}
		if !hasMore {
			break
		}
		last := page[len(page)-1]
		cursor = &profileModel.ProfileCursor{CreatedAt: last.Meta.CreatedAt, ProfileID: last.ProfileID,
			Direction: "next"}
	}

	assert.Equal(t, created, seen)
}

func TestGetAllProfilesCursorOfAnEmptyOrganizationIsEmpty(t *testing.T) {
	env := newProfileEnv(t)

	page, hasMore, err := env.svc.GetAllProfilesCursor(env.ctx, env.orgID, 10, nil)

	require.NoError(t, err)
	assert.Empty(t, page)
	assert.False(t, hasMore)
}

func TestGetAllProfilesCursorListsMastersNotMergedProfiles(t *testing.T) {
	env := newProfileEnv(t)
	master := env.createWithCity(t, "Colombo")
	env.addChild(t, master.ProfileID, "child-1")

	page, _, err := env.svc.GetAllProfilesCursor(env.ctx, env.orgID, 10, nil)

	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, master.ProfileID, page[0].ProfileID)
	require.Len(t, page[0].MergedFrom, 1)
}
