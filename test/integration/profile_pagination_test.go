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

	"github.com/stretchr/testify/require"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	dbmodel "github.com/wso2/identity-customer-data-service/internal/system/database/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
)

// Test_ProfileCursorPagination covers keyset pagination, which is the part of
// the data access layer most sensitive to how a datasource stores and compares
// timestamps. It runs against whichever datasource CDS_TEST_DB selects.
func Test_ProfileCursorPagination(t *testing.T) {

	const (
		totalProfiles = 7
		pageSize      = 3
	)

	profileSvc := profileService.GetProfilesService()
	org := fmt.Sprintf("pagination-org-%d", time.Now().UnixNano())

	for i := 0; i < totalProfiles; i++ {
		_, err := profileSvc.CreateProfile(context.Background(), profileModel.ProfileRequest{}, org)
		require.NoError(t, err, "failed to create profile %d", i)
	}

	// Give three profiles the same creation time. Sharing a timestamp is what
	// makes the profile id part of the cursor load-bearing, and it is the case
	// most likely to differ between datasources.
	tied := forceSharedCreatedAt(t, org, 3)
	require.Len(t, tied, 3, "expected three profiles to share a creation time")

	t.Run("Paging forward visits every profile exactly once", func(t *testing.T) {
		seen := make([]string, 0, totalProfiles)
		visited := make(map[string]bool, totalProfiles)

		var cursor *profileModel.ProfileCursor
		for page := 0; ; page++ {
			require.LessOrEqual(t, page, totalProfiles, "paging did not terminate")

			profiles, hasMore, err := profileSvc.GetAllProfilesCursor(context.Background(), org, pageSize, cursor)
			require.NoError(t, err)
			require.NotEmpty(t, profiles, "page %d is empty", page)
			require.LessOrEqual(t, len(profiles), pageSize, "page %d is over the limit", page)

			for _, profile := range profiles {
				require.False(t, visited[profile.ProfileId],
					"profile %s was returned on more than one page", profile.ProfileId)
				visited[profile.ProfileId] = true
				seen = append(seen, profile.ProfileId)
			}

			requireDescending(t, profiles)

			if !hasMore {
				break
			}
			last := profiles[len(profiles)-1]
			cursor = &profileModel.ProfileCursor{
				CreatedAt: last.Meta.CreatedAt,
				ProfileId: last.ProfileId,
				Direction: "next",
			}
		}

		require.Len(t, seen, totalProfiles, "paging did not return every profile")
	})

	t.Run("Paging backward returns the previous page", func(t *testing.T) {
		firstPage, hasMore, err := profileSvc.GetAllProfilesCursor(context.Background(), org, pageSize, nil)
		require.NoError(t, err)
		require.True(t, hasMore)
		require.Len(t, firstPage, pageSize)

		last := firstPage[len(firstPage)-1]
		secondPage, _, err := profileSvc.GetAllProfilesCursor(context.Background(), org, pageSize, &profileModel.ProfileCursor{
			CreatedAt: last.Meta.CreatedAt,
			ProfileId: last.ProfileId,
			Direction: "next",
		})
		require.NoError(t, err)
		require.Len(t, secondPage, pageSize)

		// Stepping back from the start of the second page must reproduce the
		// first page.
		first := secondPage[0]
		backwards, _, err := profileSvc.GetAllProfilesCursor(context.Background(), org, pageSize, &profileModel.ProfileCursor{
			CreatedAt: first.Meta.CreatedAt,
			ProfileId: first.ProfileId,
			Direction: "prev",
		})
		require.NoError(t, err)
		require.Len(t, backwards, pageSize)

		require.ElementsMatch(t, profileIds(firstPage), profileIds(backwards),
			"paging back did not return the first page")
	})

	t.Run("A cursor survives encoding", func(t *testing.T) {
		page, _, err := profileSvc.GetAllProfilesCursor(context.Background(), org, pageSize, nil)
		require.NoError(t, err)
		require.NotEmpty(t, page)

		last := page[len(page)-1]
		original := profileModel.ProfileCursor{
			CreatedAt: last.Meta.CreatedAt,
			ProfileId: last.ProfileId,
			Direction: "next",
		}

		decoded, err := profileModel.DecodeProfileCursor(profileModel.EncodeProfileCursor(original))
		require.NoError(t, err)
		require.NotNil(t, decoded)
		require.Equal(t, original.ProfileId, decoded.ProfileId)
		require.Equal(t, original.Direction, decoded.Direction)
		require.True(t, original.CreatedAt.Equal(decoded.CreatedAt),
			"expected %s, got %s", original.CreatedAt, decoded.CreatedAt)

		fromEncoded, _, err := profileSvc.GetAllProfilesCursor(context.Background(), org, pageSize, decoded)
		require.NoError(t, err)

		fromOriginal, _, err := profileSvc.GetAllProfilesCursor(context.Background(), org, pageSize, &original)
		require.NoError(t, err)

		require.Equal(t, profileIds(fromOriginal), profileIds(fromEncoded),
			"an encoded cursor selected a different page")
	})
}

// forceSharedCreatedAt gives the oldest count profiles of an organization the same
// creation time, and returns their ids.
func forceSharedCreatedAt(t *testing.T, org string, count int) []string {

	t.Helper()

	dbClient, err := provider.NewDBProvider().GetDBClient()
	require.NoError(t, err)
	defer dbClient.Close()

	rows, err := dbClient.ExecuteQueryContext(context.Background(), dbmodel.DBQuery{
		ID:    "TEST-PAG-01",
		Query: `SELECT profile_id FROM profiles WHERE org_handle = $1 ORDER BY created_at ASC`,
	}, org)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(rows), count)

	shared := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)

	ids := make([]string, 0, count)
	for _, row := range rows[:count] {
		profileId, ok := row["profile_id"].(string)
		require.True(t, ok, "unexpected profile_id type %T", row["profile_id"])

		_, err := dbClient.ExecuteQueryContext(context.Background(), dbmodel.DBQuery{
			ID:    "TEST-PAG-02",
			Query: `UPDATE profiles SET created_at = $1 WHERE profile_id = $2`,
		}, shared, profileId)
		require.NoError(t, err)

		ids = append(ids, profileId)
	}

	return ids
}

// requireDescending asserts that a page is ordered newest first, with the
// profile id breaking ties.
func requireDescending(t *testing.T, profiles []profileModel.ProfileResponse) {

	t.Helper()

	for i := 1; i < len(profiles); i++ {
		previous, current := profiles[i-1], profiles[i]
		if previous.Meta.CreatedAt.Equal(current.Meta.CreatedAt) {
			require.Greater(t, previous.ProfileId, current.ProfileId,
				"profiles sharing a creation time are not ordered by profile id")
			continue
		}
		require.True(t, previous.Meta.CreatedAt.After(current.Meta.CreatedAt),
			"expected %s to be newer than %s", previous.Meta.CreatedAt, current.Meta.CreatedAt)
	}
}

func profileIds(profiles []profileModel.ProfileResponse) []string {

	ids := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		ids = append(ids, profile.ProfileId)
	}

	return ids
}
