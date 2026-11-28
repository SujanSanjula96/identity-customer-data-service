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

package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	dbmodel "github.com/wso2/identity-customer-data-service/internal/system/database/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

const retention = 90 * 24 * time.Hour

type temporaryEnv struct {
	ctx    context.Context
	orgID  string
	cutoff time.Time
	old    time.Time
	recent time.Time
}

func newTemporaryEnv(t *testing.T, handle string) *temporaryEnv {
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
	now := time.Now().UTC()
	return &temporaryEnv{
		ctx:    context.Background(),
		orgID:  setup.TestOrg(t, handle).ID,
		cutoff: now.Add(-retention),
		old:    now.Add(-retention - 24*time.Hour),
		recent: now.Add(-24 * time.Hour),
	}
}

// add stores a profile with an active cookie. The status decides what kind of profile it is.
func (e *temporaryEnv) add(t *testing.T, orgID, id, userID string, updatedAt time.Time, status model.ProfileStatus) {
	t.Helper()
	require.NoError(t, InsertProfile(e.ctx, orgID, model.Profile{
		ProfileID:     id,
		OrgID:         orgID,
		UserID:        userID,
		CreatedAt:     updatedAt,
		UpdatedAt:     updatedAt,
		ProfileStatus: &status,
	}))
	require.NoError(t, CreateProfileCookie(e.ctx, model.ProfileCookie{
		CookieID: "cookie-" + id, ProfileID: id, IsActive: true,
	}))
}

func (e *temporaryEnv) exists(t *testing.T, orgID, id string) bool {
	t.Helper()
	profile, err := GetProfile(e.ctx, id, orgID)
	require.NoError(t, err)
	return profile != nil
}

func (e *temporaryEnv) cookieExists(t *testing.T, id string) bool {
	t.Helper()
	cookie, err := GetProfileCookieByProfileID(e.ctx, id)
	require.NoError(t, err)
	return cookie != nil
}

func (e *temporaryEnv) count(t *testing.T, table string) int {
	t.Helper()
	client, err := provider.NewDBProvider().GetDBClient()
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	rows, err := client.ExecuteQueryContext(e.ctx, dbmodel.DBQuery{ID: "test-count",
		Query: fmt.Sprintf("SELECT COUNT(*) AS n FROM %s", table)})
	require.NoError(t, err)
	return int(rows[0]["n"].(int64))
}

func (e *temporaryEnv) exec(t *testing.T, statement string, args ...interface{}) {
	t.Helper()
	client, err := provider.NewDBProvider().GetDBClient()
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	_, err = client.ExecuteQueryContext(e.ctx, dbmodel.DBQuery{ID: "test-exec", Query: statement}, args...)
	require.NoError(t, err)
}

var (
	master = model.ProfileStatus{IsReferenceProfile: true, ListProfile: true}
)

func TestExpiredTemporaryProfilesAreDeletedWithTheirCookies(t *testing.T) {
	env := newTemporaryEnv(t, "acme-anon-1")
	env.add(t, env.orgID, "anon-old", "", env.old, master)
	env.add(t, env.orgID, "anon-recent", "", env.recent, master)

	deleted, err := DeleteExpiredTemporaryProfiles(env.ctx, env.orgID, env.cutoff, 100)

	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
	assert.False(t, env.exists(t, env.orgID, "anon-old"), "the expired temporary profile is deleted")
	assert.False(t, env.cookieExists(t, "anon-old"), "its cookie goes with it, active or not")
	assert.True(t, env.exists(t, env.orgID, "anon-recent"), "a recent profile stays")
	assert.True(t, env.cookieExists(t, "anon-recent"))
	assert.Equal(t, 1, env.count(t, "profile_reference"), "the reference row of the deleted profile is gone too")
}

func TestIdentifiedAndMergedProfilesAreNeverDeleted(t *testing.T) {
	env := newTemporaryEnv(t, "acme-anon-2")
	env.add(t, env.orgID, "identified", "user-1", env.old, master)
	env.add(t, env.orgID, "parent", "user-2", env.old, master)
	env.add(t, env.orgID, "child", "", env.old, model.ProfileStatus{ReferenceProfileID: "parent"})
	env.add(t, env.orgID, "anon-master", "", env.old, master)
	env.add(t, env.orgID, "anon-child", "", env.old, model.ProfileStatus{ReferenceProfileID: "anon-master"})
	env.add(t, env.orgID, "waiting", "", env.old, model.ProfileStatus{IsWaitingOnUser: true})

	deleted, err := DeleteExpiredTemporaryProfiles(env.ctx, env.orgID, env.cutoff, 100)

	require.NoError(t, err)
	assert.Zero(t, deleted)
	for _, id := range []string{"identified", "parent", "child", "anon-master", "anon-child", "waiting"} {
		assert.True(t, env.exists(t, env.orgID, id), "%s is not a temporary profile and must stay", id)
		assert.True(t, env.cookieExists(t, id), "the cookie of %s must stay", id)
	}
}

func TestTemporaryProfilesOfOtherOrganizationsAreKept(t *testing.T) {
	env := newTemporaryEnv(t, "acme-anon-3")
	other := setup.TestOrg(t, "globex-anon-3").ID
	env.add(t, env.orgID, "mine", "", env.old, master)
	env.add(t, other, "theirs", "", env.old, master)

	deleted, err := DeleteExpiredTemporaryProfiles(env.ctx, env.orgID, env.cutoff, 100)

	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
	assert.True(t, env.exists(t, other, "theirs"))
}

func TestRejectionsOfDeletedProfilesAreRemoved(t *testing.T) {
	env := newTemporaryEnv(t, "acme-anon-4")
	env.add(t, env.orgID, "anon-a", "", env.old, master)
	env.add(t, env.orgID, "keep-b", "user-b", env.old, master)
	env.add(t, env.orgID, "keep-c", "user-c", env.old, master)
	insert := `INSERT INTO rejection_pairs (id, org_id, profile_id_1, profile_id_2) VALUES ($1, $2, $3, $4)`
	env.exec(t, insert, "r1", env.orgID, "anon-a", "keep-b")
	env.exec(t, insert, "r2", env.orgID, "keep-b", "keep-c")

	deleted, err := DeleteExpiredTemporaryProfiles(env.ctx, env.orgID, env.cutoff, 100)

	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
	assert.Equal(t, 1, env.count(t, "rejection_pairs"), "only the rejection that names the deleted profile goes")
}

func TestDeletionWorksInBatches(t *testing.T) {
	env := newTemporaryEnv(t, "acme-anon-5")
	for i := 0; i < 5; i++ {
		env.add(t, env.orgID, fmt.Sprintf("anon-%d", i), "", env.old.Add(time.Duration(i)*time.Minute), master)
	}

	first, err := DeleteExpiredTemporaryProfiles(env.ctx, env.orgID, env.cutoff, 2)
	require.NoError(t, err)
	assert.Equal(t, 2, first, "one batch deletes at most the batch size")

	second, err := DeleteExpiredTemporaryProfiles(env.ctx, env.orgID, env.cutoff, 2)
	require.NoError(t, err)
	assert.Equal(t, 2, second)

	third, err := DeleteExpiredTemporaryProfiles(env.ctx, env.orgID, env.cutoff, 2)
	require.NoError(t, err)
	assert.Equal(t, 1, third)

	none, err := DeleteExpiredTemporaryProfiles(env.ctx, env.orgID, env.cutoff, 2)
	require.NoError(t, err)
	assert.Zero(t, none)
}

func TestProfileOrgIDsListEveryOrganizationWithProfiles(t *testing.T) {
	env := newTemporaryEnv(t, "acme-anon-6")
	other := setup.TestOrg(t, "globex-anon-6").ID
	env.add(t, env.orgID, "p1", "", env.recent, master)
	env.add(t, env.orgID, "p2", "", env.recent, master)
	env.add(t, other, "p3", "", env.recent, master)

	orgIDs, err := ListProfileOrgIDs(env.ctx)

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{env.orgID, other}, orgIDs)
}
