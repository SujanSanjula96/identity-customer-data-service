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
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
	consentStore "github.com/wso2/identity-customer-data-service/internal/consent/store"
	"github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// callWithCookie runs a handler on a request that carries the profile cookie.
func (e *profileHandlerEnv) callWithCookie(t *testing.T, handler http.HandlerFunc, method, authorization, body,
	cookieID string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/t/"+e.handle+"/cds/api/v1/profiles", strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), constants.TenantContextKey, e.handle))
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	request.Header.Set("Content-Type", "application/json")
	if cookieID != "" {
		request.AddCookie(&http.Cookie{Name: constants.ProfileCookie, Value: cookieID})
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func (e *profileHandlerEnv) bearer() string { return "Bearer " + e.auth.ValidToken }

func (e *profileHandlerEnv) createUserProfile(t *testing.T, userID, city string) *model.ProfileResponse {
	t.Helper()
	created, err := e.svc.CreateProfile(e.ctx, model.ProfileRequest{
		UserID: userID, Traits: map[string]interface{}{"city": city}}, e.orgID)
	require.NoError(t, err)
	return created
}

func (e *profileHandlerEnv) createCookie(t *testing.T, profileID string, active bool) string {
	t.Helper()
	cookie, err := e.svc.CreateProfileCookie(e.ctx, profileID)
	require.NoError(t, err)
	if !active {
		require.NoError(t, e.svc.UpdateCookieStatusByCookieID(e.ctx, cookie.CookieID, false))
	}
	return cookie.CookieID
}

func TestInitProfileReturnsTheProfileOfAnActiveCookie(t *testing.T) {
	env := newProfileHandlerEnv(t)
	env.markConsoleSystemApp(t)
	existing := env.createProfile(t, "Colombo")
	cookieID := env.createCookie(t, existing.ProfileID, true)

	recorder := env.callWithCookie(t, env.h.InitProfile, http.MethodPost, env.bearer(),
		`{"traits":{"city":"Kandy"}}`, cookieID)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	got := decodeProfile(t, recorder)
	assert.Equal(t, existing.ProfileID, got.ProfileID, "no new profile is created")
	assert.Equal(t, "Colombo", got.Traits["city"])
	assert.Equal(t, cookieID, got.AnonymousTracker)
	cookies := recorder.Result().Cookies()
	require.Len(t, cookies, 1, "the cookie is set again")
	assert.Equal(t, constants.ProfileCookie, cookies[0].Name)
	assert.Equal(t, cookieID, cookies[0].Value)
	assertProfileCookieAttributes(t, cookies[0])
}

func TestInitProfileCreatesANewProfileWhenTheCookieIsInactive(t *testing.T) {
	env := newProfileHandlerEnv(t)
	existing := env.createProfile(t, "Colombo")
	cookieID := env.createCookie(t, existing.ProfileID, false)

	recorder := env.callWithCookie(t, env.h.InitProfile, http.MethodPost, env.bearer(),
		`{"traits":{"city":"Kandy"}}`, cookieID)

	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	assert.NotEqual(t, existing.ProfileID, decodeProfile(t, recorder).ProfileID)
}

func (e *profileHandlerEnv) addCategory(t *testing.T) string {
	t.Helper()
	id := uuid.New().String()
	require.NoError(t, consentStore.AddConsentCategory(e.ctx, e.orgID, consentModel.ConsentCategory{
		CategoryName: "Analytics", CategoryIdentifier: id, OrgID: e.orgID, Purpose: "profiling"}))
	return id
}

func TestProfileConsentsOverHTTP(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfile(t, "Colombo")
	category := env.addCategory(t)
	ids := profileIDOf(created.ProfileID)

	updated := env.call(t, env.h.UpdateProfileConsents, http.MethodPut, env.auth.ValidToken, "",
		`[{"category_identifier":"`+category+`","is_consented":true}]`, ids)
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())

	got := env.call(t, env.h.GetProfileConsents, http.MethodGet, env.auth.ValidToken, "", "", ids)
	require.Equal(t, http.StatusOK, got.Code, got.Body.String())
	var records []model.ConsentRecord
	require.NoError(t, json.Unmarshal(got.Body.Bytes(), &records))
	require.Len(t, records, 1)
	assert.Equal(t, category, records[0].CategoryIdentifier)
	assert.True(t, records[0].IsConsented)
}

func TestProfileConsentRefusalsOverHTTP(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfile(t, "Colombo")
	ids := profileIDOf(created.ProfileID)

	unknownCategory := env.call(t, env.h.UpdateProfileConsents, http.MethodPut, env.auth.ValidToken, "",
		`[{"category_identifier":"nope","is_consented":true}]`, ids)
	assert.Equal(t, http.StatusBadRequest, unknownCategory.Code)

	malformed := env.call(t, env.h.UpdateProfileConsents, http.MethodPut, env.auth.ValidToken, "", `[{`, ids)
	assert.Equal(t, http.StatusBadRequest, malformed.Code)

	missingProfile := env.call(t, env.h.UpdateProfileConsents, http.MethodPut, env.auth.ValidToken, "", `[]`,
		profileIDOf("missing"))
	assert.Equal(t, http.StatusNotFound, missingProfile.Code)
	getMissing := env.call(t, env.h.GetProfileConsents, http.MethodGet, env.auth.ValidToken, "", "",
		profileIDOf("missing"))
	assert.Equal(t, http.StatusNotFound, getMissing.Code)

	noID := env.call(t, env.h.GetProfileConsents, http.MethodGet, env.auth.ValidToken, "", "", nil)
	assert.Equal(t, http.StatusNotFound, noID.Code)
	noIDUpdate := env.call(t, env.h.UpdateProfileConsents, http.MethodPut, env.auth.ValidToken, "", `[]`, nil)
	assert.Equal(t, http.StatusNotFound, noIDUpdate.Code)
}
