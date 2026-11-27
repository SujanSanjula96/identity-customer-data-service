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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// An application that is not a system application writes only its own application data, as it
// reads only its own. The stub token speaks for the Console, so the Console is the caller.

func (e *profileHandlerEnv) storedTheme(t *testing.T, profileID, appID string) interface{} {
	t.Helper()
	stored, err := e.svc.GetProfile(e.ctx, profileID, e.orgID)
	require.NoError(t, err)
	return stored.ApplicationData[appID]["theme"]
}

func TestPatchProfileRefusesTheDataOfAnotherApplication(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfileWithOtherAppData(t)

	patched := env.call(t, env.h.PatchProfile, http.MethodPatch, env.auth.ValidToken, "",
		`{"application_data":{"`+otherApp+`":{"theme":"light"}}}`, profileIDOf(created.ProfileID))
	assert.Equal(t, http.StatusForbidden, patched.Code, patched.Body.String())
	assert.Equal(t, "dark", env.storedTheme(t, created.ProfileID, otherApp), "the other app's data is unchanged")

	// Data of the caller's own application in the same patch does not make it allowed.
	env.addThemeSchema(t, constants.ConsoleApp)
	mixed := env.call(t, env.h.PatchProfile, http.MethodPatch, env.auth.ValidToken, "",
		`{"application_data":{"`+constants.ConsoleApp+`":{"theme":"light"},"`+otherApp+`":{"theme":"light"}}}`,
		profileIDOf(created.ProfileID))
	assert.Equal(t, http.StatusForbidden, mixed.Code, mixed.Body.String())
	assert.Equal(t, "dark", env.storedTheme(t, created.ProfileID, otherApp))
	assert.Nil(t, env.storedTheme(t, created.ProfileID, constants.ConsoleApp), "a refused patch writes nothing")
}

func TestPatchProfileWritesTheCallersOwnApplicationData(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfileWithOtherAppData(t)
	env.addThemeSchema(t, constants.ConsoleApp)

	// The response is filtered by consent as GetProfile filters it, so the stored profile tells
	// what was written.
	patched := env.call(t, env.h.PatchProfile, http.MethodPatch, env.auth.ValidToken, "",
		`{"application_data":{"`+constants.ConsoleApp+`":{"theme":"light"}}}`, profileIDOf(created.ProfileID))
	require.Equal(t, http.StatusOK, patched.Code, patched.Body.String())
	assert.Equal(t, "light", env.storedTheme(t, created.ProfileID, constants.ConsoleApp))
	assert.Equal(t, "dark", env.storedTheme(t, created.ProfileID, otherApp), "the other app's data is kept")

	traitsOnly := env.call(t, env.h.PatchProfile, http.MethodPatch, env.auth.ValidToken, "",
		`{"traits":{"city":"Kandy"}}`, profileIDOf(created.ProfileID))
	assert.Equal(t, http.StatusOK, traitsOnly.Code, "a patch without application data needs no check")
}

func TestPatchProfileLetsASystemApplicationWriteTheDataOfAnyApplication(t *testing.T) {
	env := newProfileHandlerEnv(t)
	env.markConsoleSystemApp(t)
	created := env.createProfileWithOtherAppData(t)

	patched := env.call(t, env.h.PatchProfile, http.MethodPatch, env.auth.ValidToken, "",
		`{"application_data":{"`+otherApp+`":{"theme":"light"}}}`, profileIDOf(created.ProfileID))
	require.Equal(t, http.StatusOK, patched.Code, patched.Body.String())
	assert.Equal(t, "light", env.storedTheme(t, created.ProfileID, otherApp))
}

func TestInitProfileRefusesTheDataOfAnotherApplication(t *testing.T) {
	env := newProfileHandlerEnv(t)
	env.addThemeSchema(t, otherApp)

	created := env.call(t, env.h.InitProfile, http.MethodPost, env.auth.ValidToken, "",
		`{"application_data":{"`+otherApp+`":{"theme":"dark"}}}`, nil)
	assert.Equal(t, http.StatusForbidden, created.Code, created.Body.String())
	assert.Empty(t, created.Result().Cookies(), "no profile and no cookie is created")

	env.addThemeSchema(t, constants.ConsoleApp)
	own := env.call(t, env.h.InitProfile, http.MethodPost, env.auth.ValidToken, "",
		`{"application_data":{"`+constants.ConsoleApp+`":{"theme":"dark"}}}`, nil)
	require.Equal(t, http.StatusCreated, own.Code, own.Body.String())
	assert.Equal(t, "dark", env.storedTheme(t, decodeProfile(t, own).ProfileID, constants.ConsoleApp))
}

func TestUpdateProfileRefusesTheDataOfAnotherApplication(t *testing.T) {
	env := newProfileHandlerEnv(t)
	created := env.createProfileWithOtherAppData(t)

	updated := env.call(t, env.h.UpdateProfile, http.MethodPut, env.auth.ValidToken, "",
		`{"traits":{"city":"Kandy"},"application_data":{"`+otherApp+`":{"theme":"light"}}}`,
		profileIDOf(created.ProfileID))
	assert.Equal(t, http.StatusForbidden, updated.Code, updated.Body.String())
	assert.Equal(t, "dark", env.storedTheme(t, created.ProfileID, otherApp))
}
