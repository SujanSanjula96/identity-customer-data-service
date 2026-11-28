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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adminConfigModel "github.com/wso2/identity-customer-data-service/internal/admin_config/model"
	adminConfigStore "github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

func Test_TemporaryProfileCleanup(t *testing.T) {
	ctx := context.Background()
	org := setup.TestOrg(t, fmt.Sprintf("anon-cleanup-%d", time.Now().UnixNano()))
	now := time.Now().UTC()
	cutoff := now.Add(-90 * 24 * time.Hour)
	old := cutoff.Add(-24 * time.Hour)
	recent := now.Add(-24 * time.Hour)
	master := profileModel.ProfileStatus{IsReferenceProfile: true, ListProfile: true}

	add := func(id, userID string, updatedAt time.Time, status profileModel.ProfileStatus) {
		t.Helper()
		require.NoError(t, profileStore.InsertProfile(ctx, org.ID, profileModel.Profile{
			ProfileID: id, OrgID: org.ID, UserID: userID, CreatedAt: updatedAt, UpdatedAt: updatedAt,
			ProfileStatus: &status,
		}))
		require.NoError(t, profileStore.CreateProfileCookie(ctx, profileModel.ProfileCookie{
			CookieID: "cookie-" + id, ProfileID: id, IsActive: true,
		}))
	}
	exec := func(statement string, args ...interface{}) {
		t.Helper()
		_, err := setup.ExecStatement(suiteDB, suiteDBType, statement, args...)
		require.NoError(t, err)
	}
	count := func(statement string, args ...interface{}) int {
		t.Helper()
		stmt, err := setup.PrepareStatement(suiteDB, suiteDBType, statement)
		require.NoError(t, err)
		defer func() { _ = stmt.Close() }()
		var n int
		require.NoError(t, stmt.QueryRow(args...).Scan(&n))
		return n
	}
	exists := func(id string) bool {
		t.Helper()
		profile, err := profileStore.GetProfile(ctx, id, org.ID)
		require.NoError(t, err)
		return profile != nil
	}
	cookieExists := func(id string) bool {
		t.Helper()
		cookie, err := profileStore.GetProfileCookieByProfileID(ctx, id)
		require.NoError(t, err)
		return cookie != nil
	}

	add("anon-old-1", "", old, master)
	add("anon-old-2", "", old, master)
	add("anon-recent", "", recent, master)
	add("identified-old", "user-1", old, master)
	add("anon-parent", "", old, master)
	add("anon-child", "", old, profileModel.ProfileStatus{ReferenceProfileID: "anon-parent"})

	exec(`INSERT INTO rejection_pairs (id, org_id, profile_id_1, profile_id_2) VALUES ($1, $2, $3, $4)`,
		"rejection-1", org.ID, "anon-old-1", "identified-old")
	exec(`INSERT INTO review_tasks (id, org_id, incoming_profile_id, candidate_profile_id, match_score, status)
		VALUES ($1, $2, $3, $4, $5, $6)`, "task-1", org.ID, "identified-old", "anon-old-2", 0.9, "PENDING")

	deleted, err := profileStore.DeleteExpiredTemporaryProfiles(ctx, org.ID, cutoff, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted, "one batch deletes at most the batch size")
	deleted, err = profileStore.DeleteExpiredTemporaryProfiles(ctx, org.ID, cutoff, 100)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted, "the second expired temporary profile goes in the next batch")
	deleted, err = profileStore.DeleteExpiredTemporaryProfiles(ctx, org.ID, cutoff, 100)
	require.NoError(t, err)
	assert.Zero(t, deleted, "nothing else qualifies")

	for _, id := range []string{"anon-old-1", "anon-old-2"} {
		assert.False(t, exists(id), "%s is expired and temporary", id)
		assert.False(t, cookieExists(id), "the cookie of %s goes with it", id)
		assert.Zero(t, count(`SELECT COUNT(*) FROM profile_reference WHERE profile_id = $1`, id),
			"the reference row of %s goes with it", id)
	}
	for _, id := range []string{"anon-recent", "identified-old", "anon-parent", "anon-child"} {
		assert.True(t, exists(id), "%s must stay", id)
		assert.True(t, cookieExists(id), "the cookie of %s must stay", id)
	}
	assert.Zero(t, count(`SELECT COUNT(*) FROM rejection_pairs WHERE org_id = $1`, org.ID),
		"the rejection that names a deleted profile goes")
	assert.Zero(t, count(`SELECT COUNT(*) FROM review_tasks WHERE org_id = $1`, org.ID),
		"the review task that names a deleted profile as candidate goes")
}

func Test_TemporaryProfileCleanupSettingsAreStoredAndListed(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	full := setup.TestOrg(t, fmt.Sprintf("anon-settings-full-%d", suffix))
	partial := setup.TestOrg(t, fmt.Sprintf("anon-settings-partial-%d", suffix))
	unset := setup.TestOrg(t, fmt.Sprintf("anon-settings-unset-%d", suffix))

	require.NoError(t, adminConfigStore.UpdateAdminConfig(ctx, adminConfigModel.AdminConfig{
		OrgID: full.ID, CDSEnabled: true, TemporaryProfileRetentionDays: 30,
		TemporaryProfileCleanupIntervalDays: 6,
	}, full.ID))
	require.NoError(t, adminConfigStore.UpdateAdminConfig(ctx, adminConfigModel.AdminConfig{
		OrgID: partial.ID, CDSEnabled: true, TemporaryProfileRetentionDays: 400,
	}, partial.ID))
	require.NoError(t, adminConfigStore.UpdateAdminConfig(ctx, adminConfigModel.AdminConfig{
		OrgID: unset.ID, CDSEnabled: true,
	}, unset.ID))

	settings, err := adminConfigStore.ListTemporaryProfileCleanupSettings(ctx)
	require.NoError(t, err)

	assert.Equal(t, adminConfigModel.TemporaryProfileCleanupSettings{RetentionDays: 30, IntervalDays: 6},
		settings[full.ID])
	assert.Equal(t, adminConfigModel.TemporaryProfileCleanupSettings{RetentionDays: 400}, settings[partial.ID],
		"a setting that was not stored is 0")
	assert.Equal(t, adminConfigModel.TemporaryProfileCleanupSettings{}, settings[unset.ID],
		"an organization that stored none has none")

	stored, err := adminConfigStore.GetAdminConfig(ctx, full.ID)
	require.NoError(t, err)
	assert.Equal(t, 6, stored.TemporaryProfileCleanupIntervalDays)
}
