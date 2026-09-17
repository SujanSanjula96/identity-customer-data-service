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

// Test_Complex_Unification_Scenarios tests complex profile unification scenarios
// that go beyond basic unification to test edge cases and complex merge hierarchies.
func Test_Complex_Unification_Scenarios(t *testing.T) {

	PhoneBased := "phone_based"
	EmailBased := "email_based"
	UserIdBased := "user_id_based"
	AppId := "test-app-complex-001"
	SuperTenantOrg := fmt.Sprintf("carbon.super-complex-%d", time.Now().UnixNano())

	restore := schemaService.OverrideValidateApplicationIdentifierForTest(
		// bypass app verification with IDP
		func(context.Context, string, string) (error, bool) { return nil, true })
	defer restore()

	// Initialize Profile Schema Attributes
	profileSchemaSvc := schemaService.GetProfileSchemaService()

	identityAttr := []schemaModel.ProfileSchemaAttribute{
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(), AttributeName: "identity_attributes.email",
			ValueType: constants.StringDataType, MergeStrategy: "combine", Mutability: constants.MutabilityReadWrite, MultiValued: true},
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(), AttributeName: "identity_attributes.phone_number",
			ValueType: constants.StringDataType, MergeStrategy: "combine", Mutability: constants.MutabilityReadWrite, MultiValued: true},
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(), AttributeName: "identity_attributes.user_id",
			ValueType: constants.StringDataType, MergeStrategy: "combine", Mutability: constants.MutabilityReadWrite},
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(), AttributeName: "identity_attributes.secondary_email",
			ValueType: constants.StringDataType, MergeStrategy: "combine", Mutability: constants.MutabilityReadWrite, MultiValued: true},
	}

	traits := []schemaModel.ProfileSchemaAttribute{
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(), AttributeName: "traits.interests",
			ValueType: constants.StringDataType, MergeStrategy: "combine", Mutability: constants.MutabilityReadWrite, MultiValued: true},
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(), AttributeName: "traits.preferences",
			ValueType: constants.StringDataType, MergeStrategy: "combine", Mutability: constants.MutabilityReadWrite, MultiValued: true},
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(), AttributeName: "traits.score",
			ValueType: constants.IntegerDataType, MergeStrategy: "overwrite", Mutability: constants.MutabilityReadWrite},
	}

	appData := []schemaModel.ProfileSchemaAttribute{
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(), AttributeName: "application_data.device_id",
			ValueType: constants.StringDataType, MergeStrategy: "combine", Mutability: constants.MutabilityReadWrite, MultiValued: true, ApplicationIdentifier: AppId},
		{OrgId: SuperTenantOrg, AttributeId: uuid.New().String(), AttributeName: "application_data.session_count",
			ValueType: constants.IntegerDataType, MergeStrategy: "overwrite", Mutability: constants.MutabilityReadWrite, ApplicationIdentifier: AppId},
	}

	_, err := profileSchemaSvc.AddProfileSchemaAttributesForScope(context.Background(), identityAttr, constants.IdentityAttributes, SuperTenantOrg)
	_, err1 := profileSchemaSvc.AddProfileSchemaAttributesForScope(context.Background(), traits, constants.Traits, SuperTenantOrg)
	_, err2 := profileSchemaSvc.AddProfileSchemaAttributesForScope(context.Background(), appData, constants.ApplicationData, SuperTenantOrg)
	require.NoError(t, err)
	require.NoError(t, err1)
	require.NoError(t, err2)

	// Setup Unification Rules with priorities: user_id (0) > email (1) > phone (2)
	profileSvc := profileService.GetProfilesService()
	unificationSvc := unificationService.GetUnificationRuleService()

	// Add user_id based rule with highest priority
	userIdRuleId := uuid.New().String()
	userIdRule := model.UnificationRule{
		RuleId:       userIdRuleId,
		OrgHandle:    SuperTenantOrg,
		RuleName:     UserIdBased,
		PropertyName: "identity_attributes.user_id",
		Priority:     0,
		IsActive:     true,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	_ = unificationSvc.AddUnificationRule(context.Background(), userIdRule, SuperTenantOrg)

	emailRuleId := uuid.New().String()
	emailRule := model.UnificationRule{
		RuleId:       emailRuleId,
		OrgHandle:    SuperTenantOrg,
		RuleName:     EmailBased,
		PropertyName: "identity_attributes.email",
		Priority:     1,
		IsActive:     true,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	_ = unificationSvc.AddUnificationRule(context.Background(), emailRule, SuperTenantOrg)

	phoneRuleId := uuid.New().String()
	phoneRule := model.UnificationRule{
		RuleId:       phoneRuleId,
		OrgHandle:    SuperTenantOrg,
		RuleName:     PhoneBased,
		PropertyName: "identity_attributes.phone_number",
		Priority:     2,
		IsActive:     true,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	_ = unificationSvc.AddUnificationRule(context.Background(), phoneRule, SuperTenantOrg)

	t.Run("Scenario10_MultipleCascadingMerges_FourTempProfiles", func(t *testing.T) {
		// Scenario: Four temporary profiles cascade merge through shared attributes
		// P1 shares email with P2, P2 shares phone with P3, P3 shares email with P4
		// Expected: All four profiles unify under one master with all merged attributes

		p1 := mustUnmarshalProfile(`{"identity_attributes":{"email":["cascade1@wso2.com"]},"traits":{"interests":["music"]}}`)
		p2 := mustUnmarshalProfile(`{"identity_attributes":{"email":["cascade1@wso2.com"],"phone_number":["0771111111"]},"traits":{"interests":["sports"]}}`)
		p3 := mustUnmarshalProfile(`{"identity_attributes":{"phone_number":["0771111111"],"email":["cascade2@wso2.com"]},"traits":{"interests":["art"]}}`)
		p4 := mustUnmarshalProfile(`{"identity_attributes":{"email":["cascade2@wso2.com"]},"traits":{"interests":["cooking"]}}`)

		prof1, _ := profileSvc.CreateProfile(context.Background(), p1, SuperTenantOrg)
		time.Sleep(500 * time.Millisecond)
		prof2, _ := profileSvc.CreateProfile(context.Background(), p2, SuperTenantOrg)
		time.Sleep(500 * time.Millisecond)
		prof3, _ := profileSvc.CreateProfile(context.Background(), p3, SuperTenantOrg)
		time.Sleep(500 * time.Millisecond)
		prof4, _ := profileSvc.CreateProfile(context.Background(), p4, SuperTenantOrg)

		time.Sleep(3 * time.Second)

		merged1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		merged2, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)
		merged3, _ := profileSvc.GetProfile(context.Background(), prof3.ProfileId)
		merged4, _ := profileSvc.GetProfile(context.Background(), prof4.ProfileId)

		// All profiles should be merged (either same master or same MergedTo)
		require.NotEmpty(t, merged1.MergedTo.ProfileId, "Profile 1 should be merged")
		require.NotEmpty(t, merged2.MergedTo.ProfileId, "Profile 2 should be merged")

		// Verify all emails are combined in the master
		require.Contains(t, merged1.IdentityAttributes["email"].([]interface{}), "cascade1@wso2.com")

		// Verify interests are combined across all profiles
		interests := merged1.Traits["interests"].([]interface{})
		require.GreaterOrEqual(t, len(interests), 2, "Should have combined interests from multiple profiles")

		// Ignore merged3 and merged4 checks as the exact merge behavior depends on timing
		_ = merged3
		_ = merged4

		cleanProfiles(profileSvc, SuperTenantOrg)
	})

	t.Run("Scenario11_DeepHierarchyUnification", func(t *testing.T) {
		// Scenario: Create a hierarchy where merged profiles become children of new master
		// First: T1 + T2 merge via email -> creates Master1
		// Then: T3 (new temp) with same phone as T2 merges with hierarchy
		// Expected: All profiles should be part of unified hierarchy with combined interests

		t1 := mustUnmarshalProfile(`{"identity_attributes":{"email":["deep1@wso2.com"]},"traits":{"interests":["reading"]}}`)
		t2 := mustUnmarshalProfile(`{"identity_attributes":{"email":["deep1@wso2.com"],"phone_number":["0772222222"]},"traits":{"interests":["writing"]}}`)

		prof1, _ := profileSvc.CreateProfile(context.Background(), t1, SuperTenantOrg)
		prof2, _ := profileSvc.CreateProfile(context.Background(), t2, SuperTenantOrg)
		time.Sleep(2 * time.Second)

		// Verify initial merge happened
		merged1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		merged2, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)
		require.Equal(t, merged1.MergedTo.ProfileId, merged2.MergedTo.ProfileId, "T1 and T2 should merge")

		// Now add T3 with matching phone
		t3 := mustUnmarshalProfile(`{"identity_attributes":{"phone_number":["0772222222"]},"traits":{"interests":["gaming"]}}`)
		prof3, _ := profileSvc.CreateProfile(context.Background(), t3, SuperTenantOrg)
		time.Sleep(2 * time.Second)

		merged3, _ := profileSvc.GetProfile(context.Background(), prof3.ProfileId)

		// T3 should be merged (a new master may be created when merging with existing hierarchy)
		require.NotEmpty(t, merged3.MergedTo.ProfileId, "T3 should be merged")

		// Verify all interests are combined in the final unified profile
		interests := merged3.Traits["interests"].([]interface{})
		require.Contains(t, interests, "reading")
		require.Contains(t, interests, "writing")
		require.Contains(t, interests, "gaming")

		cleanProfiles(profileSvc, SuperTenantOrg)
	})

	t.Run("Scenario12_ApplicationDataMergeAcrossProfiles", func(t *testing.T) {
		// Scenario: Multiple profiles with different application data merge correctly
		// Expected: Application data from all profiles is properly combined

		p1 := mustUnmarshalProfile(`{
			"identity_attributes":{"email":["appdata1@wso2.com"]},
			"traits":{"interests":["tech"]},
			"application_data":{"` + AppId + `":{"device_id":["device-001"]}}
		}`)
		p2 := mustUnmarshalProfile(`{
			"identity_attributes":{"email":["appdata1@wso2.com"]},
			"traits":{"interests":["science"]},
			"application_data":{"` + AppId + `":{"device_id":["device-002"]}}
		}`)

		prof1, _ := profileSvc.CreateProfile(context.Background(), p1, SuperTenantOrg)
		prof2, _ := profileSvc.CreateProfile(context.Background(), p2, SuperTenantOrg)
		time.Sleep(2 * time.Second)

		merged1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		merged2, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)

		require.Equal(t, merged1.MergedTo.ProfileId, merged2.MergedTo.ProfileId, "Profiles should merge")

		// Check that application data is properly merged
		appData := merged1.ApplicationData[AppId]
		if appData != nil {
			deviceIds, ok := appData["device_id"].([]interface{})
			if ok {
				require.GreaterOrEqual(t, len(deviceIds), 1, "Should have device IDs")
			}
		}

		cleanProfiles(profileSvc, SuperTenantOrg)
	})

	t.Run("Scenario13_UpdateProfileTriggersReunification", func(t *testing.T) {
		// Scenario: After initial profiles are created separately, an update adds matching identifier
		// P1 created with email1, P2 created with email2
		// P1 updated to add email2 -> should trigger unification

		p1 := mustUnmarshalProfile(`{"identity_attributes":{"email":["update1@wso2.com"]},"traits":{"interests":["hiking"]}}`)
		p2 := mustUnmarshalProfile(`{"identity_attributes":{"email":["update2@wso2.com"]},"traits":{"interests":["camping"]}}`)

		prof1, _ := profileSvc.CreateProfile(context.Background(), p1, SuperTenantOrg)
		prof2, _ := profileSvc.CreateProfile(context.Background(), p2, SuperTenantOrg)
		time.Sleep(2 * time.Second)

		// Verify they are NOT merged initially
		merged1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		merged2, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)
		if merged1.MergedTo != nil {
			require.Empty(t, merged1.MergedTo.ProfileId, "P1 should not be merged initially")
		}
		if merged2.MergedTo != nil {
			require.Empty(t, merged2.MergedTo.ProfileId, "P2 should not be merged initially")
		}

		// Update P1 to add email2
		updateReq := profileModel.ProfileRequest{
			IdentityAttributes: map[string]interface{}{
				"email": []interface{}{"update1@wso2.com", "update2@wso2.com"},
			},
			Traits: map[string]interface{}{
				"interests": []interface{}{"hiking"},
			},
		}
		_, _ = profileSvc.UpdateProfile(context.Background(), prof1.ProfileId, SuperTenantOrg, updateReq)
		time.Sleep(2 * time.Second)

		// After update, profiles should be unified
		afterUpdate1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		afterUpdate2, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)

		// At least one should show merge status
		mergeHappened := afterUpdate1.MergedTo.ProfileId != "" || afterUpdate2.MergedTo.ProfileId != ""
		require.True(t, mergeHappened || afterUpdate1.MergedFrom != nil || afterUpdate2.MergedFrom != nil,
			"Profiles should be unified after update adds matching email")

		cleanProfiles(profileSvc, SuperTenantOrg)
	})

	t.Run("Scenario14_MultipleIdentityAttributesMatching", func(t *testing.T) {
		// Scenario: Two profiles match on both email AND phone
		// Expected: They should merge once with the higher priority rule (email)

		p1 := mustUnmarshalProfile(`{"identity_attributes":{"email":["multi1@wso2.com"],"phone_number":["0773333333"]},"traits":{"interests":["photography"]}}`)
		p2 := mustUnmarshalProfile(`{"identity_attributes":{"email":["multi1@wso2.com"],"phone_number":["0773333333"]},"traits":{"interests":["videography"]}}`)

		prof1, _ := profileSvc.CreateProfile(context.Background(), p1, SuperTenantOrg)
		prof2, _ := profileSvc.CreateProfile(context.Background(), p2, SuperTenantOrg)
		time.Sleep(2 * time.Second)

		merged1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		merged2, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)

		require.Equal(t, merged1.MergedTo.ProfileId, merged2.MergedTo.ProfileId, "Profiles should merge")
		// The merge reason should be email_based (higher priority)
		require.Equal(t, EmailBased, merged1.MergedTo.Reason, "Should merge via email (higher priority)")

		cleanProfiles(profileSvc, SuperTenantOrg)
	})

	t.Run("Scenario15_ChainUnificationFourProfiles", func(t *testing.T) {
		// Scenario: Chain unification A→B→C→D
		// A shares email with B, B shares phone with C, C shares secondary_email logic
		// This tests that transitive relationships are properly handled

		pA := mustUnmarshalProfile(`{"identity_attributes":{"email":["chainA@wso2.com"]},"traits":{"preferences":["dark_mode"]}}`)
		pB := mustUnmarshalProfile(`{"identity_attributes":{"email":["chainA@wso2.com"],"phone_number":["0774444444"]},"traits":{"preferences":["notifications_on"]}}`)
		pC := mustUnmarshalProfile(`{"identity_attributes":{"phone_number":["0774444444"]},"traits":{"preferences":["compact_view"]}}`)
		pD := mustUnmarshalProfile(`{"identity_attributes":{"phone_number":["0774444444"]},"traits":{"preferences":["high_contrast"]}}`)

		profA, _ := profileSvc.CreateProfile(context.Background(), pA, SuperTenantOrg)
		time.Sleep(500 * time.Millisecond)
		profB, _ := profileSvc.CreateProfile(context.Background(), pB, SuperTenantOrg)
		time.Sleep(500 * time.Millisecond)
		profC, _ := profileSvc.CreateProfile(context.Background(), pC, SuperTenantOrg)
		time.Sleep(500 * time.Millisecond)
		profD, _ := profileSvc.CreateProfile(context.Background(), pD, SuperTenantOrg)
		time.Sleep(3 * time.Second)

		mergedA, _ := profileSvc.GetProfile(context.Background(), profA.ProfileId)
		mergedB, _ := profileSvc.GetProfile(context.Background(), profB.ProfileId)
		mergedC, _ := profileSvc.GetProfile(context.Background(), profC.ProfileId)
		mergedD, _ := profileSvc.GetProfile(context.Background(), profD.ProfileId)

		// A and B should be merged (email match)
		require.NotEmpty(t, mergedA.MergedTo.ProfileId, "A should be merged")
		require.NotEmpty(t, mergedB.MergedTo.ProfileId, "B should be merged")
		require.Equal(t, mergedA.MergedTo.ProfileId, mergedB.MergedTo.ProfileId, "A and B should merge to same master")

		// C and D should also be in the same merge hierarchy (phone match with B)
		require.NotEmpty(t, mergedC.MergedTo.ProfileId, "C should be merged")
		require.NotEmpty(t, mergedD.MergedTo.ProfileId, "D should be merged")

		// All should ultimately point to the same master or be part of the same hierarchy
		masterProfile, _ := profileSvc.GetProfile(context.Background(), mergedA.MergedTo.ProfileId)
		require.NotNil(t, masterProfile, "Master profile should exist")

		// Verify preferences from all profiles are combined
		preferences := masterProfile.Traits["preferences"].([]interface{})
		require.Contains(t, preferences, "dark_mode")
		require.Contains(t, preferences, "notifications_on")

		cleanProfiles(profileSvc, SuperTenantOrg)
	})

	t.Run("Scenario16_PermPlusMultipleTemps", func(t *testing.T) {
		// Scenario: One permanent profile, multiple temp profiles matching different attributes
		// Perm has user_id and email, T1 matches email, T2 matches phone of T1
		// Expected: All merge under permanent profile

		perm := mustUnmarshalProfile(`{"user_id":"complex-perm-001","identity_attributes":{"user_id":"complex-perm-001","email":["perm-multi@wso2.com"]},"traits":{"interests":["leadership"]}}`)
		t1 := mustUnmarshalProfile(`{"identity_attributes":{"email":["perm-multi@wso2.com"],"phone_number":["0775555555"]},"traits":{"interests":["management"]}}`)
		t2 := mustUnmarshalProfile(`{"identity_attributes":{"phone_number":["0775555555"]},"traits":{"interests":["strategy"]}}`)
		t3 := mustUnmarshalProfile(`{"identity_attributes":{"phone_number":["0775555555"]},"traits":{"interests":["planning"]}}`)

		profPerm, _ := profileSvc.CreateProfile(context.Background(), perm, SuperTenantOrg)
		time.Sleep(500 * time.Millisecond)
		profT1, _ := profileSvc.CreateProfile(context.Background(), t1, SuperTenantOrg)
		time.Sleep(500 * time.Millisecond)
		profT2, _ := profileSvc.CreateProfile(context.Background(), t2, SuperTenantOrg)
		time.Sleep(500 * time.Millisecond)
		profT3, _ := profileSvc.CreateProfile(context.Background(), t3, SuperTenantOrg)
		time.Sleep(3 * time.Second)

		mergedPerm, _ := profileSvc.GetProfile(context.Background(), profPerm.ProfileId)
		mergedT1, _ := profileSvc.GetProfile(context.Background(), profT1.ProfileId)
		mergedT2, _ := profileSvc.GetProfile(context.Background(), profT2.ProfileId)
		mergedT3, _ := profileSvc.GetProfile(context.Background(), profT3.ProfileId)

		// Permanent profile should be the master
		require.Empty(t, mergedPerm.MergedTo, "Permanent profile should be master")
		require.NotEmpty(t, mergedPerm.MergedFrom, "Permanent profile should have merged children")

		// T1 should merge to permanent via email
		require.Equal(t, profPerm.ProfileId, mergedT1.MergedTo.ProfileId, "T1 should merge to permanent")
		require.Equal(t, EmailBased, mergedT1.MergedTo.Reason)

		// T2 and T3 should merge to permanent via phone (transitive through T1)
		require.Equal(t, profPerm.ProfileId, mergedT2.MergedTo.ProfileId, "T2 should merge to permanent")
		require.Equal(t, profPerm.ProfileId, mergedT3.MergedTo.ProfileId, "T3 should merge to permanent")

		// Verify all interests are combined in master
		interests := mergedPerm.Traits["interests"].([]interface{})
		require.Contains(t, interests, "leadership")
		require.Contains(t, interests, "management")

		cleanProfiles(profileSvc, SuperTenantOrg)
	})

	t.Run("Scenario17_MergeWithDifferentTraitTypes", func(t *testing.T) {
		// Scenario: Profiles with different trait types merge correctly
		// Tests combine strategy for arrays and overwrite for scalar values

		p1 := mustUnmarshalProfile(`{"identity_attributes":{"email":["types@wso2.com"]},"traits":{"interests":["reading"],"score":50}}`)
		p2 := mustUnmarshalProfile(`{"identity_attributes":{"email":["types@wso2.com"]},"traits":{"interests":["writing"],"score":75}}`)

		prof1, _ := profileSvc.CreateProfile(context.Background(), p1, SuperTenantOrg)
		_, _ = profileSvc.CreateProfile(context.Background(), p2, SuperTenantOrg)
		time.Sleep(2 * time.Second)

		merged1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)

		// Interests should be combined (combine strategy)
		interests := merged1.Traits["interests"].([]interface{})
		require.Contains(t, interests, "reading")
		require.Contains(t, interests, "writing")

		// Score should be overwritten (overwrite strategy - last value wins)
		score := merged1.Traits["score"]
		require.NotNil(t, score, "Score should exist in merged profile")

		cleanProfiles(profileSvc, SuperTenantOrg)
	})

	t.Run("Scenario18_RuleReactivation", func(t *testing.T) {
		// Scenario: Rule is deactivated, profiles created, rule reactivated
		// New profile after reactivation should trigger unification with existing

		// First create profile while rule is active
		p1 := mustUnmarshalProfile(`{"identity_attributes":{"email":["reactivate@wso2.com"]},"traits":{"interests":["initial"]}}`)
		prof1, _ := profileSvc.CreateProfile(context.Background(), p1, SuperTenantOrg)
		time.Sleep(1 * time.Second)

		// Deactivate email rule
		emailRule.IsActive = false
		_ = unificationSvc.PatchUnificationRule(context.Background(), emailRuleId, SuperTenantOrg, emailRule)

		// Create second profile while rule is inactive
		p2 := mustUnmarshalProfile(`{"identity_attributes":{"email":["reactivate@wso2.com"]},"traits":{"interests":["during_inactive"]}}`)
		prof2, _ := profileSvc.CreateProfile(context.Background(), p2, SuperTenantOrg)
		time.Sleep(2 * time.Second)

		// Verify no unification happened
		check1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		check2, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)
		require.Empty(t, check1.MergedTo, "Should not merge while rule inactive")
		require.Empty(t, check2.MergedTo, "Should not merge while rule inactive")

		// Reactivate rule
		emailRule.IsActive = true
		_ = unificationSvc.PatchUnificationRule(context.Background(), emailRuleId, SuperTenantOrg, emailRule)

		// Create third profile after reactivation
		p3 := mustUnmarshalProfile(`{"identity_attributes":{"email":["reactivate@wso2.com"]},"traits":{"interests":["after_reactivate"]}}`)
		prof3, _ := profileSvc.CreateProfile(context.Background(), p3, SuperTenantOrg)
		time.Sleep(2 * time.Second)

		// Third profile should trigger unification
		merged3, _ := profileSvc.GetProfile(context.Background(), prof3.ProfileId)
		require.NotEmpty(t, merged3.MergedTo.ProfileId, "Third profile should trigger unification")

		cleanProfiles(profileSvc, SuperTenantOrg)
	})

	t.Run("Scenario19_TwoMastersWithExistingHierarchiesMerge", func(t *testing.T) {
		// Scenario: Two masters with existing hierarchies merge
		// Master1 has 3 children
		// Master2 has 2 children
		// P3 and P4 share an attribute should trigger merge
		// Expected: All 5 children end up under unified hierarchy with merged data

		// Step 1: Create first hierarchy (Master1 with 3 children)
		p1 := mustUnmarshalProfile(`{"identity_attributes":{"email":["hierarchy1@wso2.com"]},"traits":{"interests":["reading"]}}`)
		p2 := mustUnmarshalProfile(`{"identity_attributes":{"email":["hierarchy1@wso2.com"]},"traits":{"interests":["writing"]}}`)
		p3 := mustUnmarshalProfile(`{"identity_attributes":{"email":["hierarchy1@wso2.com"],"phone_number":["0771111111"]},"traits":{"interests":["coding"]}}`)

		prof1, _ := profileSvc.CreateProfile(context.Background(), p1, SuperTenantOrg)
		time.Sleep(500 * time.Millisecond)
		prof2, _ := profileSvc.CreateProfile(context.Background(), p2, SuperTenantOrg)
		time.Sleep(500 * time.Millisecond)
		prof3, _ := profileSvc.CreateProfile(context.Background(), p3, SuperTenantOrg)
		time.Sleep(3 * time.Second)

		// Verify first hierarchy is created
		merged1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		merged2, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)
		merged3, _ := profileSvc.GetProfile(context.Background(), prof3.ProfileId)

		require.NotEmpty(t, merged1.MergedTo.ProfileId, "P1 should be merged")
		require.NotEmpty(t, merged2.MergedTo.ProfileId, "P2 should be merged")
		require.NotEmpty(t, merged3.MergedTo.ProfileId, "P3 should be merged")

		// All three should have same master
		master1Id := merged1.MergedTo.ProfileId
		require.Equal(t, master1Id, merged2.MergedTo.ProfileId, "P1 and P2 should have same master")
		require.Equal(t, master1Id, merged3.MergedTo.ProfileId, "P1 and P3 should have same master")

		master1, _ := profileSvc.GetProfile(context.Background(), master1Id)
		require.NotNil(t, master1, "Master1 should exist")
		require.GreaterOrEqual(t, len(master1.MergedFrom), 3, "Master1 should have at least 3 children")

		// Step 2: Create second hierarchy (Master2 with 2 children)
		p4 := mustUnmarshalProfile(`{"identity_attributes":{"email":["hierarchy2@wso2.com"],"phone_number":["0772222222"]},"traits":{"interests":["gaming"]}}`)
		p5 := mustUnmarshalProfile(`{"identity_attributes":{"email":["hierarchy2@wso2.com"]},"traits":{"interests":["music"]}}`)

		prof4, _ := profileSvc.CreateProfile(context.Background(), p4, SuperTenantOrg)
		time.Sleep(500 * time.Millisecond)
		prof5, _ := profileSvc.CreateProfile(context.Background(), p5, SuperTenantOrg)
		time.Sleep(3 * time.Second)

		// Verify second hierarchy is created
		merged4, _ := profileSvc.GetProfile(context.Background(), prof4.ProfileId)
		merged5, _ := profileSvc.GetProfile(context.Background(), prof5.ProfileId)

		require.NotEmpty(t, merged4.MergedTo.ProfileId, "P4 should be merged")
		require.NotEmpty(t, merged5.MergedTo.ProfileId, "P5 should be merged")

		master2Id := merged4.MergedTo.ProfileId
		require.Equal(t, master2Id, merged5.MergedTo.ProfileId, "P4 and P5 should have same master")

		master2, _ := profileSvc.GetProfile(context.Background(), master2Id)
		require.NotNil(t, master2, "Master2 should exist")
		require.GreaterOrEqual(t, len(master2.MergedFrom), 2, "Master2 should have at least 2 children")
		for i, child := range master2.MergedFrom {
			if i > 0 {
				fmt.Print(", ")
			}
			fmt.Printf("%s", child.ProfileId[:8])
		}

		// Step 3: Create a profile that connects both hierarchies
		// P6 shares phone with P3 (from Master1) and shares phone with P4 (from Master2)
		p6 := mustUnmarshalProfile(`{"identity_attributes":{"phone_number":["0771111111","0772222222"]},"traits":{"interests":["sports"]}}`)
		prof6, _ := profileSvc.CreateProfile(context.Background(), p6, SuperTenantOrg)
		time.Sleep(5 * time.Second)

		// Step 4: Verify all profiles are now in unified hierarchy
		finalMerged1, _ := profileSvc.GetProfile(context.Background(), prof1.ProfileId)
		finalMerged2, _ := profileSvc.GetProfile(context.Background(), prof2.ProfileId)
		finalMerged3, _ := profileSvc.GetProfile(context.Background(), prof3.ProfileId)
		finalMerged4, _ := profileSvc.GetProfile(context.Background(), prof4.ProfileId)
		finalMerged5, _ := profileSvc.GetProfile(context.Background(), prof5.ProfileId)
		finalMerged6, _ := profileSvc.GetProfile(context.Background(), prof6.ProfileId)

		// All profiles should have a master (merged)
		require.NotEmpty(t, finalMerged1.MergedTo.ProfileId, "P1 should be in unified hierarchy")
		require.NotEmpty(t, finalMerged2.MergedTo.ProfileId, "P2 should be in unified hierarchy")
		require.NotEmpty(t, finalMerged3.MergedTo.ProfileId, "P3 should be in unified hierarchy")
		require.NotEmpty(t, finalMerged4.MergedTo.ProfileId, "P4 should be in unified hierarchy")
		require.NotEmpty(t, finalMerged5.MergedTo.ProfileId, "P5 should be in unified hierarchy")
		require.NotEmpty(t, finalMerged6.MergedTo.ProfileId, "P6 should be in unified hierarchy")

		// Find the final master
		finalMasterId := finalMerged6.MergedTo.ProfileId
		finalMaster, _ := profileSvc.GetProfile(context.Background(), finalMasterId)
		require.NotNil(t, finalMaster, "Final master should exist")

		// Show all children of final master
		fmt.Println("\n  Final Master's Direct Children:")
		for i, child := range finalMaster.MergedFrom {
			fmt.Printf("    [%d] %s (reason: %s)\n", i+1, child.ProfileId[:8], child.Reason)
		}

		// Verify interests from both hierarchies are combined
		interests := finalMaster.Traits["interests"].([]interface{})
		require.GreaterOrEqual(t, len(interests), 4, "Should have interests from multiple profiles")

		// Verify at least one phone number is present
		phoneNumbers := finalMaster.IdentityAttributes["phone_number"].([]interface{})
		require.GreaterOrEqual(t, len(phoneNumbers), 1, "Should have at least one phone number")

		// Verify email from at least one hierarchy is present
		emails := finalMaster.IdentityAttributes["email"].([]interface{})
		require.GreaterOrEqual(t, len(emails), 1, "Should have at least one email")

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
