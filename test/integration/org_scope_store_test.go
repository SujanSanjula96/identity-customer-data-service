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

	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
	"github.com/wso2/identity-customer-data-service/test/setup"

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
	ownerOrg := setup.TestOrg(t, fmt.Sprintf("scope-rule-owner-%d", suffix))
	otherOrg := setup.TestOrg(t, fmt.Sprintf("scope-rule-other-%d", suffix))
	ruleSvc := unificationService.GetUnificationRuleService()
	schemaSvc := schemaService.GetProfileSchemaService()

	_, err := schemaSvc.AddProfileSchemaAttributesForScope(ctx, []profileSchemaModel.ProfileSchemaAttribute{{
		OrgID: ownerOrg.ID, AttributeID: uuid.New().String(), AttributeName: "traits.loyalty_id",
		ValueType: constants.StringDataType, MergeStrategy: "combine", Mutability: constants.MutabilityReadWrite,
	}}, constants.Traits, ownerOrg.ID)
	require.NoError(t, err)

	now := time.Now().UTC()
	rule := unificationModel.UnificationRule{
		RuleID: uuid.New().String(), OrgID: ownerOrg.ID, RuleName: "Owner loyalty rule",
		PropertyName: "traits.loyalty_id", Priority: 1, IsActive: true, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, ruleSvc.AddUnificationRule(ctx, rule, ownerOrg.ID))
	changed := rule
	changed.RuleName = "Renamed"
	changed.Priority = 77
	changed.IsActive = false

	t.Run("Service_calls_with_other_org_return_not_found", func(t *testing.T) {
		found, err := ruleSvc.GetUnificationRule(ctx, rule.RuleID, otherOrg.ID)
		assert.Nil(t, found)
		assertClientError(t, err, http.StatusNotFound, errors2.UnificationRuleNotFound.Code)
		assertClientError(t, ruleSvc.PatchUnificationRule(ctx, rule.RuleID, otherOrg.ID, changed),
			http.StatusNotFound, errors2.UnificationRuleNotFound.Code)
	})

	t.Run("Service_delete_with_other_org_deletes_nothing", func(t *testing.T) {
		require.NoError(t, ruleSvc.DeleteUnificationRule(ctx, rule.RuleID, otherOrg.ID))

		stored, err := ruleSvc.GetUnificationRule(ctx, rule.RuleID, ownerOrg.ID)
		require.NoError(t, err)
		require.NotNil(t, stored, "owner rule must survive a delete by another org")
		assert.Equal(t, "Owner loyalty rule", stored.RuleName)
	})

	t.Run("Store_calls_with_other_org_change_nothing", func(t *testing.T) {
		found, err := unificationStore.GetUnificationRule(ctx, rule.RuleID, otherOrg.ID)
		require.NoError(t, err)
		assert.Nil(t, found)
		require.NoError(t, unificationStore.PatchUnificationRule(ctx, rule.RuleID, otherOrg.ID, changed))
		require.NoError(t, unificationStore.DeleteUnificationRule(ctx, rule.RuleID, otherOrg.ID))

		stored, err := unificationStore.GetUnificationRule(ctx, rule.RuleID, ownerOrg.ID)
		require.NoError(t, err)
		require.NotNil(t, stored, "owner rule must survive a delete scoped to another org")
		assert.Equal(t, "Owner loyalty rule", stored.RuleName)
		assert.Equal(t, 1, stored.Priority)
		assert.True(t, stored.IsActive)
	})

	t.Run("Delete_unknown_rule_succeeds", func(t *testing.T) {
		require.NoError(t, ruleSvc.DeleteUnificationRule(ctx, uuid.New().String(), ownerOrg.ID))
	})

	_ = ruleSvc.DeleteUnificationRule(ctx, rule.RuleID, ownerOrg.ID)
	_ = schemaSvc.DeleteProfileSchemaAttributesByScope(ctx, ownerOrg.ID, constants.Traits)
}

func Test_OrgScope_ProfileStore(t *testing.T) {
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	ownerOrg := setup.TestOrg(t, fmt.Sprintf("scope-profile-owner-%d", suffix))
	otherOrg := setup.TestOrg(t, fmt.Sprintf("scope-profile-other-%d", suffix))
	ownerUserID := fmt.Sprintf("scope-owner-user-%d", suffix)
	profiles := profileService.GetProfilesService()
	consentSvc := consentService.GetConsentCategoryService()
	schemaSvc := schemaService.GetProfileSchemaService()

	for _, org := range []orgModel.Ref{ownerOrg, otherOrg} {
		_, err := schemaSvc.AddProfileSchemaAttributesForScope(ctx, []profileSchemaModel.ProfileSchemaAttribute{{
			OrgID: org.ID, AttributeID: uuid.New().String(), AttributeName: "traits.interests",
			ValueType: constants.StringDataType, MergeStrategy: "combine", Mutability: constants.MutabilityReadWrite,
		}}, constants.Traits, org.ID)
		require.NoError(t, err)
		require.NoError(t, consentSvc.SeedDefaultConsentCategory(ctx, org.ID))
	}
	ownerCategory, err := consentSvc.AddConsentCategory(ctx, ownerOrg.ID, consentModel.ConsentCategory{
		CategoryName: "Owner Marketing", OrgID: ownerOrg.ID, Purpose: "personalization",
	})
	require.NoError(t, err)

	owner, err := profiles.CreateProfile(ctx, profileModel.ProfileRequest{
		UserID: ownerUserID,
		Traits: map[string]interface{}{"interests": "owner-interest"},
	}, ownerOrg.ID)
	require.NoError(t, err)
	require.NoError(t, profiles.UpdateProfileConsents(ctx, owner.ProfileID, ownerOrg.ID,
		[]profileModel.ConsentRecord{{CategoryIdentifier: ownerCategory.CategoryIdentifier, IsConsented: true}}))
	cookie, err := profiles.CreateProfileCookie(ctx, owner.ProfileID)
	require.NoError(t, err)

	t.Run("Lookups_with_other_org_find_nothing", func(t *testing.T) {
		byID, err := profileStore.GetProfile(ctx, owner.ProfileID, otherOrg.ID)
		require.NoError(t, err)
		assert.Nil(t, byID)

		byUser, err := profileStore.GetProfileWithUserID(ctx, ownerUserID, otherOrg.ID)
		require.NoError(t, err)
		assert.Nil(t, byUser)

		byCookie, err := profileStore.GetProfileCookie(ctx, cookie.CookieID, otherOrg.ID)
		require.NoError(t, err)
		assert.Nil(t, byCookie)
	})

	t.Run("Lookups_with_own_org_find_the_profile", func(t *testing.T) {
		byID, err := profileStore.GetProfile(ctx, owner.ProfileID, ownerOrg.ID)
		require.NoError(t, err)
		require.NotNil(t, byID)

		byUser, err := profileStore.GetProfileWithUserID(ctx, ownerUserID, ownerOrg.ID)
		require.NoError(t, err)
		require.NotNil(t, byUser)
		assert.Equal(t, owner.ProfileID, byUser.ProfileID)

		byCookie, err := profileStore.GetProfileCookie(ctx, cookie.CookieID, ownerOrg.ID)
		require.NoError(t, err)
		require.NotNil(t, byCookie)
		assert.Equal(t, owner.ProfileID, byCookie.ProfileID)
	})

	t.Run("Service_calls_with_other_org_return_not_found", func(t *testing.T) {
		_, err := profiles.GetProfile(ctx, owner.ProfileID, otherOrg.ID)
		assertClientError(t, err, http.StatusNotFound, errors2.ProfileNotFound.Code)
		_, err = profiles.UpdateProfile(ctx, owner.ProfileID, otherOrg.ID, profileModel.ProfileRequest{
			Traits: map[string]interface{}{"interests": "changed"},
		})
		assertClientError(t, err, http.StatusNotFound, errors2.ProfileNotFound.Code)
		_, err = profiles.PatchProfile(ctx, owner.ProfileID, otherOrg.ID,
			map[string]interface{}{"traits": map[string]interface{}{"interests": "changed"}})
		assertClientError(t, err, http.StatusNotFound, errors2.ProfileNotFound.Code)
		_, err = profiles.GetProfileConsents(ctx, owner.ProfileID, otherOrg.ID)
		assertClientError(t, err, http.StatusNotFound, errors2.ProfileNotFound.Code)
		err = profiles.UpdateProfileConsents(ctx, owner.ProfileID, otherOrg.ID, []profileModel.ConsentRecord{
			{CategoryIdentifier: ownerCategory.CategoryIdentifier, IsConsented: false}})
		assertClientError(t, err, http.StatusNotFound, errors2.ProfileNotFound.Code)
		_, err = profiles.FindProfileByUserID(ctx, ownerUserID, otherOrg.ID)
		assertClientError(t, err, http.StatusNotFound, errors2.ProfileNotFound.Code)
		_, err = profiles.GetProfileCookieByID(ctx, cookie.CookieID, otherOrg.ID)
		assertClientError(t, err, http.StatusNotFound, errors2.ProfileCookieNotFound.Code)
	})

	t.Run("Service_delete_with_other_org_deletes_nothing", func(t *testing.T) {
		require.NoError(t, profiles.DeleteProfile(ctx, owner.ProfileID, otherOrg.ID))

		stored, err := profileStore.GetProfile(ctx, owner.ProfileID, ownerOrg.ID)
		require.NoError(t, err)
		require.NotNil(t, stored, "owner profile must survive a delete by another org")
		assert.Equal(t, "owner-interest", stored.Traits["interests"])
	})

	t.Run("Consent_update_rejects_categories_of_other_orgs", func(t *testing.T) {
		other, err := profiles.CreateProfile(ctx, profileModel.ProfileRequest{
			Traits: map[string]interface{}{"interests": "other-interest"},
		}, otherOrg.ID)
		require.NoError(t, err)

		for _, categoryID := range []string{ownerCategory.CategoryIdentifier, uuid.New().String(), ""} {
			err = profiles.UpdateProfileConsents(ctx, other.ProfileID, otherOrg.ID, []profileModel.ConsentRecord{
				{CategoryIdentifier: categoryID, IsConsented: true}})
			assertClientError(t, err, http.StatusBadRequest, errors2.ConsentCatValidation.Code)
		}
		consents, err := profiles.GetProfileConsents(ctx, other.ProfileID, otherOrg.ID)
		require.NoError(t, err)
		assert.Empty(t, consents)

		_ = profiles.DeleteProfile(ctx, other.ProfileID, otherOrg.ID)
	})

	t.Run("Owner_profile_is_unchanged", func(t *testing.T) {
		got, err := profiles.GetProfile(ctx, owner.ProfileID, ownerOrg.ID)
		require.NoError(t, err)
		assert.Equal(t, ownerUserID, got.UserID)
		assert.Equal(t, "owner-interest", got.Traits["interests"])

		consents, err := profiles.GetProfileConsents(ctx, owner.ProfileID, ownerOrg.ID)
		require.NoError(t, err)
		require.Len(t, consents, 1)
		assert.True(t, consents[0].IsConsented)
	})

	_ = profiles.DeleteProfile(ctx, owner.ProfileID, ownerOrg.ID)
	_ = consentSvc.DeleteConsentCategory(ctx, ownerCategory.CategoryIdentifier, ownerOrg.ID)
}

func assertClientError(t *testing.T, err error, status int, code string) {
	t.Helper()

	require.Error(t, err)
	var clientErr *errors2.ClientError
	require.ErrorAs(t, err, &clientErr)
	assert.Equal(t, status, clientErr.StatusCode)
	assert.Equal(t, code, clientErr.ErrorMessage.Code)
}
