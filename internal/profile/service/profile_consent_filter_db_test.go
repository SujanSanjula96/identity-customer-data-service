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
package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	consentStore "github.com/wso2/identity-customer-data-service/internal/consent/store"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// addCategoryWith stores a consent category over attributes of the schema.
func (e *profileEnv) addCategoryWith(t *testing.T, name string, attributes ...consentModel.ConsentAttribute) string {
	t.Helper()
	id := uuid.New().String()
	require.NoError(t, consentStore.AddConsentCategory(e.ctx, e.orgID, consentModel.ConsentCategory{
		CategoryName:       name,
		CategoryIdentifier: id,
		OrgId:              e.orgID,
		Purpose:            "profiling",
		Attributes:         attributes,
	}))
	return id
}

func traitAttribute(name string) consentModel.ConsentAttribute {
	return consentModel.ConsentAttribute{
		Scope:         constants.Traits,
		AttributeName: constants.Traits + "." + name,
		AttributeId:   "attr-" + constants.Traits + "." + name,
	}
}

func (e *profileEnv) consentTo(t *testing.T, profileID string, categories ...string) {
	t.Helper()
	records := make([]profileModel.ConsentRecord, 0, len(categories))
	for _, category := range categories {
		records = append(records, profileModel.ConsentRecord{CategoryIdentifier: category, IsConsented: true})
	}
	require.NoError(t, e.svc.UpdateProfileConsents(e.ctx, profileID, e.orgID, records))
}

func fullProfile(profileID string) profileModel.ProfileResponse {
	return profileModel.ProfileResponse{
		ProfileId:          profileID,
		UserId:             "u-1",
		IdentityAttributes: map[string]interface{}{"email": "a@b.c"},
		Traits:             map[string]interface{}{"city": "Colombo", "age": 30, "tier": "gold"},
		ApplicationData: map[string]map[string]interface{}{
			"client-x": {"app-1": map[string]interface{}{"theme": "dark"}, "other": "hidden"},
		},
	}
}

func TestConsentFilterKeepsOnlyWhatTheProfileConsentedTo(t *testing.T) {
	env := newProfileEnv(t)
	profile := env.createWithCity(t, "Colombo")
	analytics := env.addCategoryWith(t, "Analytics", traitAttribute("city"))
	offers := env.addCategoryWith(t, "Offers", traitAttribute("tier"))
	env.consentTo(t, profile.ProfileId, analytics)

	got, err := FilterProfileByConsent(env.ctx, fullProfile(profile.ProfileId), profile.ProfileId, env.orgID,
		[]string{analytics, offers})

	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{"city": "Colombo"}, got.Traits,
		"the category it did not consent to adds nothing")
	assert.Nil(t, got.IdentityAttributes)
	assert.Nil(t, got.ApplicationData)
	assert.Equal(t, profile.ProfileId, got.ProfileId, "the identifiers are always kept")
	assert.Equal(t, "u-1", got.UserId)
}

func TestConsentFilterUnitesTheConsentedCategories(t *testing.T) {
	env := newProfileEnv(t)
	profile := env.createWithCity(t, "Colombo")
	analytics := env.addCategoryWith(t, "Analytics", traitAttribute("city"))
	offers := env.addCategoryWith(t, "Offers", traitAttribute("tier"), traitAttribute("age"))
	env.consentTo(t, profile.ProfileId, analytics, offers)

	got, err := FilterProfileByConsent(env.ctx, fullProfile(profile.ProfileId), profile.ProfileId, env.orgID,
		[]string{analytics, offers, analytics})

	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{"city": "Colombo", "age": 30, "tier": "gold"}, got.Traits)
}

func TestConsentFilterReturnsNothingWithoutConsentOrCategories(t *testing.T) {
	env := newProfileEnv(t)
	profile := env.createWithCity(t, "Colombo")
	analytics := env.addCategoryWith(t, "Analytics", traitAttribute("city"))

	notConsented, err := FilterProfileByConsent(env.ctx, fullProfile(profile.ProfileId), profile.ProfileId, env.orgID,
		[]string{analytics})
	require.NoError(t, err)
	assert.Nil(t, notConsented.Traits)
	assert.Nil(t, notConsented.IdentityAttributes)

	none, err := FilterProfileByConsent(env.ctx, fullProfile(profile.ProfileId), profile.ProfileId, env.orgID, nil)
	require.NoError(t, err)
	assert.Nil(t, none.Traits)

	env.consentTo(t, profile.ProfileId, analytics)
	unknown, err := FilterProfileByConsent(env.ctx, fullProfile(profile.ProfileId), profile.ProfileId, env.orgID,
		[]string{"not-a-category"})
	require.NoError(t, err)
	assert.Nil(t, unknown.Traits, "a category the organization does not have is ignored")
}

func TestConsentFilterAlwaysKeepsTheMandatoryIdentityData(t *testing.T) {
	env := newProfileEnv(t)
	profile := env.createWithCity(t, "Colombo")
	require.NoError(t, consentStore.SeedDefaultIdentityDataCategory(env.ctx, env.orgID))

	got, err := FilterProfileByConsent(env.ctx, fullProfile(profile.ProfileId), profile.ProfileId, env.orgID, nil)

	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{"email": "a@b.c"}, got.IdentityAttributes,
		"identity attributes need no consent")
	assert.Nil(t, got.Traits)
}

func TestConsentFilterKeepsTheApplicationDataOfAConsentedGroup(t *testing.T) {
	env := newProfileEnv(t)
	env.seedSchemaForApp(t, "app-1")
	profile := env.createWithCity(t, "Colombo")
	category := env.addCategoryWith(t, "Apps", consentModel.ConsentAttribute{
		Scope:                 constants.ApplicationData,
		AttributeName:         "application_data.theme",
		AttributeId:           "attr-app-1-theme",
		ApplicationIdentifier: "app-1",
	})
	env.consentTo(t, profile.ProfileId, category)

	got, err := FilterProfileByConsent(env.ctx, fullProfile(profile.ProfileId), profile.ProfileId, env.orgID,
		[]string{category})

	require.NoError(t, err)
	assert.Equal(t, map[string]map[string]interface{}{
		"client-x": {"app-1": map[string]interface{}{"theme": "dark"}},
	}, got.ApplicationData, "only the data group that the category names is kept")
}
