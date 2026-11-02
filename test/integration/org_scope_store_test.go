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
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	consentService "github.com/wso2/identity-customer-data-service/internal/consent/service"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	profileSchemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	unificationModel "github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	unificationService "github.com/wso2/identity-customer-data-service/internal/unification_rules/service"
	unificationStore "github.com/wso2/identity-customer-data-service/internal/unification_rules/store"
)

func Test_OrgScope_UnificationRuleStore(t *testing.T) {
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	ownerOrg := fmt.Sprintf("scope-rule-owner-%d", suffix)
	otherOrg := fmt.Sprintf("scope-rule-other-%d", suffix)
	ruleSvc := unificationService.GetUnificationRuleService()
	schemaSvc := schemaService.GetProfileSchemaService()

	_, err := schemaSvc.AddProfileSchemaAttributesForScope(ctx, []profileSchemaModel.ProfileSchemaAttribute{{
		OrgId: ownerOrg, AttributeId: uuid.New().String(), AttributeName: "traits.loyalty_id",
		ValueType: constants.StringDataType, MergeStrategy: "combine", Mutability: constants.MutabilityReadWrite,
	}}, constants.Traits, ownerOrg)
	require.NoError(t, err)

	now := time.Now().UTC()
	rule := unificationModel.UnificationRule{
		RuleId: uuid.New().String(), OrgHandle: ownerOrg, RuleName: "Owner loyalty rule",
		PropertyName: "traits.loyalty_id", Priority: 1, IsActive: true, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, ruleSvc.AddUnificationRule(ctx, rule, ownerOrg))
	changed := rule
	changed.RuleName = "Renamed"
	changed.Priority = 77
	changed.IsActive = false

	t.Run("Service_calls_with_other_org_return_not_found", func(t *testing.T) {
		found, err := ruleSvc.GetUnificationRule(ctx, rule.RuleId, otherOrg)
		assert.Nil(t, found)
		assertClientError(t, err, http.StatusNotFound, errors2.UNIFICATION_RULE_NOT_FOUND.Code)
		assertClientError(t, ruleSvc.PatchUnificationRule(ctx, rule.RuleId, otherOrg, changed),
			http.StatusNotFound, errors2.UNIFICATION_RULE_NOT_FOUND.Code)
	})

	t.Run("Service_delete_with_other_org_deletes_nothing", func(t *testing.T) {
		require.NoError(t, ruleSvc.DeleteUnificationRule(ctx, rule.RuleId, otherOrg))

		stored, err := ruleSvc.GetUnificationRule(ctx, rule.RuleId, ownerOrg)
		require.NoError(t, err)
		require.NotNil(t, stored, "owner rule must survive a delete by another org")
		assert.Equal(t, "Owner loyalty rule", stored.RuleName)
	})

	t.Run("Store_calls_with_other_org_change_nothing", func(t *testing.T) {
		found, err := unificationStore.GetUnificationRule(ctx, rule.RuleId, otherOrg)
		require.NoError(t, err)
		assert.Nil(t, found)
		require.NoError(t, unificationStore.PatchUnificationRule(ctx, rule.RuleId, otherOrg, changed))
		require.NoError(t, unificationStore.DeleteUnificationRule(ctx, rule.RuleId, otherOrg))

		stored, err := unificationStore.GetUnificationRule(ctx, rule.RuleId, ownerOrg)
		require.NoError(t, err)
		require.NotNil(t, stored, "owner rule must survive a delete scoped to another org")
		assert.Equal(t, "Owner loyalty rule", stored.RuleName)
		assert.Equal(t, 1, stored.Priority)
		assert.True(t, stored.IsActive)
	})

	t.Run("Delete_unknown_rule_succeeds", func(t *testing.T) {
		require.NoError(t, ruleSvc.DeleteUnificationRule(ctx, uuid.New().String(), ownerOrg))
	})

	_ = ruleSvc.DeleteUnificationRule(ctx, rule.RuleId, ownerOrg)
	_ = schemaSvc.DeleteProfileSchemaAttributesByScope(ctx, ownerOrg, constants.Traits)
}

func Test_OrgScope_ProfileStore(t *testing.T) {
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	ownerOrg := fmt.Sprintf("scope-profile-owner-%d", suffix)
	otherOrg := fmt.Sprintf("scope-profile-other-%d", suffix)
	ownerUserId := fmt.Sprintf("scope-owner-user-%d", suffix)
	profiles := profileService.GetProfilesService()
	consentSvc := consentService.GetConsentCategoryService()
	schemaSvc := schemaService.GetProfileSchemaService()

	for _, org := range []string{ownerOrg, otherOrg} {
		_, err := schemaSvc.AddProfileSchemaAttributesForScope(ctx, []profileSchemaModel.ProfileSchemaAttribute{{
			OrgId: org, AttributeId: uuid.New().String(), AttributeName: "traits.interests",
			ValueType: constants.StringDataType, MergeStrategy: "combine", Mutability: constants.MutabilityReadWrite,
		}}, constants.Traits, org)
		require.NoError(t, err)
		require.NoError(t, consentSvc.SeedDefaultConsentCategory(ctx, org))
	}
	ownerCategory, err := consentSvc.AddConsentCategory(ctx, consentModel.ConsentCategory{
		CategoryName: "Owner Marketing", OrgHandle: ownerOrg, Purpose: "personalization",
	})
	require.NoError(t, err)

	owner, err := profiles.CreateProfile(ctx, profileModel.ProfileRequest{
		UserId: ownerUserId,
		Traits: map[string]interface{}{"interests": "owner-interest"},
	}, ownerOrg)
	require.NoError(t, err)
	require.NoError(t, profiles.UpdateProfileConsents(ctx, owner.ProfileId, ownerOrg,
		[]profileModel.ConsentRecord{{CategoryIdentifier: ownerCategory.CategoryIdentifier, IsConsented: true}}))
	cookie, err := profiles.CreateProfileCookie(ctx, owner.ProfileId)
	require.NoError(t, err)

	t.Run("Lookups_with_other_org_find_nothing", func(t *testing.T) {
		byId, err := profileStore.GetProfile(ctx, owner.ProfileId, otherOrg)
		require.NoError(t, err)
		assert.Nil(t, byId)

		byUser, err := profileStore.GetProfileWithUserId(ctx, ownerUserId, otherOrg)
		require.NoError(t, err)
		assert.Nil(t, byUser)

		byCookie, err := profileStore.GetProfileCookie(ctx, cookie.CookieId, otherOrg)
		require.NoError(t, err)
		assert.Nil(t, byCookie)
	})

	t.Run("Lookups_with_own_org_find_the_profile", func(t *testing.T) {
		byId, err := profileStore.GetProfile(ctx, owner.ProfileId, ownerOrg)
		require.NoError(t, err)
		require.NotNil(t, byId)

		byUser, err := profileStore.GetProfileWithUserId(ctx, ownerUserId, ownerOrg)
		require.NoError(t, err)
		require.NotNil(t, byUser)
		assert.Equal(t, owner.ProfileId, byUser.ProfileId)

		byCookie, err := profileStore.GetProfileCookie(ctx, cookie.CookieId, ownerOrg)
		require.NoError(t, err)
		require.NotNil(t, byCookie)
		assert.Equal(t, owner.ProfileId, byCookie.ProfileId)
	})

	t.Run("Service_calls_with_other_org_return_not_found", func(t *testing.T) {
		_, err := profiles.GetProfile(ctx, owner.ProfileId, otherOrg)
		assertClientError(t, err, http.StatusNotFound, errors2.PROFILE_NOT_FOUND.Code)
		_, err = profiles.UpdateProfile(ctx, owner.ProfileId, otherOrg, profileModel.ProfileRequest{
			Traits: map[string]interface{}{"interests": "changed"},
		})
		assertClientError(t, err, http.StatusNotFound, errors2.PROFILE_NOT_FOUND.Code)
		_, err = profiles.PatchProfile(ctx, owner.ProfileId, otherOrg,
			map[string]interface{}{"traits": map[string]interface{}{"interests": "changed"}})
		assertClientError(t, err, http.StatusNotFound, errors2.PROFILE_NOT_FOUND.Code)
		_, err = profiles.GetProfileConsents(ctx, owner.ProfileId, otherOrg)
		assertClientError(t, err, http.StatusNotFound, errors2.PROFILE_NOT_FOUND.Code)
		err = profiles.UpdateProfileConsents(ctx, owner.ProfileId, otherOrg, []profileModel.ConsentRecord{
			{CategoryIdentifier: ownerCategory.CategoryIdentifier, IsConsented: false}})
		assertClientError(t, err, http.StatusNotFound, errors2.PROFILE_NOT_FOUND.Code)
		_, err = profiles.FindProfileByUserId(ctx, ownerUserId, otherOrg)
		assertClientError(t, err, http.StatusNotFound, errors2.PROFILE_NOT_FOUND.Code)
		_, err = profiles.GetProfileCookieById(ctx, cookie.CookieId, otherOrg)
		assertClientError(t, err, http.StatusNotFound, errors2.PROFILE_COOKIE_NOT_FOUND.Code)
	})

	t.Run("Service_delete_with_other_org_deletes_nothing", func(t *testing.T) {
		require.NoError(t, profiles.DeleteProfile(ctx, owner.ProfileId, otherOrg))

		stored, err := profileStore.GetProfile(ctx, owner.ProfileId, ownerOrg)
		require.NoError(t, err)
		require.NotNil(t, stored, "owner profile must survive a delete by another org")
		assert.Equal(t, "owner-interest", stored.Traits["interests"])
	})

	t.Run("Consent_update_rejects_categories_of_other_orgs", func(t *testing.T) {
		other, err := profiles.CreateProfile(ctx, profileModel.ProfileRequest{
			Traits: map[string]interface{}{"interests": "other-interest"},
		}, otherOrg)
		require.NoError(t, err)

		for _, categoryId := range []string{ownerCategory.CategoryIdentifier, uuid.New().String(), ""} {
			err = profiles.UpdateProfileConsents(ctx, other.ProfileId, otherOrg, []profileModel.ConsentRecord{
				{CategoryIdentifier: categoryId, IsConsented: true}})
			assertClientError(t, err, http.StatusBadRequest, errors2.CONSENT_CAT_VALIDATION.Code)
		}
		consents, err := profiles.GetProfileConsents(ctx, other.ProfileId, otherOrg)
		require.NoError(t, err)
		assert.Empty(t, consents)

		_ = profiles.DeleteProfile(ctx, other.ProfileId, otherOrg)
	})

	t.Run("Owner_profile_is_unchanged", func(t *testing.T) {
		got, err := profiles.GetProfile(ctx, owner.ProfileId, ownerOrg)
		require.NoError(t, err)
		assert.Equal(t, ownerUserId, got.UserId)
		assert.Equal(t, "owner-interest", got.Traits["interests"])

		consents, err := profiles.GetProfileConsents(ctx, owner.ProfileId, ownerOrg)
		require.NoError(t, err)
		require.Len(t, consents, 1)
		assert.True(t, consents[0].IsConsented)
	})

	_ = profiles.DeleteProfile(ctx, owner.ProfileId, ownerOrg)
	_ = consentSvc.DeleteConsentCategory(ctx, ownerCategory.CategoryIdentifier, ownerOrg)
}

func assertClientError(t *testing.T, err error, status int, code string) {

	t.Helper()

	require.Error(t, err)
	var clientErr *errors2.ClientError
	require.ErrorAs(t, err, &clientErr)
	assert.Equal(t, status, clientErr.StatusCode)
	assert.Equal(t, code, clientErr.ErrorMessage.Code)
}
