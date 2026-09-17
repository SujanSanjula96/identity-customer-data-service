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
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	consentService "github.com/wso2/identity-customer-data-service/internal/consent/service"
	consentStore "github.com/wso2/identity-customer-data-service/internal/consent/store"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	profileSchemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

func Test_ConsentFilter(t *testing.T) {
	org := fmt.Sprintf("filter-org-%d", time.Now().UnixNano())
	profileSvc := profileService.GetProfilesService()
	consentSvc := consentService.GetConsentCategoryService()
	schemaSvc := schemaService.GetProfileSchemaService()

	restore := schemaService.OverrideValidateApplicationIdentifierForTest(
		func(context.Context, string, string) (error, bool) { return nil, true })
	defer restore()

	var profileId string
	var marketingCategoryId string

	// ── Setup ─────────────────────────────────────────────────────────────────

	t.Run("Setup_schema_attributes", func(t *testing.T) {
		identityAttrs := []profileSchemaModel.ProfileSchemaAttribute{
			{
				OrgId:         org,
				AttributeId:   uuid.New().String(),
				AttributeName: "identity_attributes.email",
				DisplayName:   "Email",
				ValueType:     constants.StringDataType,
				MergeStrategy: "combine",
				Mutability:    constants.MutabilityReadWrite,
			},
			{
				OrgId:         org,
				AttributeId:   uuid.New().String(),
				AttributeName: "identity_attributes.phone",
				DisplayName:   "Phone",
				ValueType:     constants.StringDataType,
				MergeStrategy: "combine",
				Mutability:    constants.MutabilityReadWrite,
			},
		}
		_, err := schemaSvc.AddProfileSchemaAttributesForScope(context.Background(), identityAttrs, constants.IdentityAttributes, org)
		require.NoError(t, err)

		traitAttrs := []profileSchemaModel.ProfileSchemaAttribute{
			{
				OrgId:         org,
				AttributeId:   uuid.New().String(),
				AttributeName: "traits.interests",
				DisplayName:   "Interests",
				ValueType:     constants.StringDataType,
				MergeStrategy: "combine",
				Mutability:    constants.MutabilityReadWrite,
			},
		}
		_, err = schemaSvc.AddProfileSchemaAttributesForScope(context.Background(), traitAttrs, constants.Traits, org)
		require.NoError(t, err)
	})

	t.Run("Setup_mandatory_category", func(t *testing.T) {
		err := consentSvc.SeedDefaultConsentCategory(context.Background(), org)
		require.NoError(t, err)
	})

	t.Run("Setup_marketing_category", func(t *testing.T) {
		cat := consentModel.ConsentCategory{
			CategoryName: "Marketing",
			OrgHandle:    org,
			Purpose:      "personalization",
			Attributes:   []consentModel.ConsentAttribute{{AttributeName: "traits.interests"}},
		}
		created, err := consentSvc.AddConsentCategory(context.Background(), cat)
		require.NoError(t, err)
		marketingCategoryId = created.CategoryIdentifier
	})

	t.Run("Setup_profile", func(t *testing.T) {
		var req profileModel.ProfileRequest
		err := json.Unmarshal([]byte(`{
			"user_id": "filter-user-001",
			"identity_attributes": {"email": "jane@example.com", "phone": "+1-555-0100"},
			"traits": {"interests": "reading"}
		}`), &req)
		require.NoError(t, err)

		profile, err := profileSvc.CreateProfile(context.Background(), req, org)
		require.NoError(t, err)
		profileId = profile.ProfileId
	})

	// ── Filter cases ──────────────────────────────────────────────────────────

	t.Run("No_consentCategoryId_returns_mandatory_identity_fields_only", func(t *testing.T) {
		profile, err := profileSvc.GetProfile(context.Background(), profileId)
		require.NoError(t, err)

		filtered, err := profileService.FilterProfileByConsent(context.Background(), *profile, profileId, org, nil)
		require.NoError(t, err)

		assert.Contains(t, filtered.IdentityAttributes, "email", "mandatory email should be present")
		assert.Contains(t, filtered.IdentityAttributes, "phone", "mandatory phone should be present")
		assert.Empty(t, filtered.Traits, "traits are not in the mandatory category")
	})

	t.Run("Revoked_consent_returns_mandatory_fields_only", func(t *testing.T) {
		err := profileSvc.UpdateProfileConsents(context.Background(), profileId, org, []profileModel.ConsentRecord{
			{CategoryIdentifier: marketingCategoryId, IsConsented: false},
		})
		require.NoError(t, err)

		profile, err := profileSvc.GetProfile(context.Background(), profileId)
		require.NoError(t, err)

		filtered, err := profileService.FilterProfileByConsent(context.Background(), *profile, profileId, org, []string{marketingCategoryId})
		require.NoError(t, err)

		assert.NotEmpty(t, filtered.IdentityAttributes)
		assert.Empty(t, filtered.Traits, "traits should not be present when consent is revoked")
	})

	t.Run("Consented_returns_union_of_mandatory_and_category_fields", func(t *testing.T) {
		err := profileSvc.UpdateProfileConsents(context.Background(), profileId, org, []profileModel.ConsentRecord{
			{CategoryIdentifier: marketingCategoryId, IsConsented: true},
		})
		require.NoError(t, err)

		profile, err := profileSvc.GetProfile(context.Background(), profileId)
		require.NoError(t, err)

		filtered, err := profileService.FilterProfileByConsent(context.Background(), *profile, profileId, org, []string{marketingCategoryId})
		require.NoError(t, err)

		assert.Contains(t, filtered.IdentityAttributes, "email")
		assert.Contains(t, filtered.IdentityAttributes, "phone")
		assert.Contains(t, filtered.Traits, "interests", "consented trait should appear in response")
	})

	t.Run("Multiple_consentCategoryIds_union_of_consented_only", func(t *testing.T) {
		// Add a second optional category the profile has NOT consented to.
		analyticsCat := consentModel.ConsentCategory{
			CategoryName: "Analytics",
			OrgHandle:    org,
			Purpose:      "profiling",
			Attributes:   []consentModel.ConsentAttribute{{AttributeName: "traits.interests"}},
		}
		createdAnalytics, err := consentSvc.AddConsentCategory(context.Background(), analyticsCat)
		require.NoError(t, err)
		analyticsCategoryId := createdAnalytics.CategoryIdentifier

		// marketing = consented, analytics = no consent record → revoked
		profile, err := profileSvc.GetProfile(context.Background(), profileId)
		require.NoError(t, err)

		filtered, err := profileService.FilterProfileByConsent(context.Background(),
			*profile, profileId, org,
			[]string{marketingCategoryId, analyticsCategoryId},
		)
		require.NoError(t, err)

		// Mandatory identity fields always present.
		assert.Contains(t, filtered.IdentityAttributes, "email")
		// Traits appear because marketing (which covers interests) is consented.
		assert.Contains(t, filtered.Traits, "interests")
	})

	t.Run("Mandatory_category_cannot_be_consented_to_per_profile", func(t *testing.T) {
		mandatoryIds, err := consentStore.GetMandatoryConsentCategoryIds(context.Background(), org)
		require.NoError(t, err)
		require.NotEmpty(t, mandatoryIds)

		err = profileSvc.UpdateProfileConsents(context.Background(), profileId, org, []profileModel.ConsentRecord{
			{CategoryIdentifier: mandatoryIds[0], IsConsented: true},
		})
		assert.Error(t, err, "should not be able to modify consent for mandatory category")
	})

	t.Cleanup(func() {
		_ = profileSvc.DeleteProfile(context.Background(), profileId)
	})
}
