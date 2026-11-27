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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaStore "github.com/wso2/identity-customer-data-service/internal/profile_schema/store"
	"github.com/wso2/identity-customer-data-service/internal/system/audit"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
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
			AttributeID:   "attr-email",
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
	profile, err := e.svc.FindProfileByUserID(e.ctx, userID, e.orgID)
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
	assert.Equal(t, http.StatusBadRequest, disabled.Code, "CDD has to be enabled for the organization")
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
	cookieID := env.createCookie(t, anonymous.ProfileID, true)

	recorder := env.sync(t, adminAuthorization(),
		env.event(constants.AddUserEvent, "u-1", map[string]interface{}{
			"profileCookie": cookieID,
			"claims":        map[string]interface{}{"http://wso2.org/claims/email": "a@b.c"},
		}))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	linked, err := env.svc.GetProfile(env.ctx, anonymous.ProfileID, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "u-1", linked.UserID)
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
	cookieID := env.createCookie(t, anonymous.ProfileID, true)

	noUser := env.sync(t, adminAuthorization(), env.event(constants.AuthenticationSuccess, "", nil))
	assert.Equal(t, http.StatusBadRequest, noUser.Code, "the event needs a user")

	recorder := env.sync(t, adminAuthorization(), env.event(constants.AuthenticationSuccess, "u-1",
		map[string]interface{}{"profileCookie": cookieID}))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.JSONEq(t, `{"status": "updated"}`, recorder.Body.String())
	linked, err := env.svc.GetProfile(env.ctx, anonymous.ProfileID, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "u-1", linked.UserID)

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
	cookieID := env.createCookie(t, linked.ProfileID, true)

	recorder := env.sync(t, adminAuthorization(), env.event(constants.AuthenticationSuccess, "u-1",
		map[string]interface{}{"profileCookie": cookieID}))

	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, linked.ProfileID, env.findUser(t, "u-1").ProfileID)
}

func TestSessionEndDeactivatesTheCookieOfTheUser(t *testing.T) {
	for _, event := range []string{constants.SessionTermination, constants.SessionExpiry} {
		t.Run(event, func(t *testing.T) {
			env := newSyncEnv(t)
			profile := env.createUserProfile(t, "u-1", "Colombo")
			other := env.createUserProfile(t, "u-2", "Kandy")
			own := env.createCookie(t, profile.ProfileID, true)
			foreign := env.createCookie(t, other.ProfileID, true)
			end := func(userID, cookie string) *httptest.ResponseRecorder {
				extra := map[string]interface{}{}
				if cookie != "" {
					extra["profileCookie"] = cookie
				}
				return env.sync(t, adminAuthorization(), env.event(event, userID, extra))
			}

			// None of these may touch a cookie that is not the user's.
			assert.Equal(t, http.StatusOK, end("u-1", foreign).Code)
			assert.True(t, env.cookieActive(t, foreign), "the cookie of another profile stays active")
			assert.Equal(t, http.StatusOK, end("u-1", "").Code)
			assert.Equal(t, http.StatusOK, end("u-1", "unknown-cookie").Code)
			assert.True(t, env.cookieActive(t, own))

			assert.Equal(t, http.StatusOK, end("u-1", own).Code)
			assert.False(t, env.cookieActive(t, own), "the user's own cookie is deactivated")
		})
	}
}

func TestAnotherUserDoesNotGetTheProfileOfAnExpiredSession(t *testing.T) {
	env := newSyncEnv(t)
	first := env.createUserProfile(t, "u-1", "Colombo")
	cookieID := env.createCookie(t, first.ProfileID, true)

	// The session of u-1 timed out, and u-2 logs in from the same browser.
	expire := env.sync(t, adminAuthorization(), env.event(constants.SessionExpiry, "u-1",
		map[string]interface{}{"profileCookie": cookieID}))
	require.Equal(t, http.StatusOK, expire.Code, expire.Body.String())
	login := env.sync(t, adminAuthorization(), env.event(constants.AuthenticationSuccess, "u-2",
		map[string]interface{}{"profileCookie": cookieID}))
	require.Equal(t, http.StatusOK, login.Code, login.Body.String())

	assert.False(t, env.cookieActive(t, cookieID), "the cookie stays deactivated")
	env.assertStillOwnedBy(t, first, "u-1", "Colombo")
	assert.Nil(t, env.findUser(t, "u-2"), "u-2 is not given the profile of u-1")
}

func TestSyncIgnoresAnEventItDoesNotKnow(t *testing.T) {
	env := newSyncEnv(t)

	recorder := env.sync(t, adminAuthorization(), env.event("POST_SOMETHING_ELSE", "u-1", nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Nil(t, env.findUser(t, "u-1"))
}

// cookieActive reports whether the profile cookie is active.
func (e *profileHandlerEnv) cookieActive(t *testing.T, cookieID string) bool {
	t.Helper()
	got, err := e.svc.GetProfileCookieByID(e.ctx, cookieID, e.orgID)
	require.NoError(t, err)
	return got.IsActive
}

// mergeInto marks the child profile as merged into the master profile, as unification does.
func (e *profileHandlerEnv) mergeInto(t *testing.T, master, child *model.ProfileResponse) {
	t.Helper()
	require.NoError(t, profileStore.UpdateProfileReferences(e.ctx, model.Profile{ProfileID: master.ProfileID},
		[]model.Reference{{ProfileID: child.ProfileID, Reason: "test"}}))
	merged, err := e.svc.GetProfile(e.ctx, child.ProfileID, e.orgID)
	require.NoError(t, err)
	require.Equal(t, master.UserID, merged.UserID, "a merged profile reads the user of its master")
}

// assertStillOwnedBy checks that the profile still belongs to the user and keeps its data.
func (e *profileHandlerEnv) assertStillOwnedBy(t *testing.T, profile *model.ProfileResponse, userID, city string) {
	t.Helper()
	got, err := e.svc.GetProfile(e.ctx, profile.ProfileID, e.orgID)
	require.NoError(t, err)
	assert.Equal(t, userID, got.UserID, "the profile keeps its user")
	assert.Equal(t, city, got.Traits["city"], "the profile keeps its data")
	owner := e.findUser(t, userID)
	require.NotNil(t, owner)
	assert.Equal(t, profile.ProfileID, owner.ProfileID, "the user keeps the profile")
}

func TestAddUserDoesNotTakeTheProfileOfAnotherUser(t *testing.T) {
	env := newSyncEnv(t)
	owned := env.createUserProfile(t, "u-owner", "Colombo")
	child := env.createProfile(t, "Kandy")
	env.mergeInto(t, owned, child)

	for name, cookieProfile := range map[string]*model.ProfileResponse{
		"the cookie of the user's profile":  owned,
		"the cookie of a merged-in profile": child,
	} {
		t.Run(name, func(t *testing.T) {
			newUser := "u-new-" + cookieProfile.ProfileID
			cookieID := env.createCookie(t, cookieProfile.ProfileID, true)

			recorder := env.sync(t, adminAuthorization(), env.event(constants.AddUserEvent, newUser,
				map[string]interface{}{
					"profileCookie": cookieID,
					"claims":        map[string]interface{}{"http://wso2.org/claims/email": "new@b.c"},
				}))

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			env.assertStillOwnedBy(t, owned, "u-owner", "Colombo")
			created := env.findUser(t, newUser)
			require.NotNil(t, created, "the new user gets a profile of their own")
			assert.NotEqual(t, owned.ProfileID, created.ProfileID)
			assert.NotEqual(t, child.ProfileID, created.ProfileID)
			assert.Equal(t, "new@b.c", created.IdentityAttributes["email"])
			assert.Nil(t, created.Traits["city"], "no data of the other user")
		})
	}
}

func TestAddUserWithTheCookieOfTheirOwnProfileUpdatesIt(t *testing.T) {
	env := newSyncEnv(t)
	owned := env.createUserProfile(t, "u-1", "Colombo")
	cookieID := env.createCookie(t, owned.ProfileID, true)

	recorder := env.sync(t, adminAuthorization(), env.event(constants.AddUserEvent, "u-1",
		map[string]interface{}{
			"profileCookie": cookieID,
			"claims":        map[string]interface{}{"http://wso2.org/claims/email": "a@b.c"},
		}))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	got := env.findUser(t, "u-1")
	require.NotNil(t, got)
	assert.Equal(t, owned.ProfileID, got.ProfileID)
	assert.Equal(t, "a@b.c", got.IdentityAttributes["email"])
}

func TestAuthenticationSuccessDoesNotTakeTheProfileOfAnotherUser(t *testing.T) {
	env := newSyncEnv(t)
	owned := env.createUserProfile(t, "u-owner", "Colombo")
	child := env.createProfile(t, "Kandy")
	env.mergeInto(t, owned, child)
	env.createUserProfile(t, "u-other", "Galle")

	for _, cookieProfile := range []*model.ProfileResponse{owned, child} {
		cookieID := env.createCookie(t, cookieProfile.ProfileID, true)
		// One user with a profile of their own, and one user without a profile.
		for _, userID := range []string{"u-other", "u-without-profile"} {
			recorder := env.sync(t, adminAuthorization(), env.event(constants.AuthenticationSuccess, userID,
				map[string]interface{}{"profileCookie": cookieID}))

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			env.assertStillOwnedBy(t, owned, "u-owner", "Colombo")
		}
	}
	assert.Equal(t, "Galle", env.findUser(t, "u-other").Traits["city"])
	assert.Nil(t, env.findUser(t, "u-without-profile"))
}

// syncAudited sends one sync event through the audit wrapper of the route, and returns the
// response and the audit record.
func (e *profileHandlerEnv) syncAudited(t *testing.T, event map[string]interface{}) (
	*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	require.NoError(t, audit.Init(audit.Options{
		Enabled: true,
		Output:  log.OutputOptions{File: log.FileOutput(auditPath)},
	}))
	defer func() { _ = audit.Init(audit.Options{}) }()

	body, err := json.Marshal(event)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/t/"+e.handle+"/cds/api/v1/profiles/sync",
		strings.NewReader(string(body)))
	request = request.WithContext(context.WithValue(request.Context(), constants.TenantContextKey, e.handle))
	request.Header.Set("Authorization", adminAuthorization())
	recorder := httptest.NewRecorder()
	audit.Wrap(audit.ProfileSync, e.h.SyncProfile)(recorder, request)

	content, err := os.ReadFile(auditPath) // #nosec G304 -- the test reads a file in its temporary directory.
	require.NoError(t, err)
	lines := bytes.Split(bytes.TrimSpace(content), []byte("\n"))
	require.Len(t, lines, 1)
	var record map[string]any
	require.NoError(t, json.Unmarshal(lines[0], &record))
	return recorder, record
}

func TestSyncAuditNamesTheMasterOfAMergedCookieProfile(t *testing.T) {
	env := newSyncEnv(t)
	master := env.createUserProfile(t, "u-1", "Colombo")
	childID := "child-of-" + master.ProfileID
	now := time.Now().UTC()
	require.NoError(t, profileStore.InsertProfile(env.ctx, env.orgID, model.Profile{
		ProfileID: childID,
		OrgID:     env.orgID,
		CreatedAt: now,
		UpdatedAt: now,
		ProfileStatus: &model.ProfileStatus{
			ReferenceProfileID: master.ProfileID,
			ReferenceReason:    "email",
		},
	}))
	cookieID := env.createCookie(t, childID, true)

	recorder, record := env.syncAudited(t, env.event(constants.AddUserEvent, "u-1", map[string]interface{}{
		"profileCookie": cookieID,
		"claims":        map[string]interface{}{"http://wso2.org/claims/email": "a@b.c"},
	}))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, map[string]any{"type": "profile", "id": master.ProfileID}, record["target"],
		"the update writes the master profile")
}

func TestSyncAuditRecordsAFailedProfileCreation(t *testing.T) {
	env := newSyncEnv(t)

	recorder, record := env.syncAudited(t, env.event(constants.UpdateUserClaimsEvent, "u-new",
		map[string]interface{}{"claims": map[string]interface{}{"http://wso2.org/claims/email": 12345}}))

	assert.GreaterOrEqual(t, recorder.Code, http.StatusBadRequest, recorder.Body.String())
	outcome := record["outcome"].(map[string]any)
	assert.Equal(t, "failed", outcome["result"])
}

// addPhoneAttribute adds a phone identity attribute, which the identity provider does not send.
func (e *profileHandlerEnv) addPhoneAttribute(t *testing.T) {
	t.Helper()
	require.NoError(t, schemaStore.AddProfileSchemaAttributesForScope(context.Background(),
		[]schemaModel.ProfileSchemaAttribute{{
			AttributeID:   "attr-phone",
			AttributeName: "identity_attributes.phone",
			Scope:         constants.IdentityAttributes,
			ValueType:     constants.StringDataType,
			MergeStrategy: "overwrite",
			Mutability:    constants.MutabilityReadWrite,
		}}, constants.IdentityAttributes, e.orgID))
}

// createAnonymousWithIdentity creates an anonymous profile with identity attributes a client set.
func (e *profileHandlerEnv) createAnonymousWithIdentity(t *testing.T) *model.ProfileResponse {
	t.Helper()
	created, err := e.svc.CreateProfile(e.ctx, model.ProfileRequest{
		IdentityAttributes: map[string]interface{}{"email": "anon@b.c", "phone": "111"},
		Traits:             map[string]interface{}{"city": "Colombo"},
	}, e.orgID)
	require.NoError(t, err)
	return created
}

func TestAuthenticationSuccessKeepsOnlyTheAnonymousValuesTheUserProfileLacks(t *testing.T) {
	env := newSyncEnv(t)
	env.sync(t, adminAuthorization(), env.event(constants.AddUserEvent, "u-1", emailClaim("idp@b.c")))
	require.NotNil(t, env.findUser(t, "u-1"))
	env.addPhoneAttribute(t)
	anonymous := env.createAnonymousWithIdentity(t)
	cookieID := env.createCookie(t, anonymous.ProfileID, true)

	recorder := env.sync(t, adminAuthorization(), env.event(constants.AuthenticationSuccess, "u-1",
		map[string]interface{}{"profileCookie": cookieID}))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	stitched, err := env.svc.GetProfile(env.ctx, anonymous.ProfileID, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "u-1", stitched.UserID)
	assert.Equal(t, map[string]interface{}{"phone": "111"}, stitched.IdentityAttributes,
		"the email of the identity provider is not contested, the phone it lacks is kept")
	assert.Equal(t, "Colombo", stitched.Traits["city"])
}

func TestLinkProfileKeepsOnlyTheAnonymousValuesTheUserProfileLacks(t *testing.T) {
	env := newSyncEnv(t)
	env.sync(t, adminAuthorization(), env.event(constants.AddUserEvent, "u-1", emailClaim("idp@b.c")))
	env.addPhoneAttribute(t)
	anonymous := env.createAnonymousWithIdentity(t)
	alone := env.createAnonymousWithIdentity(t)
	link := func(profileID, userID string) {
		recorder := env.call(t, env.h.LinkProfile, http.MethodPost, env.auth.ValidToken, "",
			`{"profile_id":"`+profileID+`","user_id":"`+userID+`"}`, nil)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	}

	link(anonymous.ProfileID, "u-1")
	link(alone.ProfileID, "u-2")

	stored, err := env.svc.GetProfile(env.ctx, anonymous.ProfileID, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{"phone": "111"}, stored.IdentityAttributes)
	storedAlone, err := env.svc.GetProfile(env.ctx, alone.ProfileID, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{"email": "anon@b.c", "phone": "111"}, storedAlone.IdentityAttributes,
		"a user with no profile has no values to keep, so the anonymous ones stay")
}
