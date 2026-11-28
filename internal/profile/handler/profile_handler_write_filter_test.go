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
package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	consentStore "github.com/wso2/identity-customer-data-service/internal/consent/store"
	"github.com/wso2/identity-customer-data-service/internal/profile/model"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaStore "github.com/wso2/identity-customer-data-service/internal/profile_schema/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// The responses of the endpoints that write a profile carry the stored profile. A caller that is
// not a system application must get no more of it than GetProfile gives it: no trait the profile
// has not consented to share, and no data of another application.

const otherApp = "other-app"

// createProfileWithOtherAppData creates a profile with a trait and with data of an application
// that is not the caller's.
func (e *profileHandlerEnv) createProfileWithOtherAppData(t *testing.T) *model.ProfileResponse {
	t.Helper()
	e.addThemeSchema(t, otherApp)

	created, err := e.svc.CreateProfile(e.ctx, model.ProfileRequest{
		Traits:          map[string]interface{}{"city": "Colombo"},
		ApplicationData: map[string]interface{}{otherApp: map[string]interface{}{"theme": "dark"}},
	}, e.orgID)
	require.NoError(t, err)
	require.Equal(t, "dark", created.ApplicationData[otherApp]["theme"], "the profile holds the other app's data")
	return created
}

// addThemeSchema adds the application data attribute theme of an application to the schema.
func (e *profileHandlerEnv) addThemeSchema(t *testing.T, appID string) {
	t.Helper()
	require.NoError(t, schemaStore.AddProfileSchemaAttributesForScope(e.ctx, []schemaModel.ProfileSchemaAttribute{{
		AttributeID:           "attr-" + appID + "-theme",
		AttributeName:         constants.ApplicationData + ".theme",
		Scope:                 constants.ApplicationData,
		ApplicationIdentifier: appID,
		ValueType:             constants.StringDataType,
		MergeStrategy:         "overwrite",
		Mutability:            constants.MutabilityReadWrite,
	}}, constants.ApplicationData, e.orgID))
}

// initProfileWithCookie calls InitProfile with the query and the profile cookie.
func (e *profileHandlerEnv) initProfileWithCookie(t *testing.T, query, cookieID string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/t/"+e.handle+"/cds/api/v1/profiles"+query,
		strings.NewReader(`{}`))
	request = request.WithContext(context.WithValue(request.Context(), constants.TenantContextKey, e.handle))
	request.Header.Set("Authorization", e.bearer())
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: constants.ProfileCookie, Value: cookieID})
	recorder := httptest.NewRecorder()
	e.h.InitProfile(recorder, request)
	return recorder
}

// consentToCity adds a consent category that shares traits.city, and the consent of the profile to it.
func (e *profileHandlerEnv) consentToCity(t *testing.T, profileID string) string {
	t.Helper()
	category := "category-city"
	require.NoError(t, consentStore.AddConsentCategory(e.ctx, e.orgID, consentModel.ConsentCategory{
		CategoryName:       "Analytics",
		CategoryIdentifier: category,
		OrgID:              e.orgID,
		Purpose:            "profiling",
		Attributes: []consentModel.ConsentAttribute{{
			Scope:         constants.Traits,
			AttributeName: constants.Traits + ".city",
			AttributeID:   "attr-" + constants.Traits + ".city",
		}},
	}))
	require.NoError(t, e.svc.UpdateProfileConsents(e.ctx, profileID, e.orgID,
		[]model.ConsentRecord{{CategoryIdentifier: category, IsConsented: true}}))
	return category
}

func TestPatchProfileRespondsWithWhatTheCallerMayRead(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfileWithOtherAppData(t)

	// An empty patch changes nothing, so its answer must not read what GetProfile hides.
	patched := env.call(t, env.h.PatchProfile, http.MethodPatch, env.auth.ValidToken,
		"?includeApplicationData=true", `{}`, profileIDOf(created.ProfileID))
	require.Equal(t, http.StatusOK, patched.Code, patched.Body.String())
	got := decodeProfile(t, patched)
	assert.Equal(t, created.ProfileID, got.ProfileID)
	assert.Empty(t, got.Traits, "a trait without consent is not returned")
	assert.NotContains(t, got.ApplicationData, otherApp, "another application's data is not returned")

	read := env.call(t, env.h.GetProfile, http.MethodGet, env.auth.ValidToken,
		"?includeApplicationData=true", "", profileIDOf(created.ProfileID))
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	assert.Equal(t, decodeProfile(t, read).Traits, got.Traits, "PATCH returns what GET returns")

	stored, err := env.svc.GetProfile(env.ctx, created.ProfileID, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "Colombo", stored.Traits["city"], "the filter changes the response, not the profile")
}

func TestPatchProfileRespondsWithConsentedAttributes(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfile(t, "Colombo")
	category := env.consentToCity(t, created.ProfileID)

	patched := env.call(t, env.h.PatchProfile, http.MethodPatch, env.auth.ValidToken,
		"?consentCategoryId="+category, `{"traits":{"city":"Kandy"}}`, profileIDOf(created.ProfileID))
	require.Equal(t, http.StatusOK, patched.Code, patched.Body.String())
	assert.Equal(t, "Kandy", decodeProfile(t, patched).Traits["city"], "a consented trait is returned")
}

func TestPatchProfileRespondsWithTheWholeProfileToASystemApplication(t *testing.T) {
	env := newProfileHandlerEnv(t)
	env.markConsoleSystemApp(t)
	created := env.createProfileWithOtherAppData(t)

	patched := env.call(t, env.h.PatchProfile, http.MethodPatch, env.auth.ValidToken,
		"?includeApplicationData=true", `{}`, profileIDOf(created.ProfileID))
	require.Equal(t, http.StatusOK, patched.Code, patched.Body.String())
	got := decodeProfile(t, patched)
	assert.Equal(t, "Colombo", got.Traits["city"])
	assert.Equal(t, "dark", got.ApplicationData[otherApp]["theme"])
}

func TestInitProfileWithACookieRespondsWithWhatTheCallerMayRead(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfileWithOtherAppData(t)
	cookieID := env.createCookie(t, created.ProfileID, true)

	recorder := env.initProfileWithCookie(t, "?includeApplicationData=true", cookieID)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	got := decodeProfile(t, recorder)
	assert.Equal(t, created.ProfileID, got.ProfileID, "the profile of the cookie is returned")
	assert.Equal(t, cookieID, got.AnonymousTracker, "the cookie is still named in the response")
	assert.Empty(t, got.Traits, "a trait without consent is not returned")
	assert.NotContains(t, got.ApplicationData, otherApp, "another application's data is not returned")
}

func TestInitProfileWithACookieRespondsWithTheWholeProfileToASystemApplication(t *testing.T) {
	env := newProfileHandlerEnv(t)
	env.markConsoleSystemApp(t)
	created := env.createProfileWithOtherAppData(t)
	cookieID := env.createCookie(t, created.ProfileID, true)

	recorder := env.initProfileWithCookie(t, "?includeApplicationData=true", cookieID)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	got := decodeProfile(t, recorder)
	assert.Equal(t, "Colombo", got.Traits["city"])
	assert.Equal(t, "dark", got.ApplicationData[otherApp]["theme"])
}
