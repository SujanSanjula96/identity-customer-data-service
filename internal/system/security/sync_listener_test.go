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

package security

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// adminRequestOn is a sync request with the right admin credentials, on the sync listener or not.
func adminRequestOn(t *testing.T, syncListener bool) *http.Request {
	t.Helper()
	var received *http.Request
	handler := http.Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { received = r }))
	if syncListener {
		handler = SyncListener(handler)
	}
	request := httptest.NewRequest(http.MethodPost, "/t/acme/cds/api/v1/profiles/sync", nil)
	request.SetBasicAuth("sync", "secret")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	require.NotNil(t, received)
	return received
}

func TestAuthnWithAdminCredentialsByListener(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	credentials := config.AuthServerConfig{AdminUsername: "sync", AdminPassword: "secret"}

	for name, tc := range map[string]struct {
		separate     bool
		syncListener bool
		allowed      bool
	}{
		"one listener":                            {allowed: true},
		"a sync listener: on the sync listener":   {separate: true, syncListener: true, allowed: true},
		"a sync listener: on the public listener": {separate: true},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := config.Config{AuthServer: credentials}
			if tc.separate {
				cfg.Sync.Addr.Port = 8901
			}
			config.OverrideCDSRuntime(cfg)

			err := AuthnWithAdminCredentials(adminRequestOn(t, tc.syncListener))

			if tc.allowed {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			status, _ := statusOf(t, err)
			assert.Equal(t, http.StatusUnauthorized, status)
		})
	}
}

func TestOnSyncListener(t *testing.T) {
	assert.True(t, OnSyncListener(adminRequestOn(t, true)))
	assert.False(t, OnSyncListener(adminRequestOn(t, false)))
}
