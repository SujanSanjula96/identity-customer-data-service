/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	profileSchema "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

// largeText returns non-ASCII text of at least size bytes. It mixes scripts of
// one, two, three and four UTF-8 bytes, so a read cut off at a byte or a
// character limit shows up as a mismatch.
func largeText(size int) string {
	const sample = "ශ්\u200dරී ලංකා 東京 Αθήνα שלום café 😀 "
	return strings.Repeat(sample, size/len(sample)+1)
}

// Test_LargeDocuments stores traits, identity attributes, application data and
// canonical values larger than 32 KB and checks every read returns them whole.
// A driver that reads a LOB only up to 32 KB returns them cut off, with no error.
func Test_LargeDocuments(t *testing.T) {
	org := setup.TestOrg(t, fmt.Sprintf("large-documents-%d", time.Now().UnixNano()))
	ctx := context.Background()
	profileSvc := profileService.GetProfilesService()
	profileSchemaSvc := schemaService.GetProfileSchemaService()
	restore := schemaService.OverrideValidateApplicationIdentifierForTest(
		func(context.Context, string, string) (error, bool) { return nil, true })
	defer restore()

	attribute := func(name, appID string) profileSchema.ProfileSchemaAttribute {
		return profileSchema.ProfileSchemaAttribute{
			OrgID:                 org.ID,
			AttributeID:           uuid.New().String(),
			AttributeName:         name,
			ValueType:             constants.StringDataType,
			MergeStrategy:         "overwrite",
			Mutability:            constants.MutabilityReadWrite,
			ApplicationIdentifier: appID,
		}
	}

	canonical := make([]profileSchema.CanonicalValue, 0, 1500)
	for i := 0; i < 1500; i++ {
		canonical = append(canonical, profileSchema.CanonicalValue{
			Value: fmt.Sprintf("value-%04d", i), Label: fmt.Sprintf("Label %04d කොළඹ", i)})
	}
	tier := attribute("traits.tier", "")
	tier.CanonicalValues = canonical

	for scope, attrs := range map[string][]profileSchema.ProfileSchemaAttribute{
		constants.IdentityAttributes: {attribute("identity_attributes.biography", "")},
		constants.Traits:             {attribute("traits.notes", ""), attribute("traits.city", ""), tier},
		constants.ApplicationData:    {attribute("application_data.history", "app1")},
	} {
		_, err := profileSchemaSvc.AddProfileSchemaAttributesForScope(ctx, attrs, scope, org.ID)
		require.NoError(t, err, "failed to add the %s attributes", scope)
	}

	t.Run("CanonicalValues_ReadWhole", func(t *testing.T) {
		stored, err := profileSchemaSvc.GetProfileSchemaAttributeByID(ctx, org.ID, tier.AttributeID)
		require.NoError(t, err)
		require.Equal(t, canonical, stored.CanonicalValues)
	})

	notes, biography, history := largeText(100_000), largeText(60_000), largeText(40_000)
	request := profileModel.ProfileRequest{
		IdentityAttributes: map[string]any{"biography": biography},
		Traits:             map[string]any{"notes": notes, "city": "කොළඹ"},
		ApplicationData:    map[string]any{"app1": map[string]any{"history": history}},
	}

	created, err := profileSvc.CreateProfile(ctx, request, org.ID)
	require.NoError(t, err)
	require.NotNil(t, created)

	t.Run("GetProfile_ReturnsWholeDocuments", func(t *testing.T) {
		profile, err := profileSvc.GetProfile(ctx, created.ProfileID, org.ID)
		require.NoError(t, err)
		require.Equal(t, notes, profile.Traits["notes"])
		require.Equal(t, biography, profile.IdentityAttributes["biography"])
		require.Equal(t, history, profile.ApplicationData["app1"]["history"])
	})

	t.Run("ListAndFilter_ReturnWholeDocuments", func(t *testing.T) {
		profiles, _, err := profileSvc.GetAllProfilesCursor(ctx, org.ID, 10, nil)
		require.NoError(t, err)
		require.Len(t, profiles, 1)
		require.Equal(t, notes, profiles[0].Traits["notes"])

		for _, filter := range []string{"traits.city eq කොළඹ", "traits.city co ළඹ", "traits.city sw කො"} {
			profiles, _, err = profileSvc.GetAllProfilesWithFilterCursor(ctx, org.ID, []string{filter}, 10, nil)
			require.NoError(t, err, filter)
			require.Len(t, profiles, 1, filter)
			require.Equal(t, created.ProfileID, profiles[0].ProfileID, filter)
		}

		profiles, _, err = profileSvc.GetAllProfilesWithFilterCursor(ctx, org.ID,
			[]string{"traits.city eq ගාල්ල"}, 10, nil)
		require.NoError(t, err)
		require.Empty(t, profiles)
	})

	t.Run("UpdateProfile_StoresALargerDocument", func(t *testing.T) {
		longer := largeText(250_000)
		update := profileModel.ProfileRequest{
			IdentityAttributes: map[string]any{"biography": biography},
			Traits:             map[string]any{"notes": longer, "city": "කොළඹ"},
		}
		_, err := profileSvc.UpdateProfile(ctx, created.ProfileID, org.ID, update)
		require.NoError(t, err)

		profile, err := profileSvc.GetProfile(ctx, created.ProfileID, org.ID)
		require.NoError(t, err)
		require.Equal(t, longer, profile.Traits["notes"])
	})

	t.Cleanup(func() {
		_ = profileSvc.DeleteProfile(ctx, created.ProfileID, org.ID)
		_ = profileSchemaSvc.DeleteProfileSchema(ctx, org.ID)
	})
}
