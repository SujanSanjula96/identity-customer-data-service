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

package activemqintegration

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

// Test_ActiveMQ_ProfileUnification_EmailBased verifies that the full profile
// unification pipeline works correctly when the worker queue is backed by
// ActiveMQ.  Two anonymous (temporary) profiles that share the same e-mail
// address are created; after the ActiveMQ consumer processes the queued
// messages the profiles should be merged into a single master profile.
func Test_ActiveMQ_ProfileUnification_EmailBased(t *testing.T) {
	// Bypass the IDP app-identifier check so tests don't need a live identity
	// server.
	restoreValidation := schemaService.OverrideValidateApplicationIdentifierForTest(
		func(context.Context, string, string) (error, bool) { return nil, true },
	)
	defer restoreValidation()

	orgHandle := fmt.Sprintf("activemq-test-%d", time.Now().UnixNano())

	// ── Schema setup ──────────────────────────────────────────────────────────
	schemaSvc := schemaService.GetProfileSchemaService()
	identityAttrs := []schemaModel.ProfileSchemaAttribute{
		{
			OrgId:         orgHandle,
			AttributeId:   uuid.New().String(),
			AttributeName: "identity_attributes.email",
			ValueType:     constants.StringDataType,
			MergeStrategy: "combine",
			Mutability:    constants.MutabilityReadWrite,
			MultiValued:   true,
		},
	}
	traitAttrs := []schemaModel.ProfileSchemaAttribute{
		{
			OrgId:         orgHandle,
			AttributeId:   uuid.New().String(),
			AttributeName: "traits.interests",
			ValueType:     constants.StringDataType,
			MergeStrategy: "combine",
			Mutability:    constants.MutabilityReadWrite,
			MultiValued:   true,
		},
	}
	_, err := schemaSvc.AddProfileSchemaAttributesForScope(context.Background(), identityAttrs, constants.IdentityAttributes, orgHandle)
	require.NoError(t, err)
	_, err = schemaSvc.AddProfileSchemaAttributesForScope(context.Background(), traitAttrs, constants.Traits, orgHandle)
	require.NoError(t, err)

	// ── Unification rule: match on email ─────────────────────────────────────
	unificationSvc := unificationService.GetUnificationRuleService()
	emailRule := model.UnificationRule{
		RuleId:       uuid.New().String(),
		RuleName:     "email_match",
		OrgHandle:    orgHandle,
		PropertyName: "identity_attributes.email",
		Priority:     1,
		IsActive:     true,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	require.NoError(t, unificationSvc.AddUnificationRule(context.Background(), emailRule, orgHandle))

	// ── Create two profiles that share an email ───────────────────────────────
	profileSvc := profileService.GetProfilesService()

	p1Req := mustUnmarshalProfileReq(`{"identity_attributes":{"email":["shared@activemq-test.com"]},"traits":{"interests":["music"]}}`)
	p2Req := mustUnmarshalProfileReq(`{"identity_attributes":{"email":["shared@activemq-test.com"]},"traits":{"interests":["sports"]}}`)

	p1, err := profileSvc.CreateProfile(context.Background(), p1Req, orgHandle)
	require.NoError(t, err)
	p2, err := profileSvc.CreateProfile(context.Background(), p2Req, orgHandle)
	require.NoError(t, err)

	// Allow enough time for the ActiveMQ messages to be consumed and processed.
	time.Sleep(5 * time.Second)

	// ── Assertions ───────────────────────────────────────────────────────────
	merged1, err := profileSvc.GetProfile(context.Background(), p1.ProfileId)
	require.NoError(t, err)
	merged2, err := profileSvc.GetProfile(context.Background(), p2.ProfileId)
	require.NoError(t, err)

	// Both profiles should point to the same master profile.
	require.NotEmpty(t, merged1.MergedTo.ProfileId, "profile 1 should be merged via ActiveMQ queue")
	require.NotEmpty(t, merged2.MergedTo.ProfileId, "profile 2 should be merged via ActiveMQ queue")
	require.Equal(t, merged1.MergedTo.ProfileId, merged2.MergedTo.ProfileId,
		"both profiles should be unified into the same master profile")

	// The master profile should carry the combined interests.
	master, err := profileSvc.GetProfile(context.Background(), merged1.MergedTo.ProfileId)
	require.NoError(t, err)
	interests, ok := master.Traits["interests"].([]interface{})
	require.True(t, ok, "interests should be a slice")
	require.ElementsMatch(t, []interface{}{"music", "sports"}, interests,
		"master profile should contain combined interests from both profiles")

	// ── Cleanup ───────────────────────────────────────────────────────────────
	t.Cleanup(func() {
		_ = unificationSvc.DeleteUnificationRule(context.Background(), emailRule.RuleId)
		profiles, _, _ := profileSvc.GetAllProfilesCursor(context.Background(), orgHandle, 20, nil)
		for _, p := range profiles {
			_ = profileSvc.DeleteProfile(context.Background(), p.ProfileId)
		}
		_ = schemaSvc.DeleteProfileSchemaAttributesByScope(context.Background(), orgHandle, constants.IdentityAttributes)
		_ = schemaSvc.DeleteProfileSchemaAttributesByScope(context.Background(), orgHandle, constants.Traits)
	})
}

func mustUnmarshalProfileReq(jsonStr string) profileModel.ProfileRequest {
	var p profileModel.ProfileRequest
	if err := json.Unmarshal([]byte(jsonStr), &p); err != nil {
		panic(err)
	}
	return p
}
