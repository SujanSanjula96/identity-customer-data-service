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
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// Test_TemporaryVisitWithoutAUsableCookie checks what a site visit does when the browser's profile
// cookie cannot identify a profile: the cookie names nothing, or its cookie is inactive.
func Test_TemporaryVisitWithoutAUsableCookie(t *testing.T) {
	ctx := context.Background()
	f := setUpOrgScopeFixture(t)
	profiles := profileService.GetProfilesService()

	visit := func(cookieValue string) (int, profileModel.ProfileResponse) {
		t.Helper()
		var cookie *http.Cookie
		if cookieValue != "" {
			cookie = &http.Cookie{Name: constants.ProfileCookie, Value: cookieValue}
		}
		status, body := f.call(t, f.ownerOrg, f.ownerToken, http.MethodPost, "/profiles", `{}`, cookie)
		var response profileModel.ProfileResponse
		require.NoError(t, json.Unmarshal(body, &response), string(body))
		return status, response
	}

	status, existing := visit("")
	require.Equal(t, http.StatusCreated, status)
	require.NotEmpty(t, existing.AnonymousTracker)

	t.Run("Active_cookie_returns_its_profile", func(t *testing.T) {
		status, response := visit(existing.AnonymousTracker)

		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, existing.ProfileID, response.ProfileID)
		assert.Equal(t, existing.AnonymousTracker, response.AnonymousTracker)
	})

	t.Run("Invalid_cookie_creates_a_new_temporary_profile", func(t *testing.T) {
		invalid := uuid.New().String()

		status, response := visit(invalid)

		assert.Equal(t, http.StatusCreated, status)
		assert.NotEqual(t, existing.ProfileID, response.ProfileID)
		assert.NotEmpty(t, response.AnonymousTracker)
		assert.NotEqual(t, invalid, response.AnonymousTracker, "the invalid cookie is replaced, not reused")
		cookie, err := profileStore.GetProfileCookie(ctx, invalid, f.owner.ID)
		require.NoError(t, err)
		assert.Nil(t, cookie, "the invalid cookie is not stored")
	})

	t.Run("Inactive_cookie_creates_a_new_temporary_profile", func(t *testing.T) {
		status, stale := visit("")
		require.Equal(t, http.StatusCreated, status)
		require.NoError(t, profiles.UpdateCookieStatusByCookieID(ctx, stale.AnonymousTracker, false))

		status, response := visit(stale.AnonymousTracker)

		assert.Equal(t, http.StatusCreated, status)
		assert.NotEqual(t, stale.ProfileID, response.ProfileID, "an inactive cookie does not resolve its profile")
		assert.NotEqual(t, stale.AnonymousTracker, response.AnonymousTracker)
		old, err := profileStore.GetProfileCookie(ctx, stale.AnonymousTracker, f.owner.ID)
		require.NoError(t, err)
		require.NotNil(t, old)
		assert.False(t, old.IsActive, "the inactive cookie stays inactive")
		kept, err := profileStore.GetProfile(ctx, stale.ProfileID, f.owner.ID)
		require.NoError(t, err)
		assert.NotNil(t, kept, "the profile behind the inactive cookie is left as it was")
	})
}
