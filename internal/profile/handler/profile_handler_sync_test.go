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
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/profile/model"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaStore "github.com/wso2/identity-customer-data-service/internal/profile_schema/store"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

const (
	syncUser     = "admin"
	syncPassword = "admin-secret"
)

// syncEnv is the profile environment with the admin credentials that the identity server sends, and the
// identity attribute that the claims below map to.
func newSyncEnv(t *testing.T) *profileHandlerEnv {
	t.Helper()
	env := newProfileHandlerEnv(t)
	conf := config.GetCDSRuntime().Config
	conf.AuthServer.AdminUsername = syncUser
	conf.AuthServer.AdminPassword = syncPassword
	config.OverrideCDSRuntime(conf)
	require.NoError(t, schemaStore.AddProfileSchemaAttributesForScope(context.Background(),
		[]schemaModel.ProfileSchemaAttribute{{
			AttributeId:   "attr-email",
			AttributeName: "identity_attributes.email",
			Scope:         constants.IdentityAttributes,
			ValueType:     constants.StringDataType,
			MergeStrategy: "overwrite",
			Mutability:    constants.MutabilityReadWrite,
		}}, constants.IdentityAttributes, env.orgID))
	return env
}

func (e *profileHandlerEnv) sync(t *testing.T, authorization string,
	event map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(event)
	require.NoError(t, err)
	return e.syncRaw(t, authorization, string(body))
}

func (e *profileHandlerEnv) syncRaw(t *testing.T, authorization, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/t/"+e.handle+"/cds/api/v1/profiles/sync", strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), constants.TenantContextKey, e.handle))
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	recorder := httptest.NewRecorder()
	e.h.SyncProfile(recorder, request)
	return recorder
}

func adminAuthorization() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(syncUser+":"+syncPassword))
}

func (e *profileHandlerEnv) event(name, userID string, extra map[string]interface{}) map[string]interface{} {
	event := map[string]interface{}{"event": name, "orgHandle": e.handle, "userId": userID}
	for key, value := range extra {
		event[key] = value
	}
	return event
}

func emailClaim(email string) map[string]interface{} {
	return map[string]interface{}{"claims": map[string]interface{}{"http://wso2.org/claims/email": email}}
}

func (e *profileHandlerEnv) findUser(t *testing.T, userID string) *model.ProfileResponse {
	t.Helper()
	profile, err := e.svc.FindProfileByUserId(e.ctx, userID, e.orgID)
	if err != nil {
		return nil
	}
	return profile
}

func TestSyncRequiresTheAdminCredentials(t *testing.T) {
	env := newSyncEnv(t)
	event := env.event(constants.AddUserEvent, "u-1", nil)

	assert.Equal(t, http.StatusUnauthorized, env.sync(t, "", event).Code)
	assert.Equal(t, http.StatusUnauthorized, env.sync(t, "Bearer "+env.auth.ValidToken, event).Code)
	wrong := "Basic " + base64.StdEncoding.EncodeToString([]byte(syncUser+":wrong"))
	assert.Equal(t, http.StatusUnauthorized, env.sync(t, wrong, event).Code)
}

func TestSyncRefusals(t *testing.T) {
	env := newSyncEnv(t)

	assert.Equal(t, http.StatusBadRequest, env.syncRaw(t, adminAuthorization(), `{"event":`).Code)

	noOrg := env.sync(t, adminAuthorization(), map[string]interface{}{"event": constants.AddUserEvent, "userId": "u-1"})
	assert.Equal(t, http.StatusBadRequest, noOrg.Code)

	unknownOrg := env.sync(t, adminAuthorization(),
		map[string]interface{}{"event": constants.AddUserEvent, "orgHandle": "nobody", "userId": "u-1"})
	assert.Equal(t, http.StatusBadRequest, unknownOrg.Code)

	_, err := env.db.Exec(`DELETE FROM cds_config WHERE org_id = ?`, env.orgID)
	require.NoError(t, err)
	disabled := env.sync(t, adminAuthorization(), env.event(constants.AddUserEvent, "u-1", nil))
	assert.Equal(t, http.StatusBadRequest, disabled.Code, "CDS has to be enabled for the organization")
}

func TestAddUserCreatesAProfileFromTheClaims(t *testing.T) {
	env := newSyncEnv(t)

	recorder := env.sync(t, adminAuthorization(), env.event(constants.AddUserEvent, "u-1", emailClaim("a@b.c")))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	profile := env.findUser(t, "u-1")
	require.NotNil(t, profile)
	assert.Equal(t, "a@b.c", profile.IdentityAttributes["email"])

	// The same event again does not create a second profile.
	env.sync(t, adminAuthorization(), env.event(constants.AddUserEvent, "u-1", emailClaim("a@b.c")))
	page, _, err := env.svc.GetAllProfilesCursor(env.ctx, env.orgID, 10, nil)
	require.NoError(t, err)
	assert.Len(t, page, 1)
}

func TestAddUserWithTheCookieOfAnAnonymousProfileLinksIt(t *testing.T) {
	env := newSyncEnv(t)
	anonymous := env.createProfile(t, "Colombo")
	cookieID := env.createCookie(t, anonymous.ProfileId, true)

	recorder := env.sync(t, adminAuthorization(),
		env.event(constants.AddUserEvent, "u-1", map[string]interface{}{
			"profileCookie": cookieID,
			"claims":        map[string]interface{}{"http://wso2.org/claims/email": "a@b.c"},
		}))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	linked, err := env.svc.GetProfile(env.ctx, anonymous.ProfileId, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "u-1", linked.UserId)
	assert.Equal(t, "a@b.c", linked.IdentityAttributes["email"])
	assert.Equal(t, "Colombo", linked.Traits["city"], "the anonymous data is kept")
}

func TestUpdateUserClaimsUpdatesTheIdentityAttributes(t *testing.T) {
	env := newSyncEnv(t)
	env.sync(t, adminAuthorization(), env.event(constants.AddUserEvent, "u-1", emailClaim("old@b.c")))

	for _, event := range []string{constants.UpdateUserClaimsEvent, constants.UpdateUserClaimEvent} {
		updated := env.sync(t, adminAuthorization(), env.event(event, "u-1", emailClaim(event+"@b.c")))

		require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
		assert.Equal(t, event+"@b.c", env.findUser(t, "u-1").IdentityAttributes["email"])
	}
}

func TestDeleteUserDeletesTheirProfile(t *testing.T) {
	env := newSyncEnv(t)
	env.sync(t, adminAuthorization(), env.event(constants.AddUserEvent, "u-1", emailClaim("a@b.c")))
	require.NotNil(t, env.findUser(t, "u-1"))

	recorder := env.sync(t, adminAuthorization(), env.event(constants.DeleteUserEvent, "u-1", nil))

	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Nil(t, env.findUser(t, "u-1"))
}

func TestAuthenticationSuccessAttachesTheUserToTheAnonymousProfile(t *testing.T) {
	env := newSyncEnv(t)
	anonymous := env.createProfile(t, "Colombo")
	cookieID := env.createCookie(t, anonymous.ProfileId, true)

	noUser := env.sync(t, adminAuthorization(), env.event(constants.AuthenticationSuccess, "", nil))
	assert.Equal(t, http.StatusBadRequest, noUser.Code, "the event needs a user")

	recorder := env.sync(t, adminAuthorization(), env.event(constants.AuthenticationSuccess, "u-1",
		map[string]interface{}{"profileCookie": cookieID}))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.JSONEq(t, `{"status": "updated"}`, recorder.Body.String())
	linked, err := env.svc.GetProfile(env.ctx, anonymous.ProfileId, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "u-1", linked.UserId)

	// A cookie that matches nothing, and no cookie at all, change nothing.
	stale := env.sync(t, adminAuthorization(), env.event(constants.AuthenticationSuccess, "u-9",
		map[string]interface{}{"profileCookie": "stale"}))
	assert.Equal(t, http.StatusOK, stale.Code)
	assert.Nil(t, env.findUser(t, "u-9"))
	none := env.sync(t, adminAuthorization(), env.event(constants.AuthenticationSuccess, "u-9", nil))
	assert.Equal(t, http.StatusOK, none.Code)
}

func TestAuthenticationSuccessKeepsAnAlreadyLinkedProfile(t *testing.T) {
	env := newSyncEnv(t)
	linked := env.createUserProfile(t, "u-1", "Colombo")
	cookieID := env.createCookie(t, linked.ProfileId, true)

	recorder := env.sync(t, adminAuthorization(), env.event(constants.AuthenticationSuccess, "u-1",
		map[string]interface{}{"profileCookie": cookieID}))

	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, linked.ProfileId, env.findUser(t, "u-1").ProfileId)
}

func TestSessionTerminationDeactivatesTheCookieOfTheUser(t *testing.T) {
	env := newSyncEnv(t)
	profile := env.createUserProfile(t, "u-1", "Colombo")
	other := env.createUserProfile(t, "u-2", "Kandy")
	own := env.createCookie(t, profile.ProfileId, true)
	foreign := env.createCookie(t, other.ProfileId, true)
	terminate := func(userID, cookie string) *httptest.ResponseRecorder {
		extra := map[string]interface{}{}
		if cookie != "" {
			extra["profileCookie"] = cookie
		}
		return env.sync(t, adminAuthorization(), env.event(constants.SessionTermination, userID, extra))
	}
	active := func(cookie string) bool {
		got, err := env.svc.GetProfileCookieById(env.ctx, cookie, env.orgID)
		require.NoError(t, err)
		return got.IsActive
	}

	// None of these may touch a cookie that is not the user's.
	assert.Equal(t, http.StatusOK, terminate("u-1", foreign).Code)
	assert.True(t, active(foreign), "the cookie of another profile stays active")
	assert.Equal(t, http.StatusOK, terminate("u-1", "").Code)
	assert.Equal(t, http.StatusOK, terminate("u-1", "unknown-cookie").Code)
	assert.True(t, active(own))

	assert.Equal(t, http.StatusOK, terminate("u-1", own).Code)
	assert.False(t, active(own), "the user's own cookie is deactivated")
}

func TestSyncIgnoresAnEventItDoesNotKnow(t *testing.T) {
	env := newSyncEnv(t)

	recorder := env.sync(t, adminAuthorization(), env.event("POST_SOMETHING_ELSE", "u-1", nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Nil(t, env.findUser(t, "u-1"))
}
