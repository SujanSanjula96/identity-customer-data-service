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

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	consentService "github.com/wso2/identity-customer-data-service/internal/consent/service"
	consentStore "github.com/wso2/identity-customer-data-service/internal/consent/store"
	profileSchemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

func Test_Consent(t *testing.T) {
	org := fmt.Sprintf("consent-org-%d", time.Now().UnixNano())
	svc := consentService.GetConsentCategoryService()
	schemaSvc := schemaService.GetProfileSchemaService()

	restore := schemaService.OverrideValidateApplicationIdentifierForTest(
		func(context.Context, string, string) (error, bool) { return nil, true })
	defer restore()

	const testAppId = "app-client-001"

	// Seed schema attributes so resolveAttributeScopes can look them up.
	t.Run("PreRequisite_SeedSchema", func(t *testing.T) {
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
		}
		_, err := schemaSvc.AddProfileSchemaAttributesForScope(context.Background(), identityAttrs, constants.IdentityAttributes, org)
		require.NoError(t, err)

		traitAttrs := []profileSchemaModel.ProfileSchemaAttribute{
			{
				OrgId:         org,
				AttributeId:   uuid.New().String(),
				AttributeName: "traits.age",
				DisplayName:   "Age",
				ValueType:     constants.StringDataType,
				MergeStrategy: "combine",
				Mutability:    constants.MutabilityReadWrite,
			},
		}
		_, err = schemaSvc.AddProfileSchemaAttributesForScope(context.Background(), traitAttrs, constants.Traits, org)
		require.NoError(t, err)

		appAttrs := []profileSchemaModel.ProfileSchemaAttribute{
			{
				OrgId:                 org,
				AttributeId:           uuid.New().String(),
				AttributeName:         "application_data.events.event_name",
				DisplayName:           "Event Name",
				ValueType:             constants.StringDataType,
				MergeStrategy:         "combine",
				Mutability:            constants.MutabilityReadWrite,
				ApplicationIdentifier: testAppId,
			},
		}
		_, err = schemaSvc.AddProfileSchemaAttributesForScope(context.Background(), appAttrs, constants.ApplicationData, org)
		require.NoError(t, err)
	})

	// Seed mandatory "Identity Data" category for the org.
	t.Run("PreRequisite_SeedMandatoryCategory", func(t *testing.T) {
		err := svc.SeedDefaultConsentCategory(context.Background(), org)
		require.NoError(t, err)

		mandatoryIds, err := consentStore.GetMandatoryConsentCategoryIds(context.Background(), org)
		require.NoError(t, err)
		assert.NotEmpty(t, mandatoryIds, "mandatory Identity Data category should be seeded")
	})

	var categoryId string

	t.Run("Add_consent_category", func(t *testing.T) {
		category := consentModel.ConsentCategory{
			CategoryName: "Marketing",
			OrgHandle:    org,
			Purpose:      "personalization",
			Destinations: []string{"crm"},
			Attributes: []consentModel.ConsentAttribute{
				{AttributeName: "identity_attributes.email"},
				{AttributeName: "traits.age"},
			},
		}
		created, err := svc.AddConsentCategory(context.Background(), category)
		require.NoError(t, err)
		assert.Equal(t, "Marketing", created.CategoryName)
		assert.NotEmpty(t, created.CategoryIdentifier)
		assert.Len(t, created.Attributes, 2)
		// Scope must be derived automatically — never blank.
		for _, attr := range created.Attributes {
			assert.NotEmpty(t, attr.Scope, "scope should be derived for %s", attr.AttributeName)
		}
		categoryId = created.CategoryIdentifier
	})

	t.Run("CategoryIdentifier_is_UUID", func(t *testing.T) {
		_, err := uuid.Parse(categoryId)
		assert.NoError(t, err, "category_identifier should be a valid UUID")
	})

	t.Run("Get_all_consent_categories", func(t *testing.T) {
		cats, err := svc.GetAllConsentCategories(context.Background())
		require.NoError(t, err)
		assert.NotEmpty(t, cats)
	})

	t.Run("Get_single_category", func(t *testing.T) {
		fetched, err := svc.GetConsentCategory(context.Background(), categoryId)
		require.NoError(t, err)
		require.NotNil(t, fetched)
		assert.Equal(t, "Marketing", fetched.CategoryName)
		assert.Len(t, fetched.Attributes, 2)
	})

	t.Run("Reject_duplicate_category_name", func(t *testing.T) {
		dup := consentModel.ConsentCategory{
			CategoryName: "Marketing",
			OrgHandle:    org,
			Purpose:      "profiling",
		}
		_, err := svc.AddConsentCategory(context.Background(), dup)
		assert.Error(t, err, "should reject a category with an already-used name")
	})

	t.Run("Reject_invalid_purpose", func(t *testing.T) {
		bad := consentModel.ConsentCategory{
			CategoryName: "Bad Purpose",
			OrgHandle:    org,
			Purpose:      "unknown",
		}
		_, err := svc.AddConsentCategory(context.Background(), bad)
		assert.Error(t, err)
	})

	t.Run("Reject_unknown_attribute_name", func(t *testing.T) {
		bad := consentModel.ConsentCategory{
			CategoryName: "Bad Attr",
			OrgHandle:    org,
			Purpose:      "profiling",
			Attributes:   []consentModel.ConsentAttribute{{AttributeName: "identity_attributes.nonexistent"}},
		}
		_, err := svc.AddConsentCategory(context.Background(), bad)
		assert.Error(t, err)
	})

	t.Run("Update_consent_category", func(t *testing.T) {
		updated := consentModel.ConsentCategory{
			CategoryIdentifier: categoryId,
			CategoryName:       "Marketing Updated",
			OrgHandle:          org,
			Purpose:            "personalization",
			Attributes:         []consentModel.ConsentAttribute{{AttributeName: "traits.age"}},
		}
		err := svc.UpdateConsentCategory(context.Background(), updated)
		require.NoError(t, err)

		fetched, err := svc.GetConsentCategory(context.Background(), categoryId)
		require.NoError(t, err)
		assert.Equal(t, "Marketing Updated", fetched.CategoryName)
		assert.Len(t, fetched.Attributes, 1)
	})

	t.Run("Reject_update_mandatory_category", func(t *testing.T) {
		mandatoryIds, err := consentStore.GetMandatoryConsentCategoryIds(context.Background(), org)
		require.NoError(t, err)
		require.NotEmpty(t, mandatoryIds)

		err = svc.UpdateConsentCategory(context.Background(), consentModel.ConsentCategory{
			CategoryIdentifier: mandatoryIds[0],
			CategoryName:       "Tampered",
			OrgHandle:          org,
			Purpose:            "profiling",
		})
		assert.Error(t, err, "should reject update on mandatory category")
	})

	t.Run("Reject_delete_mandatory_category", func(t *testing.T) {
		mandatoryIds, err := consentStore.GetMandatoryConsentCategoryIds(context.Background(), org)
		require.NoError(t, err)
		require.NotEmpty(t, mandatoryIds)

		err = svc.DeleteConsentCategory(context.Background(), mandatoryIds[0])
		assert.Error(t, err, "should reject deletion of mandatory category")
	})

	t.Run("Add_applicationData_attribute_with_valid_application_identifier", func(t *testing.T) {
		cat := consentModel.ConsentCategory{
			CategoryName: "App Events",
			OrgHandle:    org,
			Purpose:      "profiling",
			Attributes: []consentModel.ConsentAttribute{
				{AttributeName: "application_data.events.event_name", ApplicationIdentifier: testAppId},
			},
		}
		created, err := svc.AddConsentCategory(context.Background(), cat)
		require.NoError(t, err)
		require.NotNil(t, created)
		assert.Len(t, created.Attributes, 1)
		assert.Equal(t, testAppId, created.Attributes[0].ApplicationIdentifier)
		// Cleanup
		_ = svc.DeleteConsentCategory(context.Background(), created.CategoryIdentifier)
	})

	t.Run("Reject_applicationData_attribute_with_missing_application_identifier", func(t *testing.T) {
		cat := consentModel.ConsentCategory{
			CategoryName: "Missing ApplicationIdentifier",
			OrgHandle:    org,
			Purpose:      "profiling",
			Attributes: []consentModel.ConsentAttribute{
				{AttributeName: "application_data.events.event_name"}, // ApplicationIdentifier deliberately empty
			},
		}
		_, err := svc.AddConsentCategory(context.Background(), cat)
		assert.Error(t, err, "should reject applicationData attribute with no application_identifier")
	})

	t.Run("Reject_applicationData_attribute_with_mismatched_application_identifier", func(t *testing.T) {
		cat := consentModel.ConsentCategory{
			CategoryName: "Wrong ApplicationIdentifier",
			OrgHandle:    org,
			Purpose:      "profiling",
			Attributes: []consentModel.ConsentAttribute{
				{AttributeName: "application_data.events.event_name", ApplicationIdentifier: "wrong-app-id"},
			},
		}
		_, err := svc.AddConsentCategory(context.Background(), cat)
		assert.Error(t, err, "should reject application_identifier that does not match schema application_identifier")
	})

	t.Run("Delete_consent_category", func(t *testing.T) {
		err := svc.DeleteConsentCategory(context.Background(), categoryId)
		require.NoError(t, err)

		deleted, _ := svc.GetConsentCategory(context.Background(), categoryId)
		assert.Nil(t, deleted, "category should be gone after deletion")
	})
}
