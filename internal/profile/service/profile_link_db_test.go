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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

func linkRequest(profile *profileModel.ProfileResponse, userID string) profileModel.ProfileRequest {
	return profileModel.ProfileRequest{
		UserID:             userID,
		IdentityAttributes: profile.IdentityAttributes,
		Traits:             profile.Traits,
		ApplicationData:    WideAppDataMap(profile.ApplicationData),
	}
}

func TestLinkProfileLinksAProfileWithoutAUser(t *testing.T) {
	env := newProfileEnv(t)
	anonymous := env.createWithCity(t, "Colombo")

	linked, err := env.svc.LinkProfile(env.ctx, anonymous.ProfileID, env.orgID, linkRequest(anonymous, "u-1"))

	require.NoError(t, err)
	assert.Equal(t, "u-1", linked.UserID)
	assert.Equal(t, "Colombo", linked.Traits["city"])

	again, err := env.svc.LinkProfile(env.ctx, anonymous.ProfileID, env.orgID, linkRequest(anonymous, "u-1"))
	require.NoError(t, err)
	assert.Equal(t, "u-1", again.UserID)
}

func TestLinkProfileRefusesAProfileThatAnotherUserLinkedAfterTheRead(t *testing.T) {
	env := newProfileEnv(t)
	anonymous := env.createWithCity(t, "Colombo")
	readByFirst, err := env.svc.GetProfile(env.ctx, anonymous.ProfileID, env.orgID)
	require.NoError(t, err)
	readBySecond, err := env.svc.GetProfile(env.ctx, anonymous.ProfileID, env.orgID)
	require.NoError(t, err)
	require.Empty(t, readBySecond.UserID, "both requests see a profile without a user")

	_, err = env.svc.LinkProfile(env.ctx, anonymous.ProfileID, env.orgID, linkRequest(readByFirst, "u-1"))
	require.NoError(t, err)
	stale := linkRequest(readBySecond, "u-2")
	stale.Traits = map[string]interface{}{"city": "Kandy"}
	_, err = env.svc.LinkProfile(env.ctx, anonymous.ProfileID, env.orgID, stale)

	clientError := requireStatus(t, err, http.StatusConflict)
	assert.Equal(t, errors2.ProfileAlreadyLinked.Code, clientError.ErrorMessage.Code)
	stored, err := env.svc.GetProfile(env.ctx, anonymous.ProfileID, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "u-1", stored.UserID, "the first link keeps the profile")
	assert.Equal(t, "Colombo", stored.Traits["city"], "the refused link changes nothing")
}

func TestUpdateProfileCanStillChangeTheUserOfAProfile(t *testing.T) {
	env := newProfileEnv(t)
	owned := env.create(t, profileModel.ProfileRequest{UserID: "u-1",
		Traits: map[string]interface{}{"city": "Colombo"}})

	updated, err := env.svc.UpdateProfile(env.ctx, owned.ProfileID, env.orgID, linkRequest(owned, "u-2"))

	require.NoError(t, err)
	assert.Equal(t, "u-2", updated.UserID, "only a link checks the stored user")
}

func TestLinkProfileChecksTheUserOfTheMasterOfAMergedProfile(t *testing.T) {
	env := newProfileEnv(t)
	master := env.create(t, profileModel.ProfileRequest{UserID: "u-1",
		Traits: map[string]interface{}{"city": "Colombo"}})
	env.addChild(t, master.ProfileID, "child-1")
	child, err := env.svc.GetProfile(env.ctx, "child-1", env.orgID)
	require.NoError(t, err)

	_, err = env.svc.LinkProfile(env.ctx, "child-1", env.orgID, linkRequest(child, "u-2"))
	_ = requireStatus(t, err, http.StatusConflict)

	_, err = env.svc.LinkProfile(env.ctx, "child-1", env.orgID, linkRequest(child, "u-1"))
	require.NoError(t, err)
	stored, err := env.svc.GetProfile(env.ctx, master.ProfileID, env.orgID)
	require.NoError(t, err)
	assert.Equal(t, "u-1", stored.UserID)
}
