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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	consentStore "github.com/wso2/identity-customer-data-service/internal/consent/store"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
)

// Test_ProfileConsentTransaction runs one transaction through its successful
// exit and through a failed one. UpdateProfileConsents deletes every consent
// of a profile and inserts the new set, so a failed insert that did not roll
// back would leave the profile with no consents at all.
func Test_ProfileConsentTransaction(t *testing.T) {

	ctx := context.Background()
	org := fmt.Sprintf("tx-org-%d", time.Now().UnixNano())

	profileId := uuid.New().String()
	require.NoError(t, profileStore.InsertProfile(ctx, profileModel.Profile{
		ProfileId: profileId,
		OrgHandle: org,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		ProfileStatus: &profileModel.ProfileStatus{
			ListProfile: true,
		},
	}))

	category := fmt.Sprintf("tx-category-%d", time.Now().UnixNano())
	require.NoError(t, consentStore.AddConsentCategory(ctx, consentModel.ConsentCategory{
		CategoryName:       "Transaction test",
		CategoryIdentifier: category,
		OrgHandle:          org,
		Purpose:            "profiling",
	}))

	accepted := []profileModel.ConsentRecord{
		{CategoryIdentifier: category, IsConsented: true, ConsentedAt: time.Now().UTC()},
	}

	t.Run("the successful path commits", func(t *testing.T) {
		require.NoError(t, profileStore.UpdateProfileConsents(ctx, profileId, accepted))

		stored, err := profileStore.GetProfileConsents(ctx, profileId)
		require.NoError(t, err)
		require.Len(t, stored, 1)
		require.Equal(t, category, stored[0].CategoryIdentifier)
	})

	t.Run("a failed statement rolls the whole transaction back", func(t *testing.T) {
		// The same category twice breaks the unique constraint on
		// (profile_id, category_id), so the second insert fails.
		duplicated := append(append([]profileModel.ConsentRecord{}, accepted...), accepted...)

		err := profileStore.UpdateProfileConsents(ctx, profileId, duplicated)
		require.Error(t, err)
		// Both datasources name the constraint the insert broke. Neither
		// mentions a rollback, because a rollback failure must not replace the
		// error the caller needs to see.
		require.Contains(t, strings.ToLower(err.Error()), "constraint",
			"the caller did not receive the error the statement produced")
		require.NotContains(t, strings.ToLower(err.Error()), "rollback")

		// The delete that ran first must be gone with the failed insert.
		stored, storeErr := profileStore.GetProfileConsents(ctx, profileId)
		require.NoError(t, storeErr)
		require.Len(t, stored, 1, "the rollback did not restore the consent the delete removed")
		require.Equal(t, category, stored[0].CategoryIdentifier)

		// A transaction that was not rolled back still holds its connection
		// and its locks, so the next write would wait for them. The deadline
		// keeps that wait from hanging the suite.
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		require.NoError(t, profileStore.UpdateProfileConsents(bounded, profileId, accepted),
			"the failed transaction did not release its connection")
	})

	t.Run("a cancelled caller rolls the transaction back", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()

		err := profileStore.UpdateProfileConsents(cancelled, profileId, nil)
		require.ErrorIs(t, err, context.Canceled)

		stored, storeErr := profileStore.GetProfileConsents(ctx, profileId)
		require.NoError(t, storeErr)
		require.Len(t, stored, 1, "a cancelled transaction changed the stored consents")
	})
}
