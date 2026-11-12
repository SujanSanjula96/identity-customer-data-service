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
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	consentStore "github.com/wso2/identity-customer-data-service/internal/consent/store"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

func TestProfileCookieLifecycle(t *testing.T) {
	env := newProfileEnv(t)
	profile := env.createWithCity(t, "Colombo")

	cookie, err := env.svc.CreateProfileCookie(env.ctx, profile.ProfileId)
	require.NoError(t, err)
	assert.NotEmpty(t, cookie.CookieId)
	assert.True(t, cookie.IsActive)

	byProfile, err := env.svc.GetProfileCookieByProfileId(env.ctx, profile.ProfileId)
	require.NoError(t, err)
	assert.Equal(t, cookie.CookieId, byProfile.CookieId)
	byCookie, err := env.svc.GetProfileCookieById(env.ctx, cookie.CookieId, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, profile.ProfileId, byCookie.ProfileId)

	require.NoError(t, env.svc.UpdateCookieStatusByProfileId(env.ctx, profile.ProfileId, false))
	inactive, err := env.svc.GetProfileCookieByProfileId(env.ctx, profile.ProfileId)
	require.NoError(t, err)
	assert.False(t, inactive.IsActive)

	require.NoError(t, env.svc.UpdateCookieStatusByCookieId(env.ctx, cookie.CookieId, true))
	active, err := env.svc.GetProfileCookieById(env.ctx, cookie.CookieId, env.orgID)
	require.NoError(t, err)
	assert.True(t, active.IsActive)

	require.NoError(t, env.svc.DeleteCookieByProfileId(env.ctx, profile.ProfileId))
	_, err = env.svc.GetProfileCookieByProfileId(env.ctx, profile.ProfileId)
	_ = requireStatus(t, err, http.StatusNotFound)
	_, err = env.svc.GetProfileCookieById(env.ctx, cookie.CookieId, env.orgID)
	_ = requireStatus(t, err, http.StatusNotFound)
}

func TestACookieIsOnlyFoundInItsOrganization(t *testing.T) {
	env := newProfileEnv(t)
	profile := env.createWithCity(t, "Colombo")
	cookie, err := env.svc.CreateProfileCookie(env.ctx, profile.ProfileId)
	require.NoError(t, err)

	_, err = env.svc.GetProfileCookieById(env.ctx, cookie.CookieId, "another-org")

	_ = requireStatus(t, err, http.StatusNotFound)
}

func TestInactiveCookiesAreCleanedUpInBatches(t *testing.T) {
	env := newProfileEnv(t)
	var profiles []string
	for i := 0; i < 3; i++ {
		profile := env.createWithCity(t, fmt.Sprintf("city-%d", i))
		_, err := env.svc.CreateProfileCookie(env.ctx, profile.ProfileId)
		require.NoError(t, err)
		profiles = append(profiles, profile.ProfileId)
	}
	for _, id := range profiles[:2] {
		require.NoError(t, env.svc.UpdateCookieStatusByProfileId(env.ctx, id, false))
	}

	remaining := func() int {
		count := 0
		for _, id := range profiles {
			if _, err := env.svc.GetProfileCookieByProfileId(env.ctx, id); err == nil {
				count++
			}
		}
		return count
	}
	require.Equal(t, 3, remaining())

	_, err := profileStore.DeleteInactiveCookieProfiles(env.ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, remaining(), "a batch is as large as asked")
	_, err = profileStore.DeleteInactiveCookieProfiles(env.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, remaining())
	_, err = profileStore.DeleteInactiveCookieProfiles(env.ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, remaining(), "nothing is left to clean up")

	_, err = env.svc.GetProfileCookieByProfileId(env.ctx, profiles[2])
	assert.NoError(t, err, "an active cookie stays")
}

// addCategory stores a consent category and returns its identifier.
func (e *profileEnv) addCategory(t *testing.T, name string) string {
	t.Helper()
	id := uuid.New().String()
	require.NoError(t, consentStore.AddConsentCategory(e.ctx, e.orgID, consentModel.ConsentCategory{
		CategoryName:       name,
		CategoryIdentifier: id,
		OrgId:              e.orgID,
		Purpose:            "profiling",
	}))
	return id
}

func TestProfileConsentsAreStoredAndReadBack(t *testing.T) {
	env := newProfileEnv(t)
	profile := env.createWithCity(t, "Colombo")
	analytics := env.addCategory(t, "Analytics")
	offers := env.addCategory(t, "Offers")
	before := time.Now().UTC().Add(-time.Second)

	require.NoError(t, env.svc.UpdateProfileConsents(env.ctx, profile.ProfileId, env.orgID,
		[]profileModel.ConsentRecord{
			{CategoryIdentifier: analytics, IsConsented: true},
			{CategoryIdentifier: offers, IsConsented: false, ConsentedAt: before},
		}))

	records, err := env.svc.GetProfileConsents(env.ctx, profile.ProfileId, env.orgID)
	require.NoError(t, err)
	byCategory := map[string]profileModel.ConsentRecord{}
	for _, record := range records {
		byCategory[record.CategoryIdentifier] = record
	}
	require.Len(t, byCategory, 2)
	assert.True(t, byCategory[analytics].IsConsented)
	assert.True(t, byCategory[analytics].ConsentedAt.After(before), "the time is set when it was not sent")
	assert.False(t, byCategory[offers].IsConsented)
	assert.WithinDuration(t, before, byCategory[offers].ConsentedAt, time.Second, "a time that was sent is kept")

	// An update replaces the profile's consents.
	require.NoError(t, env.svc.UpdateProfileConsents(env.ctx, profile.ProfileId, env.orgID,
		[]profileModel.ConsentRecord{{CategoryIdentifier: offers, IsConsented: true}}))
	records, err = env.svc.GetProfileConsents(env.ctx, profile.ProfileId, env.orgID)
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, offers, records[0].CategoryIdentifier)
}

func TestProfileConsentRefusals(t *testing.T) {
	env := newProfileEnv(t)
	profile := env.createWithCity(t, "Colombo")
	analytics := env.addCategory(t, "Analytics")
	require.NoError(t, consentStore.SeedDefaultIdentityDataCategory(env.ctx, env.orgID))
	mandatory, err := consentStore.GetMandatoryConsentCategoryIds(env.ctx, env.orgID)
	require.NoError(t, err)
	require.Len(t, mandatory, 1)

	err = env.svc.UpdateProfileConsents(env.ctx, profile.ProfileId, env.orgID,
		[]profileModel.ConsentRecord{{CategoryIdentifier: mandatory[0], IsConsented: false}})
	clientError := requireStatus(t, err, http.StatusForbidden)
	assert.Equal(t, errors2.CONSENT_CAT_MANDATORY.Code, clientError.Code)

	err = env.svc.UpdateProfileConsents(env.ctx, profile.ProfileId, env.orgID,
		[]profileModel.ConsentRecord{{CategoryIdentifier: analytics}, {CategoryIdentifier: "not-a-category"}})
	_ = requireStatus(t, err, http.StatusBadRequest)

	err = env.svc.UpdateProfileConsents(env.ctx, "missing", env.orgID,
		[]profileModel.ConsentRecord{{CategoryIdentifier: analytics}})
	_ = requireStatus(t, err, http.StatusNotFound)
	_, err = env.svc.GetProfileConsents(env.ctx, "missing", env.orgID)
	_ = requireStatus(t, err, http.StatusNotFound)
}

func TestFilteredListByTraitIdentityAndIDs(t *testing.T) {
	env := newProfileEnv(t)
	env.seedSchema(t, constants.IdentityAttributes, "nickname", constants.StringDataType, constants.MutabilityReadWrite)
	colombo, err := env.svc.CreateProfile(env.ctx, profileModel.ProfileRequest{
		UserId:             "u-1",
		Traits:             map[string]interface{}{"city": "Colombo", "tier": "gold"},
		IdentityAttributes: map[string]interface{}{"nickname": "Colly"},
	}, env.orgID)
	require.NoError(t, err)
	kandy, err := env.svc.CreateProfile(env.ctx, profileModel.ProfileRequest{
		UserId: "u-2",
		Traits: map[string]interface{}{"city": "Kandy", "tier": "gold"},
	}, env.orgID)
	require.NoError(t, err)
	galle := env.createWithCity(t, "Galle")

	list := func(filters ...string) []string {
		page, _, err := env.svc.GetAllProfilesWithFilterCursor(env.ctx, env.orgID, filters, 10, nil)
		require.NoError(t, err)
		ids := make([]string, 0, len(page))
		for _, profile := range page {
			ids = append(ids, profile.ProfileId)
		}
		return ids
	}

	assert.ElementsMatch(t, []string{colombo.ProfileId}, list("traits.city eq Colombo"))
	assert.ElementsMatch(t, []string{colombo.ProfileId, kandy.ProfileId}, list("traits.tier eq gold"))
	assert.Empty(t, list("traits.city co an", "traits.city sw Ga"), "every condition has to hold")
	assert.ElementsMatch(t, []string{kandy.ProfileId}, list("traits.city co an", "traits.city sw Ka"))
	assert.ElementsMatch(t, []string{kandy.ProfileId}, list("traits.city co an", "traits.tier eq gold"))
	assert.ElementsMatch(t, []string{galle.ProfileId}, list("traits.city sw Ga"))
	assert.ElementsMatch(t, []string{colombo.ProfileId}, list("identity_attributes.nickname eq Colly"))
	assert.ElementsMatch(t, []string{kandy.ProfileId}, list("user_id eq u-2"))
	assert.ElementsMatch(t, []string{galle.ProfileId}, list("profile_id eq "+galle.ProfileId))
	assert.Empty(t, list("traits.city eq Nowhere"))
}
