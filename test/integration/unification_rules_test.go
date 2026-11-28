/*
 * Copyright (c) (2025-2026), Choreo Labs LLC. (https://choreolabs.com).
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

	"github.com/wso2/identity-customer-data-service/test/setup"

	"github.com/google/uuid"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/test/integration/utils"

	"github.com/stretchr/testify/require"

	profileSchema "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/service"
)

func Test_UnificationRule(t *testing.T) {
	SuperTenantOrg := setup.TestOrg(t, fmt.Sprintf("carbon.super-%d", time.Now().UnixNano()))
	profileSchemaService := schemaService.GetProfileSchemaService()
	restore := schemaService.OverrideValidateApplicationIdentifierForTest(
		// bypass app verification with IDP
		func(context.Context, string, string) (error, bool) { return nil, true })
	defer restore()

	t.Run("Pre-requisite: Add_schema_attribute", func(t *testing.T) {
		schemaAttributes := []profileSchema.ProfileSchemaAttribute{
			{
				OrgID:         SuperTenantOrg.ID,
				AttributeName: "identity_attributes.email",
				AttributeID:   uuid.New().String(),
				ValueType:     constants.StringDataType,
				MergeStrategy: "combine",
				Mutability:    constants.MutabilityReadWrite,
			},
		}
		_, err := profileSchemaService.AddProfileSchemaAttributesForScope(
			context.Background(),
			schemaAttributes,
			constants.IdentityAttributes,
			SuperTenantOrg.ID,
		)
		require.NoError(t, err, "Failed to add enrichment rule dependency")
	})

	unificationRuleService := service.GetUnificationRuleService()

	rule := model.UnificationRule{
		RuleName:     "Email based",
		RuleID:       uuid.New().String(),
		OrgID:        SuperTenantOrg.ID,
		PropertyName: "identity_attributes.email",
		Priority:     1,
		IsActive:     true,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}

	t.Run("Add_unification_rule", func(t *testing.T) {
		err := unificationRuleService.AddUnificationRule(context.Background(), rule, SuperTenantOrg.ID)
		require.NoError(t, err, "Failed to add unification rule")
	})

	t.Run("Reject_complex_attribute_in_unification_rule", func(t *testing.T) {
		//  Add a valid sub-attribute
		subAttr := profileSchema.ProfileSchemaAttribute{
			OrgID:         SuperTenantOrg.ID,
			AttributeID:   uuid.New().String(),
			AttributeName: "traits.orders.payment.method",
			ValueType:     constants.StringDataType,
			MergeStrategy: "combine",
			Mutability:    constants.MutabilityReadWrite,
		}
		_, err := profileSchemaService.AddProfileSchemaAttributesForScope(
			context.Background(),
			[]profileSchema.ProfileSchemaAttribute{subAttr},
			constants.Traits,
			SuperTenantOrg.ID,
		)
		require.NoError(t, err, "Failed to add sub-attribute to schema")

		//  Add parent complex attribute referencing sub-attribute
		parentAttr := profileSchema.ProfileSchemaAttribute{
			OrgID:         SuperTenantOrg.ID,
			AttributeID:   uuid.New().String(),
			AttributeName: "traits.orders.payment",
			ValueType:     constants.ComplexDataType,
			MergeStrategy: "combine",
			Mutability:    constants.MutabilityReadWrite,
			SubAttributes: []profileSchema.SubAttribute{
				{
					AttributeID:   subAttr.AttributeID,
					AttributeName: subAttr.AttributeName,
				},
			},
		}
		_, err = profileSchemaService.AddProfileSchemaAttributesForScope(
			context.Background(),
			[]profileSchema.ProfileSchemaAttribute{parentAttr},
			constants.Traits,
			SuperTenantOrg.ID,
		)
		require.NoError(t, err, "Failed to add complex attribute to schema")

		// Try creating a unification rule with that complex attribute
		rule := model.UnificationRule{
			RuleName:     "Payment based",
			RuleID:       uuid.New().String(),
			OrgID:        SuperTenantOrg.ID,
			PropertyName: "traits.orders.payment",
			Priority:     2,
			IsActive:     true,
			CreatedAt:    time.Now().UTC(),
			UpdatedAt:    time.Now().UTC(),
		}

		err = unificationRuleService.AddUnificationRule(context.Background(), rule, SuperTenantOrg.ID)
		errDesc := utils.ExtractErrorDescription(err)
		require.Contains(
			t,
			errDesc,
			"not allowed as it is a complex data type",
			"Expected validation message for complex attribute rejection",
		)
	})

	t.Run("Get_all_unification_rules", func(t *testing.T) {
		rules, err := unificationRuleService.GetUnificationRules(context.Background(), SuperTenantOrg.ID)
		require.NoError(t, err, "Failed to fetch unification rules")
		require.NotEmpty(t, rules, "Unification rule list is empty")
	})

	t.Run("Update_unification_rule", func(t *testing.T) {
		rule.IsActive = false // reflect change in local object
		err := unificationRuleService.PatchUnificationRule(context.Background(), rule.RuleID, SuperTenantOrg.ID, rule)
		require.NoError(t, err, "Failed to patch unification rule")

		updated, err := unificationRuleService.GetUnificationRule(context.Background(), rule.RuleID, SuperTenantOrg.ID)
		require.NoError(t, err, "Failed to fetch updated rule")
		require.False(t, updated.IsActive, "Expected is_active to be false")
	})

	t.Run("Delete_unification_rule", func(t *testing.T) {
		err := unificationRuleService.DeleteUnificationRule(context.Background(), rule.RuleID, SuperTenantOrg.ID)
		require.NoError(t, err, "Failed to delete unification rule")
	})

	// Todo : Add cases for each unification rule and ensure they are functioning correct

	t.Cleanup(func() {
		rules, _ := unificationRuleService.GetUnificationRules(context.Background(), SuperTenantOrg.ID)
		for _, r := range rules {
			_ = unificationRuleService.DeleteUnificationRule(context.Background(), r.RuleID, SuperTenantOrg.ID)
		}
		_ = profileSchemaService.DeleteProfileSchema(context.Background(), SuperTenantOrg.ID)
		_ = profileSchemaService.DeleteProfileSchemaAttributesByScope(
			context.Background(),
			SuperTenantOrg.ID,
			constants.IdentityAttributes,
		)
	})
}
