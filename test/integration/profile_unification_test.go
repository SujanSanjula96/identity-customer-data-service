/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
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
	"github.com/stretchr/testify/require"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	unificationService "github.com/wso2/identity-customer-data-service/internal/unification_rules/service"
)

// Helper for unmarshalling JSON into ProfileRequest
func mustUnmarshalProfile(jsonStr string) profileModel.ProfileRequest {
	var p profileModel.ProfileRequest
	if err := json.Unmarshal([]byte(jsonStr), &p); err != nil {
		panic(err)
	}
	return p
}

func Test_Profile_Unification_Scenarios(t *testing.T) {

	RuleNamePhoneBased := "phone_based"
	RuleNameEmailBased := "email_based"
	AppId := "test-app-001"
	AppA_Id := "app-A"
	AppB_Id := "app-B"
	SuperTenantOrg := fmt.Sprintf("carbon.super-%d", time.Now().UnixNano())

	restore := schemaService.OverrideValidateApplicationIdentifierForTest(
		// bypass app verification with IDP
		func(context.Context, string, string) (error, bool) { return nil, true })
	defer restore()

	// Initialize Profile Schema Attributes
	profileSchemaSvc := schemaService.GetProfileSchemaService()

	identityAttr := []schemaModel.ProfileSchemaAttribute{
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(), AttributeName: "identity_attributes.email", ValueType: constants.StringDataType, MergeStrategy: "combine",
			Mutability: constants.MutabilityReadWrite, MultiValued: true},
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(), AttributeName: "identity_attributes.phone_number", ValueType: constants.StringDataType,
			MergeStrategy: "combine", Mutability: constants.MutabilityReadWrite, MultiValued: true},
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(), AttributeName: "identity_attributes.user_id", ValueType: constants.StringDataType, MergeStrategy: "combine",
			Mutability: constants.MutabilityReadWrite},
	}

	traits := []schemaModel.ProfileSchemaAttribute{
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(), AttributeName: "traits.interests", ValueType: constants.StringDataType, MergeStrategy: "combine",
			Mutability: constants.MutabilityReadWrite, MultiValued: true},
	}

	appData := []schemaModel.ProfileSchemaAttribute{
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(), AttributeName: "application_data.device_id", ValueType: constants.StringDataType, MergeStrategy: "combine",
			Mutability: constants.MutabilityReadWrite, MultiValued: true, ApplicationIdentifier: AppId},
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(),
			AttributeName: "application_data.ui_mode",
			ValueType:     constants.StringDataType, MergeStrategy: "overwrite",
			Mutability: constants.MutabilityReadWrite, ApplicationIdentifier: AppA_Id},
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(),
			AttributeName: "application_data.ui_mode",
			ValueType:     constants.StringDataType, MergeStrategy: "overwrite",
			Mutability: constants.MutabilityReadWrite, ApplicationIdentifier: AppB_Id},
	}

	_, err := profileSchemaSvc.AddProfileSchemaAttributesForScope(context.Background(), identityAttr, constants.IdentityAttributes, SuperTenantOrg)
	_, err1 := profileSchemaSvc.AddProfileSchemaAttributesForScope(context.Background(), traits, constants.Traits, SuperTenantOrg)
	_, err2 := profileSchemaSvc.AddProfileSchemaAttributesForScope(context.Background(), appData, constants.ApplicationData, SuperTenantOrg)
	require.NoError(t, err)
	require.NoError(t, err1)
	require.NoError(t, err2)

	// Setup Unification Rules
	profileSvc := profileService.GetProfilesService()
	unificationSvc := unificationService.GetUnificationRuleService()

	emailRuleId := uuid.New().String()
	emailBasedRule := model.UnificationRule{
		RuleName:     RuleNameEmailBased,
		RuleId:       emailRuleId,
		OrgHandle:    SuperTenantOrg,
		PropertyName: "identity_attributes.email",
		Priority:     1,
		IsActive:     true,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	_ = unificationSvc.AddUnificationRule(context.Background(), emailBasedRule, SuperTenantOrg)

	phoneRuleId := uuid.New().String()
	phoneBasedRule := model.UnificationRule{
		RuleName:     RuleNamePhoneBased,
		RuleId:       phoneRuleId,
		OrgHandle:    SuperTenantOrg,
		PropertyName: "identity_attributes.phone_number",
		Priority:     2,
		IsActive:     true,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}

	_ = unificationSvc.AddUnificationRule(context.Background(), phoneBasedRule, SuperTenantOrg)

	t.Run("Scenario1_TempProfiles_Email_Then_Phone_Unify", func(t *testing.T) {
		p1 := mustUnmarshalProfile(`{"identity_attributes":{"email":["a@wso2.com"]},"traits":{"interests":["music"]}}`)
		p2 := mustUnmarshalProfile(`{"identity_attributes":{"email":["a@wso2.com"],"phone_number":["0771234567"]},"traits":{"interests":["sports"]}}`)
		p3 := mustUnmarshalProfile(`{"identity_attributes":{"phone_number":["0771234567"]},"traits":{"interests":["art"]}}`)

		prof1, _ := profileSvc.CreateProfile(context.Background(), p1, SuperTenantOrg)
		prof2, _ := profileSvc.CreateProfile(context.Background(), p2, SuperTenantOrg)
		prof3, _ := profileSvc.CreateProfile(context.Background(), p3, SuperTenantOrg)

		time.Sleep(2 * time.Second)

		merged1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		merged2, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)
		merged3, _ := profileSvc.GetProfile(context.Background(), prof3.ProfileId)

		require.Equal(t, merged1.MergedTo.ProfileId, merged2.MergedTo.ProfileId)
		require.Equal(t, RuleNameEmailBased, merged1.MergedTo.Reason)
		require.Equal(t, merged2.MergedTo.ProfileId, merged3.MergedTo.ProfileId)
		require.Equal(t, RuleNamePhoneBased, merged3.MergedTo.Reason)

		require.Contains(t, merged3.IdentityAttributes["email"].([]interface{}), "a@wso2.com")
		require.Contains(t, merged1.IdentityAttributes["phone_number"].([]interface{}), "0771234567")
		require.ElementsMatch(t, merged2.Traits["interests"].([]interface{}), []interface{}{"music", "sports", "art"})

		cleanProfiles(profileSvc, SuperTenantOrg)
	})

	t.Run("Scenario2_TempAndPerm_Merge_UserId_Inherit", func(t *testing.T) {
		temp := mustUnmarshalProfile(`{"identity_attributes":{"email":["b@wso2.com"],"phone_number":["0774567890"]},"traits":{"interests":["music"]}}`)
		perm := mustUnmarshalProfile(`{"user_id":"user-123","identity_attributes":{"user_id": "user-123", "email":["b@wso2.com","b2@wso2.com"]},"traits":{"interests":["sports"]}}`)

		p1, _ := profileSvc.CreateProfile(context.Background(), temp, SuperTenantOrg)
		p2, _ := profileSvc.CreateProfile(context.Background(), perm, SuperTenantOrg)
		time.Sleep(2 * time.Second)

		merged1, _ := profileSvc.GetProfile(context.Background(), p1.ProfileId)
		merged2, _ := profileSvc.GetProfile(context.Background(), p2.ProfileId)

		require.Equal(t, merged1.MergedTo.ProfileId, merged2.ProfileId)
		require.Equal(t, RuleNameEmailBased, merged1.MergedTo.Reason)

		require.Contains(t, merged1.IdentityAttributes["email"].([]interface{}), "b2@wso2.com")
		require.ElementsMatch(t, []interface{}{"music", "sports"}, merged2.Traits["interests"].([]interface{}))
		require.Contains(t, merged2.IdentityAttributes["phone_number"].([]interface{}), "0774567890")

		cleanProfiles(profileSvc, SuperTenantOrg)

	})

	t.Run("Scenario3_TempTempThenPerm_Merge", func(t *testing.T) {
		temp1 := mustUnmarshalProfile(`{"identity_attributes":{"email":["c@wso2.com"],"phone_number":["0771111111"]},"traits":{"interests":["music"]}}`)
		temp2 := mustUnmarshalProfile(`{"identity_attributes":{"email":["c@wso2.com"]},"traits":{"interests":["art"]}}`)
		perm := mustUnmarshalProfile(`{"user_id":"perm-789","identity_attributes":{"user_id": "perm-789","phone_number":["0771111111"]},"traits":{"interests":["sports"]}}`)

		p1, _ := profileSvc.CreateProfile(context.Background(), temp1, SuperTenantOrg)
		p2, _ := profileSvc.CreateProfile(context.Background(), temp2, SuperTenantOrg)
		time.Sleep(2 * time.Second)
		p3, _ := profileSvc.CreateProfile(context.Background(), perm, SuperTenantOrg)
		time.Sleep(2 * time.Second)

		merged1, _ := profileSvc.GetProfile(context.Background(), p1.ProfileId)
		merged2, _ := profileSvc.GetProfile(context.Background(), p2.ProfileId)
		merged3, _ := profileSvc.GetProfile(context.Background(), p3.ProfileId)

		require.Equal(t, merged1.MergedTo.ProfileId, merged2.MergedTo.ProfileId)
		require.Equal(t, RuleNameEmailBased, merged1.MergedTo.Reason)

		require.Equal(t, merged2.MergedTo.ProfileId, merged3.ProfileId)
		// todo: Verify if this is correct. Should it be PHONE_BASED since we merge perm to merged profiles of Temp using phone number?
		require.Equal(t, RuleNameEmailBased, merged2.MergedTo.Reason)

		require.Equal(t, "perm-789", merged1.IdentityAttributes["user_id"])
		require.ElementsMatch(t, []interface{}{"music", "art", "sports"}, merged3.Traits["interests"].([]interface{}))
		require.Contains(t, merged2.IdentityAttributes["phone_number"].([]interface{}), "0771111111")
		require.Contains(t, merged3.IdentityAttributes["email"].([]interface{}), "c@wso2.com")

		cleanProfiles(profileSvc, SuperTenantOrg)

	})

	t.Run("Scenario4_TempPermThenAnotherTemp", func(t *testing.T) {
		perm := mustUnmarshalProfile(`{"user_id":"perm-001","identity_attributes":{"user_id":"perm-001","email":["d@wso2.com"]},"traits":{"interests":["music"]}}`)
		temp := mustUnmarshalProfile(`{"identity_attributes":{"email":["d@wso2.com"],"phone_number":["0775554444"]},"traits":{"interests":["art"]}}`)
		temp2 := mustUnmarshalProfile(`{"identity_attributes":{"phone_number":["0775554444"]},"traits":{"interests":["sports"]}}`)

		p1, _ := profileSvc.CreateProfile(context.Background(), perm, SuperTenantOrg)
		p2, _ := profileSvc.CreateProfile(context.Background(), temp, SuperTenantOrg)
		p3, _ := profileSvc.CreateProfile(context.Background(), temp2, SuperTenantOrg)
		time.Sleep(2 * time.Second)

		merged1, _ := profileSvc.GetProfile(context.Background(), p1.ProfileId)
		merged2, _ := profileSvc.GetProfile(context.Background(), p2.ProfileId)
		merged3, _ := profileSvc.GetProfile(context.Background(), p3.ProfileId)

		require.Equal(t, merged1.ProfileId, merged2.MergedTo.ProfileId)
		require.Equal(t, RuleNameEmailBased, merged2.MergedTo.Reason)
		require.Equal(t, merged1.ProfileId, merged3.MergedTo.ProfileId)
		require.Equal(t, RuleNamePhoneBased, merged3.MergedTo.Reason)

		require.ElementsMatch(t, []interface{}{"music", "art", "sports"}, merged3.Traits["interests"].([]interface{}))
		require.Contains(t, merged1.IdentityAttributes["phone_number"].([]interface{}), "0775554444")
		require.Contains(t, merged3.IdentityAttributes["email"].([]interface{}), "d@wso2.com")

		cleanProfiles(profileSvc, SuperTenantOrg)

	})

	t.Run("Scenario5_TwoPermanent_SameEmail_NoMerge", func(t *testing.T) {
		perm1 := mustUnmarshalProfile(`{"user_id":"perm-A","identity_attributes":{"email":["e@wso2.com"]}}`)
		perm2 := mustUnmarshalProfile(`{"user_id":"perm-B","identity_attributes":{"email":["e@wso2.com"]}}`)

		p1, _ := profileSvc.CreateProfile(context.Background(), perm1, SuperTenantOrg)
		p2, _ := profileSvc.CreateProfile(context.Background(), perm2, SuperTenantOrg)
		time.Sleep(2 * time.Second)

		require.Empty(t, p1.MergedTo)
		require.Empty(t, p2.MergedTo)

		cleanProfiles(profileSvc, SuperTenantOrg)

	})

	t.Run("Scenario6_RulePriority_HigherPriorityWins", func(t *testing.T) {
		temp1 := mustUnmarshalProfile(`{"identity_attributes":{"email":["f@wso2.com"],"phone_number":["0777777777"]}}`)
		temp2 := mustUnmarshalProfile(`{"identity_attributes":{"email":["f@wso2.com"],"phone_number":["0777777777"]}}`)

		p1, _ := profileSvc.CreateProfile(context.Background(), temp1, SuperTenantOrg)
		p2, _ := profileSvc.CreateProfile(context.Background(), temp2, SuperTenantOrg)

		time.Sleep(2 * time.Second)

		merged1, _ := profileSvc.GetProfile(context.Background(), p1.ProfileId)
		merged2, _ := profileSvc.GetProfile(context.Background(), p2.ProfileId)

		require.Equal(t, merged1.MergedTo.ProfileId, merged2.MergedTo.ProfileId)
		require.Equal(t, RuleNameEmailBased, merged2.MergedTo.Reason)

		cleanProfiles(profileSvc, SuperTenantOrg)
	})

	t.Run("Scenario7_InactiveRule_ShouldPreventUnification", func(t *testing.T) {

		emailBasedRule.IsActive = false
		err = unificationSvc.PatchUnificationRule(context.Background(), emailRuleId, SuperTenantOrg, emailBasedRule)
		require.NoError(t, err, "Failed to deactivate email based unification rule")
		rule, _ := unificationSvc.GetUnificationRule(context.Background(), emailRuleId)
		require.Equal(t, false, rule.IsActive)

		p1 := mustUnmarshalProfile(`{"identity_attributes":{"email":["g@wso2.com"]}}`)
		p2 := mustUnmarshalProfile(`{"identity_attributes":{"email":["g@wso2.com"]}}`)

		prof1, _ := profileSvc.CreateProfile(context.Background(), p1, SuperTenantOrg)
		prof2, _ := profileSvc.CreateProfile(context.Background(), p2, SuperTenantOrg)
		time.Sleep(2 * time.Second)

		mergedProfile1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		mergedProfile2, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)

		require.Empty(t, mergedProfile1.MergedTo)
		require.Empty(t, mergedProfile2.MergedTo)

		emailBasedRule.IsActive = true
		err = unificationSvc.PatchUnificationRule(context.Background(), emailRuleId, SuperTenantOrg, emailBasedRule)
		require.NoError(t, err, "Failed to reactivate email based unification rule")
		cleanProfiles(profileSvc, SuperTenantOrg)

	})

	t.Run("Scenario8_RuleChange_ShouldNotSplitExisting", func(t *testing.T) {
		p1 := mustUnmarshalProfile(`{"identity_attributes":{"email":["j@wso2.com"]}}`)
		p2 := mustUnmarshalProfile(`{"identity_attributes":{"email":["j@wso2.com"]}}`)

		prof1, _ := profileSvc.CreateProfile(context.Background(), p1, SuperTenantOrg)
		prof2, _ := profileSvc.CreateProfile(context.Background(), p2, SuperTenantOrg)
		time.Sleep(2 * time.Second)

		merged1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		merged2, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)
		require.Equal(t, merged1.MergedTo.ProfileId, merged2.MergedTo.ProfileId)

		_ = unificationSvc.DeleteUnificationRule(context.Background(), emailRuleId)

		after1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		after2, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)

		merged1, _ = profileSvc.GetProfile(context.Background(), after1.ProfileId)
		merged2, _ = profileSvc.GetProfile(context.Background(), after2.ProfileId)
		require.Equal(t, merged1.MergedTo.ProfileId, merged2.MergedTo.ProfileId)
		cleanProfiles(profileSvc, SuperTenantOrg)

	})

	t.Run("Scenario9_CrossTenantProfiles_ShouldNotUnify", func(t *testing.T) {

		OtherTenant := fmt.Sprintf("other.org-%d", time.Now().UnixNano())
		identityAttr := []schemaModel.ProfileSchemaAttribute{
			{OrgId: OtherTenant, AttributeId: uuid.New().String(), AttributeName: "identity_attributes.email", ValueType: constants.StringDataType, MergeStrategy: "combine",
				Mutability: constants.MutabilityReadWrite, MultiValued: true},
		}
		_, err = profileSchemaSvc.AddProfileSchemaAttributesForScope(context.Background(), identityAttr, constants.IdentityAttributes, OtherTenant)

		emailRuleId := uuid.New().String()
		jsonData := []byte(`{
	   "rule_name": "` + RuleNameEmailBased + `",
		"rule_id": "` + emailRuleId + `",
		"org_handle": "` + OtherTenant + `",
	   "property_name": "identity_attributes.email",
	   "priority": 1,
	   "is_active": true
		}`)

		var emailBasedRule model.UnificationRule
		unificationErr := json.Unmarshal(jsonData, &emailBasedRule)
		require.NoError(t, unificationErr, "Failed to unmarshal rule JSON")

		emailBasedRule.CreatedAt = time.Now().UTC()
		emailBasedRule.UpdatedAt = time.Now().UTC()

		_ = unificationSvc.AddUnificationRule(context.Background(), emailBasedRule, SuperTenantOrg)

		p1 := mustUnmarshalProfile(`{"identity_attributes":{"email":["k@wso2.com"]}}`)
		p2 := mustUnmarshalProfile(`{"identity_attributes":{"email":["k@wso2.com"]}}`)

		prof1, _ := profileSvc.CreateProfile(context.Background(), p1, SuperTenantOrg)
		prof2, _ := profileSvc.CreateProfile(context.Background(), p2, OtherTenant)
		time.Sleep(5 * time.Second)

		merged1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		merged2, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)

		require.Empty(t, merged1.MergedTo)
		require.Empty(t, merged2.MergedTo)
		cleanProfiles(profileSvc, SuperTenantOrg)
		cleanProfiles(profileSvc, OtherTenant)
	})

	t.Run("Scenario10_AppAttribute_Separation", func(t *testing.T) {

		p1JSON := `{ "identity_attributes":{"email":["shared-app-test@wso2.com"]}, "application_data":{"` + AppA_Id + `":{"ui_mode":"dark"},"` + AppB_Id + `":{"ui_mode":"light"}}}`
		p1Req := mustUnmarshalProfile(p1JSON)

		p2JSON := `{"identity_attributes":{"email":["shared-app-test@wso2.com"]}, "application_data":{"` + AppB_Id + `":{"ui_mode":"light"}}}`
		p2Req := mustUnmarshalProfile(p2JSON)

		prof1, err1 := profileSvc.CreateProfile(context.Background(), p1Req, SuperTenantOrg)
		require.NoError(t, err1)
		prof2, err2 := profileSvc.CreateProfile(context.Background(), p2Req, SuperTenantOrg)
		require.NoError(t, err2)

		time.Sleep(2 * time.Second)

		merged1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		merged2, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)

		require.NotEmpty(t, merged1.MergedTo.ProfileId, "Merged1 should be merged")
		require.NotEmpty(t, merged2.MergedTo.ProfileId, "Merged2 should be merged")
		require.Equal(t, merged1.MergedTo.ProfileId, merged2.MergedTo.ProfileId, "Should merge to same profile")

		var master *profileModel.ProfileResponse
		if merged1.MergedTo.ProfileId == "" {
			master = merged1
		} else {
			master, _ = profileSvc.GetProfile(context.Background(), merged1.MergedTo.ProfileId)
		}

		// Master profile should have both app attributes intact
		appData := master.ApplicationData
		appAData := appData[AppA_Id]
		appBData := appData[AppB_Id]

		require.NotNil(t, appAData["ui_mode"], "App A ui_mode should not be nil")
		require.NotNil(t, appBData["ui_mode"], "App B ui_mode should not be nil")
		require.Equal(t, "dark", appAData["ui_mode"], "App A ui_mode should be 'dark'")

		// Profile 1 and Profile 2 should have their respective app attributes but not the others
		p1Final, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		p2Final, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)

		p1AppData := p1Final.ApplicationData
		p2AppData := p2Final.ApplicationData

		require.NotNil(t, p1AppData[AppA_Id]["ui_mode"], "Profile 1 ui_mode should not be nil")
		require.NotNil(t, p1AppData[AppB_Id]["ui_mode"], "Profile 1 ui_mode should not be nil")

		require.NotNil(t, p2AppData[AppB_Id]["ui_mode"], "Profile 2 ui_mode should not be nil")
		require.NotNil(t, p2AppData[AppA_Id]["ui_mode"], "Profile 2 ui_mode should not be nil")

		cleanProfiles(profileSvc, SuperTenantOrg)
	})

	// Cleanup
	t.Cleanup(func() {
		rules, _ := unificationSvc.GetUnificationRules(context.Background(), SuperTenantOrg)
		for _, r := range rules {
			_ = unificationSvc.DeleteUnificationRule(context.Background(), r.RuleId)
		}
		cleanProfiles(profileSvc, SuperTenantOrg)
		_ = profileSchemaSvc.DeleteProfileSchema(context.Background(), SuperTenantOrg)
		_ = profileSchemaSvc.DeleteProfileSchemaAttributesByScope(context.Background(), SuperTenantOrg, constants.IdentityAttributes)
	})
}

func cleanProfiles(profileSvc profileService.ProfilesServiceInterface, org string) {

	profiles, _, _ := profileSvc.GetAllProfilesCursor(context.Background(), org, 10, nil)
	for _, p := range profiles {
		_ = profileSvc.DeleteProfile(context.Background(), p.ProfileId)
	}
}
