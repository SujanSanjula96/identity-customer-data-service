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
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaStore "github.com/wso2/identity-customer-data-service/internal/profile_schema/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

var profileHandlerOrgCounter atomic.Int64

// profileHandlerEnv is an organization with CDS enabled, a small profile schema and a stub
// identity server, so that a handler runs with its authentication and authorization unmodified.
// The profile workers are not started, so nothing runs in the background.
type profileHandlerEnv struct {
	ctx    context.Context
	db     *sql.DB
	handle string
	orgID  string
	auth   setup.AuthStub
	h      *ProfileHandler
	svc    *profileService.ProfilesService
}

func newProfileHandlerEnv(t *testing.T) *profileHandlerEnv {
	t.Helper()
	require.NoError(t, log.Init("ERROR"))
	setup.EnableOrganizationManagement()
	testDB, err := setup.SetupTestSQLite()
	require.NoError(t, err)
	provider.SetTestDB(testDB.DB, database.TypeSQLite)
	t.Cleanup(func() {
		provider.SetTestDB(nil, "")
		testDB.Terminate()
	})
	auth := setup.InstallAuthStub(t, "profile:view", "profile:create", "profile:update", "profile:delete",
		"profile:link")

	handle := fmt.Sprintf("acme-%d", profileHandlerOrgCounter.Add(1))
	env := &profileHandlerEnv{ctx: context.Background(), db: testDB.DB, handle: handle, orgID: setup.TestOrgId(handle),
		auth: auth, h: &ProfileHandler{}, svc: &profileService.ProfilesService{}}
	setup.TestOrg(t, handle)
	_, err = env.db.Exec(`INSERT INTO cds_config (org_id, config, value) VALUES (?, 'cds_enabled', 'true')`, env.orgID)
	require.NoError(t, err)
	env.addTrait(t, "city", constants.StringDataType)
	env.addTrait(t, "age", constants.IntegerDataType)
	return env
}

func (e *profileHandlerEnv) addTrait(t *testing.T, name, valueType string) {
	t.Helper()
	full := constants.Traits + "." + name
	require.NoError(t, schemaStore.AddProfileSchemaAttributesForScope(e.ctx, []schemaModel.ProfileSchemaAttribute{{
		AttributeId:   "attr-" + full,
		AttributeName: full,
		Scope:         constants.Traits,
		ValueType:     valueType,
		MergeStrategy: "overwrite",
		Mutability:    constants.MutabilityReadWrite,
	}}, constants.Traits, e.orgID))
}

func (e *profileHandlerEnv) call(t *testing.T, handler http.HandlerFunc, method, token, query, body string,
	pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/t/"+e.handle+"/cds/api/v1/profiles"+query, strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), constants.TenantContextKey, e.handle))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("Content-Type", "application/json")
	for name, value := range pathValues {
		request.SetPathValue(name, value)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func profileIDOf(id string) map[string]string { return map[string]string{"profileId": id} }

func decodeProfile(t *testing.T, recorder *httptest.ResponseRecorder) model.ProfileResponse {
	t.Helper()
	var profile model.ProfileResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &profile), recorder.Body.String())
	return profile
}

// createProfile stores a profile through the service.
func (e *profileHandlerEnv) createProfile(t *testing.T, city string) *model.ProfileResponse {
	t.Helper()
	created, err := e.svc.CreateProfile(e.ctx, model.ProfileRequest{
		Traits: map[string]interface{}{"city": city}}, e.orgID)
	require.NoError(t, err)
	return created
}

func TestInitProfileCreatesAProfileAndSetsTheCookie(t *testing.T) {
	env := newProfileHandlerEnv(t)

	recorder := env.call(t, env.h.InitProfile, http.MethodPost, env.auth.ValidToken, "",
		`{"traits":{"city":"Colombo","age":30}}`, nil)

	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	profile := decodeProfile(t, recorder)
	assert.NotEmpty(t, profile.ProfileId)
	assert.Equal(t, "Colombo", profile.Traits["city"])
	assert.NotEmpty(t, profile.AnonymousProfileTracker)
	assert.Contains(t, recorder.Header().Get("Location"), "/profiles/"+profile.ProfileId)
	var cookie *http.Cookie
	for _, c := range recorder.Result().Cookies() {
		if c.Name == constants.ProfileCookie {
			cookie = c
		}
	}
	require.NotNil(t, cookie, "the profile cookie is set")
	assert.Equal(t, profile.AnonymousProfileTracker, cookie.Value)
}

func TestInitProfileRefusals(t *testing.T) {
	env := newProfileHandlerEnv(t)
	tests := map[string]string{
		"malformed JSON":      `{"traits":`,
		"an unknown field":    `{"color":"red"}`,
		"an unknown trait":    `{"traits":{"nope":"x"}}`,
		"a wrongly typed one": `{"traits":{"age":"old"}}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := env.call(t, env.h.InitProfile, http.MethodPost, env.auth.ValidToken, "", body, nil)

			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}
}

func TestGetProfile(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfile(t, "Colombo")

	found := env.call(t, env.h.GetProfile, http.MethodGet, env.auth.ValidToken, "", "", profileIDOf(created.ProfileId))
	require.Equal(t, http.StatusOK, found.Code, found.Body.String())
	assert.Equal(t, "application/json", found.Header().Get("Content-Type"))
	assert.Equal(t, created.ProfileId, decodeProfile(t, found).ProfileId)

	missing := env.call(t, env.h.GetProfile, http.MethodGet, env.auth.ValidToken, "", "", profileIDOf("missing"))
	assert.Equal(t, http.StatusNotFound, missing.Code)

	noID := env.call(t, env.h.GetProfile, http.MethodGet, env.auth.ValidToken, "", "", nil)
	assert.Equal(t, http.StatusNotFound, noID.Code)
}

func TestGetAllProfilesPagesThroughTheProfiles(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := map[string]bool{}
	for i := 0; i < 5; i++ {
		created[env.createProfile(t, fmt.Sprintf("city-%d", i)).ProfileId] = true
	}

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		query := "?page_size=2"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		recorder := env.call(t, env.h.GetAllProfiles, http.MethodGet, env.auth.ValidToken, query, "", nil)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		var page model.ProfileListAPIResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &page))
		require.LessOrEqual(t, len(page.Items), 2)
		for _, item := range page.Items {
			assert.False(t, seen[item.ProfileId], "no profile appears twice")
			seen[item.ProfileId] = true
		}
		cursor = page.Pagination.NextCursor
		if cursor == "" {
			break
		}
	}

	assert.Equal(t, created, seen)
}

func TestGetAllProfilesRefusals(t *testing.T) {
	env := newProfileHandlerEnv(t)
	tests := map[string]string{
		"a page size that is not a number": "?page_size=abc",
		"a cursor that is not valid":       "?cursor=not-a-cursor",
		"a threshold that is not a number": "?fuzzyFilter=traits.city+eq+x&threshold=abc",
	}
	for name, query := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := env.call(t, env.h.GetAllProfiles, http.MethodGet, env.auth.ValidToken, query, "", nil)

			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}
}

func TestGetAllProfilesSelectsTheRequestedAttributes(t *testing.T) {
	env := newProfileHandlerEnv(t)
	env.createProfile(t, "Colombo")

	recorder := env.call(t, env.h.GetAllProfiles, http.MethodGet, env.auth.ValidToken, "?attributes=traits.city", "",
		nil)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var page model.ProfileListAPIResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	assert.Equal(t, map[string]interface{}{"city": "Colombo"}, page.Items[0].Traits)
	assert.Nil(t, page.Items[0].IdentityAttributes)
}

func TestGetAllProfilesFiltersByTrait(t *testing.T) {
	env := newProfileHandlerEnv(t)
	colombo := env.createProfile(t, "Colombo")
	env.createProfile(t, "Kandy")

	recorder := env.call(t, env.h.GetAllProfiles, http.MethodGet, env.auth.ValidToken,
		"?filter=traits.city+eq+Colombo", "", nil)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var page model.ProfileListAPIResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	assert.Equal(t, colombo.ProfileId, page.Items[0].ProfileId)
}

func TestUpdateProfile(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfile(t, "Colombo")

	updated := env.call(t, env.h.UpdateProfile, http.MethodPut, env.auth.ValidToken, "",
		`{"traits":{"city":"Kandy"}}`, profileIDOf(created.ProfileId))
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	assert.Equal(t, "Kandy", decodeProfile(t, updated).Traits["city"])

	invalid := env.call(t, env.h.UpdateProfile, http.MethodPut, env.auth.ValidToken, "",
		`{"traits":{"age":"old"}}`, profileIDOf(created.ProfileId))
	assert.Equal(t, http.StatusBadRequest, invalid.Code)

	missing := env.call(t, env.h.UpdateProfile, http.MethodPut, env.auth.ValidToken, "",
		`{"traits":{"city":"Kandy"}}`, profileIDOf("missing"))
	assert.Equal(t, http.StatusNotFound, missing.Code)

	malformed := env.call(t, env.h.UpdateProfile, http.MethodPut, env.auth.ValidToken, "", `{"traits":`,
		profileIDOf(created.ProfileId))
	assert.Equal(t, http.StatusBadRequest, malformed.Code)

	noID := env.call(t, env.h.UpdateProfile, http.MethodPut, env.auth.ValidToken, "", `{}`, nil)
	assert.Equal(t, http.StatusNotFound, noID.Code)
}

func TestPatchProfile(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created, err := env.svc.CreateProfile(env.ctx, model.ProfileRequest{
		Traits: map[string]interface{}{"city": "Colombo", "age": float64(30)}}, env.orgID)
	require.NoError(t, err)

	patched := env.call(t, env.h.PatchProfile, http.MethodPatch, env.auth.ValidToken, "",
		`{"traits":{"city":"Kandy"}}`, profileIDOf(created.ProfileId))
	require.Equal(t, http.StatusOK, patched.Code, patched.Body.String())
	traits := decodeProfile(t, patched).Traits
	assert.Equal(t, "Kandy", traits["city"])
	assert.EqualValues(t, 30, traits["age"], "a trait that was not patched stays")

	invalid := env.call(t, env.h.PatchProfile, http.MethodPatch, env.auth.ValidToken, "",
		`{"traits":{"age":"old"}}`, profileIDOf(created.ProfileId))
	assert.Equal(t, http.StatusBadRequest, invalid.Code)

	missing := env.call(t, env.h.PatchProfile, http.MethodPatch, env.auth.ValidToken, "",
		`{"traits":{"city":"Kandy"}}`, profileIDOf("missing"))
	assert.Equal(t, http.StatusNotFound, missing.Code)

	malformed := env.call(t, env.h.PatchProfile, http.MethodPatch, env.auth.ValidToken, "", `{"traits":`,
		profileIDOf(created.ProfileId))
	assert.Equal(t, http.StatusBadRequest, malformed.Code)

	noID := env.call(t, env.h.PatchProfile, http.MethodPatch, env.auth.ValidToken, "", `{}`, nil)
	assert.Equal(t, http.StatusNotFound, noID.Code)
}

func TestDeleteProfile(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfile(t, "Colombo")

	first := env.call(t, env.h.DeleteProfile, http.MethodDelete, env.auth.ValidToken, "", "",
		profileIDOf(created.ProfileId))
	assert.Equal(t, http.StatusNoContent, first.Code)

	// A profile that is gone is not an error.
	again := env.call(t, env.h.DeleteProfile, http.MethodDelete, env.auth.ValidToken, "", "",
		profileIDOf(created.ProfileId))
	assert.Equal(t, http.StatusNoContent, again.Code)

	get := env.call(t, env.h.GetProfile, http.MethodGet, env.auth.ValidToken, "", "", profileIDOf(created.ProfileId))
	assert.Equal(t, http.StatusNotFound, get.Code)

	noID := env.call(t, env.h.DeleteProfile, http.MethodDelete, env.auth.ValidToken, "", "", nil)
	assert.Equal(t, http.StatusNotFound, noID.Code)
}

func TestLinkProfile(t *testing.T) {
	env := newProfileHandlerEnv(t)
	anonymous := env.createProfile(t, "Colombo")
	link := func(profileId, userId string) *httptest.ResponseRecorder {
		return env.call(t, env.h.LinkProfile, http.MethodPost, env.auth.ValidToken, "",
			fmt.Sprintf(`{"profile_id":%q,"user_id":%q}`, profileId, userId), nil)
	}

	linked := link(anonymous.ProfileId, "u-1")
	require.Equal(t, http.StatusOK, linked.Code, linked.Body.String())
	var response model.ProfileLinkResponse
	require.NoError(t, json.Unmarshal(linked.Body.Bytes(), &response))
	assert.Equal(t, model.ProfileLinkResponse{ProfileId: anonymous.ProfileId, UserId: "u-1"}, response)

	stored, err := env.svc.GetProfile(env.ctx, anonymous.ProfileId, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "u-1", stored.UserId)
	assert.Equal(t, "Colombo", stored.Traits["city"], "the profile keeps its data")

	// Linking the same user again is a no-op, linking another user is refused.
	assert.Equal(t, http.StatusOK, link(anonymous.ProfileId, "u-1").Code)
	assert.Equal(t, http.StatusConflict, link(anonymous.ProfileId, "u-2").Code)

	assert.Equal(t, http.StatusBadRequest, link("", "u-1").Code)
	assert.Equal(t, http.StatusBadRequest, link(anonymous.ProfileId, " ").Code)
	assert.Equal(t, http.StatusNotFound, link("missing", "u-1").Code)

	malformed := env.call(t, env.h.LinkProfile, http.MethodPost, env.auth.ValidToken, "", `{"profile_id":`, nil)
	assert.Equal(t, http.StatusBadRequest, malformed.Code)
}

func TestProfileHandlersRequireAuthentication(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfile(t, "Colombo")
	ids := profileIDOf(created.ProfileId)

	calls := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		body    string
	}{
		{"init", env.h.InitProfile, http.MethodPost, `{}`},
		{"get", env.h.GetProfile, http.MethodGet, ""},
		{"list", env.h.GetAllProfiles, http.MethodGet, ""},
		{"update", env.h.UpdateProfile, http.MethodPut, `{}`},
		{"patch", env.h.PatchProfile, http.MethodPatch, `{}`},
		{"delete", env.h.DeleteProfile, http.MethodDelete, ""},
		{"link", env.h.LinkProfile, http.MethodPost, `{}`},
		{"current user", env.h.GetCurrentUserProfile, http.MethodGet, ""},
		{"patch current user", env.h.PatchCurrentUserProfile, http.MethodPatch, `{}`},
		{"get consents", env.h.GetProfileConsents, http.MethodGet, ""},
		{"update consents", env.h.UpdateProfileConsents, http.MethodPut, `[]`},
	}
	for _, tc := range calls {
		t.Run(tc.name+" without a token", func(t *testing.T) {
			recorder := env.call(t, tc.handler, tc.method, "", "", tc.body, ids)

			assert.Equal(t, http.StatusUnauthorized, recorder.Code)
		})
		t.Run(tc.name+" with the wrong scope", func(t *testing.T) {
			recorder := env.call(t, tc.handler, tc.method, env.auth.OtherToken, "", tc.body, ids)

			assert.Equal(t, http.StatusForbidden, recorder.Code)
		})
	}
}

func TestProfileHandlersRefuseAnOrganizationWithoutCDS(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfile(t, "Colombo")
	_, err := env.db.Exec(`DELETE FROM cds_config WHERE org_id = ?`, env.orgID)
	require.NoError(t, err)

	for name, handler := range map[string]http.HandlerFunc{
		"init":   env.h.InitProfile,
		"get":    env.h.GetProfile,
		"list":   env.h.GetAllProfiles,
		"update": env.h.UpdateProfile,
		"patch":  env.h.PatchProfile,
		"delete": env.h.DeleteProfile,
		"link":   env.h.LinkProfile,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := env.call(t, handler, http.MethodPost, env.auth.ValidToken, "", `{}`,
				profileIDOf(created.ProfileId))

			assert.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}
