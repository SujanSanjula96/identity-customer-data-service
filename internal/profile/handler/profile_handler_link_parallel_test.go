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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

const linkRounds = 20

func TestParallelLinksGiveAProfileToOneUser(t *testing.T) {
	env := newProfileHandlerEnv(t)
	const callers = 8

	for round := 0; round < linkRounds; round++ {
		anonymous := env.createProfile(t, "Colombo")

		codes := make([]int, callers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				codes[i] = env.call(t, env.h.LinkProfile, http.MethodPost, env.auth.ValidToken, "",
					fmt.Sprintf(`{"profile_id":%q,"user_id":"u-%d"}`, anonymous.ProfileID, i), nil).Code
			}(i)
		}
		close(start)
		wg.Wait()

		var winners []string
		for i, code := range codes {
			switch code {
			case http.StatusOK:
				winners = append(winners, fmt.Sprintf("u-%d", i))
			case http.StatusConflict:
			default:
				t.Fatalf("round %d: link of u-%d returned %d", round, i, code)
			}
		}
		stored, err := env.svc.GetProfile(env.ctx, anonymous.ProfileID, env.orgID)
		require.NoError(t, err)
		require.Lenf(t, winners, 1, "round %d: users %v were all told that they own the profile, "+
			"and the profile is stored for %s", round, winners, stored.UserID)
		assert.Equal(t, winners[0], stored.UserID, "round %d", round)
	}
}

func TestLinkAndAuthenticationSuccessGiveAProfileToOneUser(t *testing.T) {
	env := newSyncEnv(t)

	// The link takes longer than the event to read the profile, so delay the event to overlap the calls.
	for round := 0; round < 2*linkRounds; round++ {
		eventDelay := time.Duration(round%linkRounds) * 100 * time.Microsecond
		anonymous := env.createProfile(t, "Colombo")
		cookieID := env.createCookie(t, anonymous.ProfileID, true)
		linkBody := fmt.Sprintf(`{"profile_id":%q,"user_id":"u-link"}`, anonymous.ProfileID)
		eventBody, err := json.Marshal(env.event(constants.AuthenticationSuccess, "u-sync",
			map[string]interface{}{"profileCookie": cookieID}))
		require.NoError(t, err)

		var linked, synced *httptest.ResponseRecorder
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			linked = env.call(t, env.h.LinkProfile, http.MethodPost, env.auth.ValidToken, "", linkBody, nil)
		}()
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(eventDelay)
			synced = env.syncRaw(t, adminAuthorization(), string(eventBody))
		}()
		close(start)
		wg.Wait()

		require.Equal(t, http.StatusOK, synced.Code, synced.Body.String())
		stored, err := env.svc.GetProfile(env.ctx, anonymous.ProfileID, env.orgID)
		require.NoError(t, err)
		switch linked.Code {
		case http.StatusOK:
			require.Equalf(t, "u-link", stored.UserID,
				"round %d: the link succeeded, but the profile is stored for %s", round, stored.UserID)
		case http.StatusConflict:
			require.Equal(t, "u-sync", stored.UserID, "round %d", round)
		default:
			t.Fatalf("round %d: link returned %d: %s", round, linked.Code, linked.Body.String())
		}
	}
}

func TestLinkAndAddUserGiveAProfileToOneUser(t *testing.T) {
	env := newSyncEnv(t)

	for round := 0; round < 2*linkRounds; round++ {
		eventDelay := time.Duration(round%linkRounds) * 100 * time.Microsecond
		anonymous := env.createProfile(t, "Colombo")
		cookieID := env.createCookie(t, anonymous.ProfileID, true)
		newUser := fmt.Sprintf("u-add-%d", round)
		linkBody := fmt.Sprintf(`{"profile_id":%q,"user_id":"u-link-%d"}`, anonymous.ProfileID, round)
		eventBody, err := json.Marshal(env.event(constants.AddUserEvent, newUser,
			map[string]interface{}{"profileCookie": cookieID}))
		require.NoError(t, err)

		var linked, synced *httptest.ResponseRecorder
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			linked = env.call(t, env.h.LinkProfile, http.MethodPost, env.auth.ValidToken, "", linkBody, nil)
		}()
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(eventDelay)
			synced = env.syncRaw(t, adminAuthorization(), string(eventBody))
		}()
		close(start)
		wg.Wait()

		require.Equal(t, http.StatusOK, synced.Code, synced.Body.String())
		stored, err := env.svc.GetProfile(env.ctx, anonymous.ProfileID, env.orgID)
		require.NoError(t, err)
		switch linked.Code {
		case http.StatusOK:
			require.Equalf(t, fmt.Sprintf("u-link-%d", round), stored.UserID,
				"round %d: the link succeeded, but the profile is stored for %s", round, stored.UserID)
			own := env.findUser(t, newUser)
			require.NotNilf(t, own, "round %d: the new user has no profile", round)
			assert.NotEqual(t, anonymous.ProfileID, own.ProfileID, "round %d", round)
		case http.StatusConflict:
			require.Equal(t, newUser, stored.UserID, "round %d", round)
		default:
			t.Fatalf("round %d: link returned %d: %s", round, linked.Code, linked.Body.String())
		}
	}
}
