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

package client

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

// introspectingServer issues token-1, token-2, ... for acme, and its acme
// introspection endpoint accepts only the bearer tokens in accepted.
type introspectingServer struct {
	*identityServer
	issued      int
	accepted    map[string]bool
	scopes      []string
	authHeaders []string
}

func newIntrospectingServer(t *testing.T) (*introspectingServer, *IdentityClient) {
	t.Helper()
	is, c := newIdentityServer(t, false)
	s := &introspectingServer{identityServer: is, accepted: map[string]bool{}}
	introspectionTokens.Lock()
	introspectionTokens.tokens = map[string]cachedToken{}
	introspectionTokens.Unlock()
	t.Cleanup(func() { now = time.Now })

	is.handle("/t/acme/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		s.scopes = append(s.scopes, r.PostForm.Get("scope"))
		s.issued++
		writeJSON(w, http.StatusOK, map[string]interface{}{"access_token": fmt.Sprintf("token-%d", s.issued),
			"expires_in": 3600})
	})
	is.handle("/t/acme/oauth2/introspect", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		s.authHeaders = append(s.authHeaders, auth)
		if len(auth) < 7 || !s.accepted[auth[7:]] {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"active": true})
	})
	return s, c
}

// The token is requested once and sent as a bearer token on each introspection.
func TestIntrospectTokenAsOrgUsesACachedBearerToken(t *testing.T) {
	s, c := newIntrospectingServer(t)
	s.accepted["token-1"] = true

	for i := 0; i < 3; i++ {
		claims, err := c.IntrospectTokenAsOrg("abc", "acme", "org-client", "org-secret")
		require.NoError(t, err)
		assert.Equal(t, true, claims["active"])
	}

	assert.Equal(t, 1, s.issued, "the token is requested once")
	assert.Equal(t, []string{introspectionScope}, s.scopes)
	assert.Equal(t, []string{"Bearer token-1", "Bearer token-1", "Bearer token-1"}, s.authHeaders)
}

// A token is not used once it is about to expire.
func TestIntrospectTokenAsOrgRenewsATokenBeforeItExpires(t *testing.T) {
	s, c := newIntrospectingServer(t)
	s.accepted["token-1"], s.accepted["token-2"] = true, true

	_, err := c.IntrospectTokenAsOrg("abc", "acme", "org-client", "org-secret")
	require.NoError(t, err)
	now = func() time.Time { return time.Now().Add(time.Hour - tokenExpirySkew/2) }
	_, err = c.IntrospectTokenAsOrg("abc", "acme", "org-client", "org-secret")
	require.NoError(t, err)

	assert.Equal(t, 2, s.issued)
	assert.Equal(t, []string{"Bearer token-1", "Bearer token-2"}, s.authHeaders)
}

// A refused cached token is replaced, and the introspection is sent once more.
func TestIntrospectTokenAsOrgRetriesOnceWithANewToken(t *testing.T) {
	s, c := newIntrospectingServer(t)
	s.accepted["token-1"] = true
	_, err := c.IntrospectTokenAsOrg("abc", "acme", "org-client", "org-secret")
	require.NoError(t, err)

	delete(s.accepted, "token-1")
	s.accepted["token-2"] = true
	claims, err := c.IntrospectTokenAsOrg("abc", "acme", "org-client", "org-secret")
	require.NoError(t, err)
	assert.Equal(t, true, claims["active"])
	assert.Equal(t, []string{"Bearer token-1", "Bearer token-1", "Bearer token-2"}, s.authHeaders)

	// The new token is cached.
	_, err = c.IntrospectTokenAsOrg("abc", "acme", "org-client", "org-secret")
	require.NoError(t, err)
	assert.Equal(t, 2, s.issued)
}

// When the new token is refused too, the error is returned.
func TestIntrospectTokenAsOrgReturnsTheErrorWhenTheNewTokenIsRefused(t *testing.T) {
	s, c := newIntrospectingServer(t)

	_, err := c.IntrospectTokenAsOrg("abc", "acme", "org-client", "org-secret")
	var clientErr *errors2.ClientError
	require.ErrorAs(t, err, &clientErr)
	assert.Equal(t, http.StatusUnauthorized, clientErr.StatusCode)
	assert.Equal(t, 2, s.issued)
	assert.Len(t, s.authHeaders, 2)
}

// Each client of each organization has its own token.
func TestIntrospectionTokensAreKeptForEachClient(t *testing.T) {
	s, c := newIntrospectingServer(t)
	s.accepted["token-1"], s.accepted["token-2"] = true, true

	_, err := c.IntrospectTokenAsOrg("abc", "acme", "org-client", "org-secret")
	require.NoError(t, err)
	_, err = c.IntrospectTokenAsOrg("abc", "acme", "new-client", "new-secret")
	require.NoError(t, err)

	assert.Equal(t, 2, s.issued)
	assert.Equal(t, []string{"Bearer token-1", "Bearer token-2"}, s.authHeaders)
}

func TestUsableLifetime(t *testing.T) {
	assert.Equal(t, time.Hour-tokenExpirySkew, usableLifetime(3600))
	assert.Equal(t, defaultTokenLifetime-tokenExpirySkew, usableLifetime(0), "no expires_in")
	assert.Equal(t, 30*time.Second, usableLifetime(60), "a lifetime shorter than twice the skew is halved")
}

// With "client_secret_basic", the client ID and secret are sent, and no token is requested.
func TestIntrospectTokenAsOrgWithClientSecretBasic(t *testing.T) {
	is, c := newIdentityServer(t, false)
	cfg := config.GetCDSRuntime().Config
	cfg.IDP.ServiceCredentials.IntrospectionAuth = config.IntrospectionAuthClientSecretBasic
	config.OverrideCDSRuntime(cfg)
	var user, secret string
	is.handle("/t/acme/oauth2/introspect", func(w http.ResponseWriter, r *http.Request) {
		user, secret, _ = r.BasicAuth()
		writeJSON(w, http.StatusOK, map[string]interface{}{"active": true})
	})

	claims, err := c.IntrospectTokenAsOrg("abc", "acme", "org-client", "org-secret")

	require.NoError(t, err)
	assert.Equal(t, true, claims["active"])
	assert.Equal(t, "org-client", user)
	assert.Equal(t, "org-secret", secret)
	assert.Empty(t, is.tokenRequests, "no token is requested")
}
