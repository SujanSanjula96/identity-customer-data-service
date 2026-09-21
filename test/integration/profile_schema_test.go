package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/test/integration/utils"
)

// createAttr is a helper to quickly generate a schema attribute.
func createAttr(org, name, vType, merge, mut string) model.ProfileSchemaAttribute {
	return model.ProfileSchemaAttribute{
		OrgId:                 org,
		AttributeId:           uuid.New().String(),
		AttributeName:         name,
		ValueType:             vType,
		MergeStrategy:         merge,
		Mutability:            mut,
		ApplicationIdentifier: "",
	}
}

func Test_ProfileSchemaService(t *testing.T) {

	SuperTenantOrg := fmt.Sprintf("carbon.super-%d", time.Now().UnixNano())
	svc := schemaService.GetProfileSchemaService()

	restore := schemaService.OverrideValidateApplicationIdentifierForTest(
		// bypass app verification with IDP
		func(context.Context, string, string) (error, bool) { return nil, true })
	defer restore()

	t.Run("Add Operations", func(t *testing.T) {

		t.Run("Add_ValidAttributes_ShouldSucceed", func(t *testing.T) {
			// 1. Identity attributes
			identityAttrs := []model.ProfileSchemaAttribute{
				createAttr(SuperTenantOrg, "identity_attributes.email", constants.StringDataType, "combine", constants.MutabilityReadWrite),
				createAttr(SuperTenantOrg, "identity_attributes.phone", constants.StringDataType, "combine", constants.MutabilityReadWrite),
			}
			_, err := svc.AddProfileSchemaAttributesForScope(context.Background(), identityAttrs, constants.IdentityAttributes, SuperTenantOrg)
			require.NoError(t, err, "Failed to add identity attributes")

			// 2. Traits
			traits := []model.ProfileSchemaAttribute{
				{
					OrgId:         SuperTenantOrg,
					AttributeId:   uuid.New().String(),
					AttributeName: "traits.interests",
					ValueType:     constants.StringDataType,
					MergeStrategy: "combine",
					Mutability:    constants.MutabilityReadWrite,
					MultiValued:   true,
				},
			}
			_, err = svc.AddProfileSchemaAttributesForScope(context.Background(), traits, constants.Traits, SuperTenantOrg)
			require.NoError(t, err, "Failed to add traits attributes")

			// 3. Application Data
			appData := []model.ProfileSchemaAttribute{
				{
					OrgId:                 SuperTenantOrg,
					AttributeId:           uuid.New().String(),
					AttributeName:         "application_data.device_id",
					ValueType:             constants.StringDataType,
					MergeStrategy:         "combine",
					Mutability:            constants.MutabilityReadWrite,
					MultiValued:           true,
					ApplicationIdentifier: "app_1",
				},
			}
			_, err = svc.AddProfileSchemaAttributesForScope(context.Background(), appData, constants.ApplicationData, SuperTenantOrg)
			require.NoError(t, err, "Failed to add application_data attributes")

			_ = svc.DeleteProfileSchema(context.Background(), SuperTenantOrg)
			_ = svc.DeleteProfileSchemaAttributesByScope(context.Background(), SuperTenantOrg, constants.IdentityAttributes)
		})

		t.Run("Add_InvalidScope_ShouldFail", func(t *testing.T) {
			attr := createAttr(SuperTenantOrg, "invalidscope.email", constants.StringDataType, "combine", constants.MutabilityReadWrite)
			_, err := svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{attr}, "invalidscope", SuperTenantOrg)
			errDesc := utils.ExtractErrorDescription(err)
			require.Contains(t, errDesc, "Invalid scope", "Expected validation failure for invalid scope")
		})

		t.Run("Add_ConflictingScope_ShouldFail", func(t *testing.T) {
			attr := createAttr(SuperTenantOrg, "identity_attributes.email", constants.StringDataType, "combine", constants.MutabilityReadWrite)
			_, err := svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{attr}, "invalidscope", SuperTenantOrg)
			errDesc := utils.ExtractErrorDescription(err)
			require.Contains(t, errDesc, "does not match the scope", "Expected validation failure for invalid scope")
		})

		t.Run("Add_MissingAppIdentifier_ShouldFail", func(t *testing.T) {
			attr := createAttr(SuperTenantOrg, "application_data.email", constants.StringDataType, "combine", constants.MutabilityReadWrite)
			_, err := svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{attr}, constants.ApplicationData, SuperTenantOrg)
			errDesc := utils.ExtractErrorDescription(err)
			require.Contains(t, errDesc, "Application identifier is required", "Expected validation failure for missing application identifier")
		})

		t.Run("Add_SameAttributeNameDifferentApps_ShouldSucceed", func(t *testing.T) {
			// Test that the same attribute name can be used for different applications
			app1Attr := model.ProfileSchemaAttribute{
				OrgId:                 SuperTenantOrg,
				AttributeId:           uuid.New().String(),
				AttributeName:         "application_data.theme",
				ValueType:             constants.StringDataType,
				MergeStrategy:         "overwrite",
				Mutability:            constants.MutabilityReadWrite,
				ApplicationIdentifier: "mobile-app",
			}
			_, err := svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{app1Attr}, constants.ApplicationData, SuperTenantOrg)
			require.NoError(t, err, "Failed to add theme attribute for mobile-app")

			// Add the SAME attribute name for a DIFFERENT application - should succeed
			app2Attr := model.ProfileSchemaAttribute{
				OrgId:                 SuperTenantOrg,
				AttributeId:           uuid.New().String(),
				AttributeName:         "application_data.theme",
				ValueType:             constants.StringDataType,
				MergeStrategy:         "overwrite",
				Mutability:            constants.MutabilityReadWrite,
				ApplicationIdentifier: "web-app",
			}
			_, err = svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{app2Attr}, constants.ApplicationData, SuperTenantOrg)
			require.NoError(t, err, "Should allow same attribute name for different applications")

			// Verify both attributes exist
			schema, err := svc.GetProfileSchema(context.Background(), SuperTenantOrg)
			require.NoError(t, err)
			appDataSchemaMap := schema[constants.ApplicationData].(map[string][]model.ProfileSchemaAttribute)

			mobileThemeFound := false
			webThemeFound := false
			for _, attrs := range appDataSchemaMap {
				for _, attr := range attrs {
					if attr.AttributeName == "application_data.theme" {
						if attr.ApplicationIdentifier == "mobile-app" {
							mobileThemeFound = true
						}
						if attr.ApplicationIdentifier == "web-app" {
							webThemeFound = true
						}
					}
				}
			}
			require.True(t, mobileThemeFound, "mobile-app theme attribute not found")
			require.True(t, webThemeFound, "web-app theme attribute not found")

			_ = svc.DeleteProfileSchema(context.Background(), SuperTenantOrg)
		})

		t.Run("Add_SameAttributeNameSameApp_ShouldFail", func(t *testing.T) {
			// Test that duplicate attribute for the SAME application is blocked
			app1Attr := model.ProfileSchemaAttribute{
				OrgId:                 SuperTenantOrg,
				AttributeId:           uuid.New().String(),
				AttributeName:         "application_data.language",
				ValueType:             constants.StringDataType,
				MergeStrategy:         "overwrite",
				Mutability:            constants.MutabilityReadWrite,
				ApplicationIdentifier: "mobile-app",
			}
			_, err := svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{app1Attr}, constants.ApplicationData, SuperTenantOrg)
			require.NoError(t, err, "Failed to add language attribute for mobile-app")

			// Try to add the SAME attribute for the SAME application - should fail
			duplicateAttr := model.ProfileSchemaAttribute{
				OrgId:                 SuperTenantOrg,
				AttributeId:           uuid.New().String(),
				AttributeName:         "application_data.language",
				ValueType:             constants.StringDataType,
				MergeStrategy:         "overwrite",
				Mutability:            constants.MutabilityReadWrite,
				ApplicationIdentifier: "mobile-app",
			}
			_, err = svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{duplicateAttr}, constants.ApplicationData, SuperTenantOrg)
			require.Error(t, err, "Should not allow duplicate attribute for same application")
			errDesc := utils.ExtractErrorDescription(err)
			require.Contains(t, errDesc, "already exists for application", "Expected error message about duplicate attribute")

			_ = svc.DeleteProfileSchema(context.Background(), SuperTenantOrg)
		})

		t.Run("Add_TooDeepAttribute_ShouldFail", func(t *testing.T) {
			attr := createAttr(SuperTenantOrg, "traits.orders.payment.card.type.extra", constants.StringDataType, "combine", constants.MutabilityReadWrite)
			_, err := svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{attr}, constants.Traits, SuperTenantOrg)
			errDesc := utils.ExtractErrorDescription(err)
			require.Contains(t, errDesc, "Attribute exceeds the maximum depth of 4", "Expected validation failure for attribute depth > 4")
		})

		t.Run("Add_MaxDepthAttribute_ShouldSucceed", func(t *testing.T) {
			attr := createAttr(SuperTenantOrg, "traits.orders.payment.card.type", constants.StringDataType, "combine", constants.MutabilityReadWrite)
			_, err := svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{attr}, constants.Traits, SuperTenantOrg)
			require.NoError(t, err, "Expected success for depth 4 attribute")
			_ = svc.DeleteProfileSchema(context.Background(), SuperTenantOrg)
			_ = svc.DeleteProfileSchemaAttributesByScope(context.Background(), SuperTenantOrg, constants.IdentityAttributes)
		})

		t.Run("Add_ValidSubAttribute_ShouldSucceed", func(t *testing.T) {
			// Create sub-attribute first
			subAttr := createAttr(SuperTenantOrg, "traits.orders.payment", constants.StringDataType, "combine", constants.MutabilityReadWrite)
			_, err := svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{subAttr}, constants.Traits, SuperTenantOrg)
			require.NoError(t, err)

			// Create parent with sub-attribute (valid: one level deeper)
			parent := model.ProfileSchemaAttribute{
				OrgId:         SuperTenantOrg,
				AttributeId:   uuid.New().String(),
				AttributeName: "traits.orders",
				ValueType:     constants.ComplexDataType,
				MergeStrategy: "combine",
				Mutability:    constants.MutabilityReadWrite,
				SubAttributes: []model.SubAttribute{
					{
						AttributeId:   subAttr.AttributeId,
						AttributeName: subAttr.AttributeName,
					},
				},
			}

			_, err = svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{parent}, constants.Traits, SuperTenantOrg)
			require.NoError(t, err, "Expected success for valid sub-attribute relationship")
			_ = svc.DeleteProfileSchema(context.Background(), SuperTenantOrg)
			_ = svc.DeleteProfileSchemaAttributesByScope(context.Background(), SuperTenantOrg, constants.IdentityAttributes)
		})

		t.Run("Add_InvalidSubAttribute_ShouldFail", func(t *testing.T) {
			// Step 1: Create sub-attribute first
			subAttr := model.ProfileSchemaAttribute{
				OrgId:         SuperTenantOrg,
				AttributeId:   uuid.New().String(),
				AttributeName: "traits.orders.payment.card.type", // depth 4
				ValueType:     constants.StringDataType,
				MergeStrategy: "combine",
				Mutability:    constants.MutabilityReadWrite,
			}
			_, err := svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{subAttr}, constants.Traits, SuperTenantOrg)
			require.NoError(t, err, "Sub-attribute creation failed unexpectedly")

			// Step 2: Parent referencing invalid deeper sub-attribute
			parent := model.ProfileSchemaAttribute{
				OrgId:         SuperTenantOrg,
				AttributeId:   uuid.New().String(),
				AttributeName: "traits.orders",
				ValueType:     constants.ComplexDataType,
				MergeStrategy: "combine",
				Mutability:    constants.MutabilityReadWrite,
				SubAttributes: []model.SubAttribute{
					{
						AttributeId:   subAttr.AttributeId,
						AttributeName: subAttr.AttributeName,
					},
				},
			}

			_, err = svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{parent}, constants.Traits, SuperTenantOrg)
			errDesc := utils.ExtractErrorDescription(err)
			require.Contains(t, errDesc, "one level deeper", "Expected failure due to invalid sub-attribute depth")
			_ = svc.DeleteProfileSchema(context.Background(), SuperTenantOrg)
			_ = svc.DeleteProfileSchemaAttributesByScope(context.Background(), SuperTenantOrg, constants.IdentityAttributes)
		})

		t.Run("Add_MaxDepthHierarchy_ShouldSucceed", func(t *testing.T) {
			// 1️ Level 4 leaf
			l4 := createAttr(SuperTenantOrg, "traits.orders.payment.card.type",
				constants.StringDataType, "combine", constants.MutabilityReadWrite)
			_, err := svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{l4}, constants.Traits, SuperTenantOrg)
			require.NoError(t, err, "Failed to add level 4 attribute")

			// 2️Level 3 parent (complex) → references level 4
			l3 := model.ProfileSchemaAttribute{
				OrgId:         SuperTenantOrg,
				AttributeId:   uuid.New().String(),
				AttributeName: "traits.orders.payment.card",
				ValueType:     constants.ComplexDataType,
				MergeStrategy: "combine",
				Mutability:    constants.MutabilityReadWrite,
				SubAttributes: []model.SubAttribute{
					{AttributeId: l4.AttributeId, AttributeName: l4.AttributeName},
				},
			}
			_, err = svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{l3}, constants.Traits, SuperTenantOrg)
			require.NoError(t, err, "Failed to add level 3 attribute")

			// 3️ Level 2 parent → references level 3
			l2 := model.ProfileSchemaAttribute{
				OrgId:         SuperTenantOrg,
				AttributeId:   uuid.New().String(),
				AttributeName: "traits.orders.payment",
				ValueType:     constants.ComplexDataType,
				MergeStrategy: "combine",
				Mutability:    constants.MutabilityReadWrite,
				SubAttributes: []model.SubAttribute{
					{AttributeId: l3.AttributeId, AttributeName: l3.AttributeName},
				},
			}
			_, err = svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{l2}, constants.Traits, SuperTenantOrg)
			require.NoError(t, err, "Failed to add level 2 attribute")

			// 4️Level 1 parent → references level 2
			l1 := model.ProfileSchemaAttribute{
				OrgId:         SuperTenantOrg,
				AttributeId:   uuid.New().String(),
				AttributeName: "traits.orders",
				ValueType:     constants.ComplexDataType,
				MergeStrategy: "combine",
				Mutability:    constants.MutabilityReadWrite,
				SubAttributes: []model.SubAttribute{
					{AttributeId: l2.AttributeId, AttributeName: l2.AttributeName},
				},
			}
			_, err = svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{l1}, constants.Traits, SuperTenantOrg)
			require.NoError(t, err, "Failed to add top-level parent attribute")

			//  Everything should pass, proving depth=4 hierarchy works correctly
			_ = svc.DeleteProfileSchemaAttributesByScope(context.Background(), SuperTenantOrg, constants.Traits)
		})

	})

	t.Run("Get Operations", func(t *testing.T) {

		identityAttrs := []model.ProfileSchemaAttribute{
			createAttr(SuperTenantOrg, "identity_attributes.email", constants.StringDataType, "combine", constants.MutabilityReadWrite),
			createAttr(SuperTenantOrg, "identity_attributes.phone", constants.StringDataType, "combine", constants.MutabilityReadWrite),
		}
		_, _ = svc.AddProfileSchemaAttributesForScope(context.Background(), identityAttrs, constants.IdentityAttributes, SuperTenantOrg)

		// 2. Traits
		traits := []model.ProfileSchemaAttribute{
			{
				OrgId:         SuperTenantOrg,
				AttributeId:   uuid.New().String(),
				AttributeName: "traits.interests",
				ValueType:     constants.StringDataType,
				MergeStrategy: "combine",
				Mutability:    constants.MutabilityReadWrite,
				MultiValued:   true,
			},
		}
		_, _ = svc.AddProfileSchemaAttributesForScope(context.Background(), traits, constants.Traits, SuperTenantOrg)

		// 3. Application Data
		appData := []model.ProfileSchemaAttribute{
			{
				OrgId:                 SuperTenantOrg,
				AttributeId:           uuid.New().String(),
				AttributeName:         "application_data.device_id",
				ValueType:             constants.StringDataType,
				MergeStrategy:         "combine",
				Mutability:            constants.MutabilityReadWrite,
				MultiValued:           true,
				ApplicationIdentifier: "app_1",
			},
		}
		_, _ = svc.AddProfileSchemaAttributesForScope(context.Background(), appData, constants.ApplicationData, SuperTenantOrg)

		t.Run("Get_ProfileSchema_Success", func(t *testing.T) {
			schema, err := svc.GetProfileSchema(context.Background(), SuperTenantOrg)
			require.NoError(t, err)
			require.NotNil(t, schema)
			require.Contains(t, schema, constants.IdentityAttributes)
			require.Contains(t, schema, constants.ApplicationData)
			require.Contains(t, schema, constants.Traits)
		})

		t.Run("Get_ById_ShouldReturnMatchingAttribute", func(t *testing.T) {
			attr := createAttr(SuperTenantOrg, "identity_attributes.phone_number", constants.StringDataType, "combine", constants.MutabilityReadWrite)
			_, err := svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{attr}, constants.IdentityAttributes, SuperTenantOrg)
			require.NoError(t, err)

			fetched, err := svc.GetProfileSchemaAttributeById(context.Background(), SuperTenantOrg, attr.AttributeId)
			require.NoError(t, err)
			require.Equal(t, attr.AttributeName, fetched.AttributeName)
		})

		t.Run("Get_ByScopeAndFilter_ShouldReturnFilteredResults", func(t *testing.T) {
			filters := []string{"attribute_name eq identity_attributes.email"}
			filtered, err := svc.GetProfileSchemaAttributesByScopeAndFilter(context.Background(), SuperTenantOrg, constants.IdentityAttributes, filters)
			require.NoError(t, err)
			require.NotNil(t, filtered)
			require.NotEmpty(t, filtered)
		})
	})

	t.Run("Update Operations", func(t *testing.T) {
		t.Run("Patch_ProfileSchemaAttribute_ById", func(t *testing.T) {
			attrId := uuid.New().String()
			attr := createAttr(SuperTenantOrg, "identity_attributes.temp_field", constants.StringDataType, "combine", constants.MutabilityReadWrite)
			attr.AttributeId = attrId

			_, err := svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{attr}, constants.IdentityAttributes, SuperTenantOrg)
			require.NoError(t, err)

			updates := map[string]interface{}{
				"attribute_name": "identity_attributes.temp_field",
				"attribute_id":   attrId,
				"value_type":     "integer",
				"merge_strategy": constants.MergeStrategyOverwrite,
				"mutability":     constants.MutabilityReadWrite,
			}

			err = svc.UpdateProfileSchemaAttributeById(context.Background(), SuperTenantOrg, attrId, updates, "")
			require.NoError(t, err, "Failed to patch profile schema attribute")

			// Verify patch persisted
			patched, err := svc.GetProfileSchemaAttributeById(context.Background(), SuperTenantOrg, attrId)
			require.NoError(t, err)
			require.Equal(t, "integer", patched.ValueType)
			require.Equal(t, constants.MergeStrategyOverwrite, patched.MergeStrategy)
		})
	})

	t.Run("Delete Operations", func(t *testing.T) {

		t.Run("Delete_ProfileSchemaAttribute_ById", func(t *testing.T) {
			attr := createAttr(SuperTenantOrg, "traits.to_delete", constants.StringDataType, "combine", constants.MutabilityReadWrite)
			_, err := svc.AddProfileSchemaAttributesForScope(context.Background(), []model.ProfileSchemaAttribute{attr}, constants.Traits, SuperTenantOrg)
			require.NoError(t, err)

			err = svc.DeleteProfileSchemaAttributeById(context.Background(), SuperTenantOrg, attr.AttributeId)
			require.NoError(t, err, "Expected no error when deleting schema attribute by ID")
		})

		t.Run("Delete_ProfileSchemaAttributes_ByScope", func(t *testing.T) {
			err := svc.DeleteProfileSchemaAttributesByScope(context.Background(), SuperTenantOrg, constants.IdentityAttributes)
			require.NoError(t, err, "Expected no error when deleting all schema attributes by scope")
		})
	})

	t.Cleanup(func() {
		_ = svc.DeleteProfileSchema(context.Background(), SuperTenantOrg)
		_ = svc.DeleteProfileSchemaAttributesByScope(context.Background(), SuperTenantOrg, constants.IdentityAttributes)
	})
}
