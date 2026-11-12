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
	request := httptest.NewRequest(method, "/t/"+e.handle+"/cds/api/v1/profiles/Me", strings.NewReader(body))
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

// userToken is a JWT of the user, for the organization of the environment.
func (e *profileHandlerEnv) userToken(t *testing.T, subject string) string {
	t.Helper()
	claims := map[string]interface{}{"org_handle": e.handle}
	if subject != "" {
		claims["sub"] = subject
	}
	return "Bearer " + e.auth.NewJWT(t, claims)
}

func (e *profileHandlerEnv) createUserProfile(t *testing.T, userID, city string) *model.ProfileResponse {
	t.Helper()
	created, err := e.svc.CreateProfile(e.ctx, model.ProfileRequest{
		UserId: userID, Traits: map[string]interface{}{"city": city}}, e.orgID)
	require.NoError(t, err)
	return created
}

func (e *profileHandlerEnv) createCookie(t *testing.T, profileID string, active bool) string {
	t.Helper()
	cookie, err := e.svc.CreateProfileCookie(e.ctx, profileID)
	require.NoError(t, err)
	if !active {
		require.NoError(t, e.svc.UpdateCookieStatusByCookieId(e.ctx, cookie.CookieId, false))
	}
	return cookie.CookieId
}

func TestGetCurrentUserProfileByTheTokenSubject(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createUserProfile(t, "u-1", "Colombo")

	recorder := env.callWithCookie(t, env.h.GetCurrentUserProfile, http.MethodGet, env.userToken(t, "u-1"), "", "")

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, created.ProfileId, decodeProfile(t, recorder).ProfileId)
}

func TestGetCurrentUserProfileByTheCookie(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfile(t, "Colombo")
	cookieID := env.createCookie(t, created.ProfileId, true)

	// An opaque token carries no subject, so the cookie is what finds the profile.
	recorder := env.callWithCookie(t, env.h.GetCurrentUserProfile, http.MethodGet, env.bearer(), "", cookieID)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, created.ProfileId, decodeProfile(t, recorder).ProfileId)
}

func TestGetCurrentUserProfileRefusals(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfile(t, "Colombo")
	inactive := env.createCookie(t, created.ProfileId, false)

	tests := []struct {
		name          string
		authorization string
		cookie        string
		status        int
	}{
		{"an opaque token and no cookie", env.bearer(), "", http.StatusUnauthorized},
		{"an opaque token and an inactive cookie", env.bearer(), inactive, http.StatusUnauthorized},
		{"a token without a subject", env.userToken(t, ""), "", http.StatusUnauthorized},
		{"a subject that has no profile", env.userToken(t, "nobody"), "", http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := env.callWithCookie(t, env.h.GetCurrentUserProfile, http.MethodGet, tc.authorization, "",
				tc.cookie)

			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}
}

func TestPatchCurrentUserProfile(t *testing.T) {
	env := newProfileHandlerEnv(t)
	byCookie := env.createProfile(t, "Colombo")
	cookieID := env.createCookie(t, byCookie.ProfileId, true)
	env.createUserProfile(t, "u-2", "Kandy")

	viaCookie := env.callWithCookie(t, env.h.PatchCurrentUserProfile, http.MethodPatch, env.bearer(),
		`{"traits":{"city":"Galle"}}`, cookieID)
	require.Equal(t, http.StatusOK, viaCookie.Code, viaCookie.Body.String())
	assert.Equal(t, "Galle", decodeProfile(t, viaCookie).Traits["city"])

	viaToken := env.callWithCookie(t, env.h.PatchCurrentUserProfile, http.MethodPatch, env.userToken(t, "u-2"),
		`{"traits":{"city":"Jaffna"}}`, "")
	require.Equal(t, http.StatusOK, viaToken.Code, viaToken.Body.String())
	assert.Equal(t, "Jaffna", decodeProfile(t, viaToken).Traits["city"])
}

func TestPatchCurrentUserProfileRefusals(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createUserProfile(t, "u-1", "Colombo")
	cookieID := env.createCookie(t, created.ProfileId, true)

	tests := []struct {
		name          string
		authorization string
		cookie        string
		body          string
		status        int
	}{
		{"an opaque token and no cookie", env.bearer(), "", `{}`, http.StatusUnauthorized},
		{"a token without a subject", env.userToken(t, ""), "", `{}`, http.StatusUnauthorized},
		{"a subject that has no profile", env.userToken(t, "nobody"), "", `{}`, http.StatusUnauthorized},
		{"malformed JSON", env.bearer(), cookieID, `{"traits":`, http.StatusBadRequest},
		{"a value that breaks the schema", env.bearer(), cookieID, `{"traits":{"age":"old"}}`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := env.callWithCookie(t, env.h.PatchCurrentUserProfile, http.MethodPatch, tc.authorization,
				tc.body, tc.cookie)

			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}
}

func TestInitProfileReturnsTheProfileOfAnActiveCookie(t *testing.T) {
	env := newProfileHandlerEnv(t)
	existing := env.createProfile(t, "Colombo")
	cookieID := env.createCookie(t, existing.ProfileId, true)

	recorder := env.callWithCookie(t, env.h.InitProfile, http.MethodPost, env.bearer(),
		`{"traits":{"city":"Kandy"}}`, cookieID)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	got := decodeProfile(t, recorder)
	assert.Equal(t, existing.ProfileId, got.ProfileId, "no new profile is created")
	assert.Equal(t, "Colombo", got.Traits["city"])
	assert.Equal(t, cookieID, got.AnonymousProfileTracker)
}

func TestInitProfileCreatesANewProfileWhenTheCookieIsInactive(t *testing.T) {
	env := newProfileHandlerEnv(t)
	existing := env.createProfile(t, "Colombo")
	cookieID := env.createCookie(t, existing.ProfileId, false)

	recorder := env.callWithCookie(t, env.h.InitProfile, http.MethodPost, env.bearer(),
		`{"traits":{"city":"Kandy"}}`, cookieID)

	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	assert.NotEqual(t, existing.ProfileId, decodeProfile(t, recorder).ProfileId)
}

func (e *profileHandlerEnv) addCategory(t *testing.T) string {
	t.Helper()
	id := uuid.New().String()
	require.NoError(t, consentStore.AddConsentCategory(e.ctx, e.orgID, consentModel.ConsentCategory{
		CategoryName: "Analytics", CategoryIdentifier: id, OrgId: e.orgID, Purpose: "profiling"}))
	return id
}

func TestProfileConsentsOverHTTP(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfile(t, "Colombo")
	category := env.addCategory(t)
	ids := profileIDOf(created.ProfileId)

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
	ids := profileIDOf(created.ProfileId)

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
