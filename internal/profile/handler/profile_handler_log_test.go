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
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// captureAppLog writes the application log at DEBUG level to a file until the test ends, and
// returns a function that reads the records written so far.
func captureAppLog(t *testing.T) func() []map[string]any {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cds.log")
	require.NoError(t, log.Configure(log.Options{
		Level:  "DEBUG",
		Output: log.OutputOptions{File: log.FileOutput(path), FileFormat: log.FormatJSON},
	}))
	t.Cleanup(func() { _ = log.Configure(log.Options{Output: log.OutputOptions{Console: true}}) })

	return func() []map[string]any {
		t.Helper()
		content, err := os.ReadFile(path) // #nosec G304 -- the test reads a file in its temporary directory.
		require.NoError(t, err)
		var records []map[string]any
		for _, line := range bytes.Split(bytes.TrimSpace(content), []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			var record map[string]any
			require.NoError(t, json.Unmarshal(line, &record), string(line))
			records = append(records, record)
		}
		return records
	}
}

// assertLogKeepsSecrets checks that no record has a profile cookie value, and that only DEBUG
// records have a user ID.
func assertLogKeepsSecrets(t *testing.T, records []map[string]any, cookies, userIDs []string) {
	t.Helper()
	require.NotEmpty(t, records, "the flow must write application records")
	for _, record := range records {
		line, err := json.Marshal(record)
		require.NoError(t, err)
		for _, cookie := range cookies {
			assert.NotContains(t, string(line), cookie, "a record has the profile cookie value")
		}
		if record["level"] == "DEBUG" {
			continue
		}
		for _, userID := range userIDs {
			assert.NotContains(t, string(line), userID, "a %v record has the user ID", record["level"])
		}
	}
}

func TestSyncEventsDoNotLogCookiesOrUserIDsAboveDebug(t *testing.T) {
	env := newSyncEnv(t)
	const (
		user      = "user-4c1e9f"
		otherUser = "user-82d07a"
		newUser   = "user-e60f13"
		stale     = "stale-cookie-5b3d61"
	)
	anonymous := env.createProfile(t, "Colombo")
	cookie := env.createCookie(t, anonymous.ProfileID, true)
	foreignProfile := env.createUserProfile(t, otherUser, "Kandy")
	foreign := env.createCookie(t, foreignProfile.ProfileID, true)
	read := captureAppLog(t)

	sync := func(event, userID string, extra map[string]interface{}) {
		t.Helper()
		recorder := env.sync(t, adminAuthorization(), env.event(event, userID, extra))
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	}
	// The cookie of the profile of another user: CDS refuses to link it and writes a WARN record.
	sync(constants.AddUserEvent, newUser, map[string]interface{}{
		"profileCookie": foreign,
		"claims":        map[string]interface{}{"http://wso2.org/claims/email": "new@example.com"},
	})
	sync(constants.AuthenticationSuccess, user, map[string]interface{}{"profileCookie": stale})
	sync(constants.AuthenticationSuccess, user, map[string]interface{}{"profileCookie": cookie})
	sync(constants.AuthenticationSuccess, user, map[string]interface{}{"profileCookie": foreign})
	sync(constants.UpdateUserClaimsEvent, otherUser, emailClaim("a@example.com"))
	sync(constants.SessionTermination, otherUser, map[string]interface{}{"profileCookie": cookie})
	sync(constants.SessionTermination, otherUser, map[string]interface{}{"profileCookie": stale})
	sync(constants.SessionTermination, otherUser, map[string]interface{}{"profileCookie": foreign})

	records := read()
	assertLogKeepsSecrets(t, records, []string{cookie, foreign, stale},
		[]string{user, otherUser, newUser})

	// The user ID stays available at DEBUG, in the user_id field.
	var debugUserIDs []any
	for _, record := range records {
		if record["level"] == "DEBUG" && record[log.KeyUserID] != nil {
			debugUserIDs = append(debugUserIDs, record[log.KeyUserID])
		}
	}
	assert.Contains(t, debugUserIDs, user)
}

func TestLinkProfileDoesNotLogTheUserIDAboveDebug(t *testing.T) {
	env := newProfileHandlerEnv(t)
	anonymous := env.createProfile(t, "Colombo")
	read := captureAppLog(t)

	const user = "user-a17be2"
	linked := env.call(t, env.h.LinkProfile, http.MethodPost, env.auth.ValidToken, "",
		fmt.Sprintf(`{"profile_id":%q,"user_id":%q}`, anonymous.ProfileID, user), nil)
	require.Equal(t, http.StatusOK, linked.Code, linked.Body.String())

	records := read()
	assertLogKeepsSecrets(t, records, nil, []string{user})
	var linkRecord map[string]any
	for _, record := range records {
		if strings.HasPrefix(fmt.Sprint(record["msg"]), "Linked the profile") {
			linkRecord = record
		}
	}
	require.NotNil(t, linkRecord, "the link writes an INFO record")
	assert.Equal(t, anonymous.ProfileID, linkRecord[log.KeyProfileID])
}
