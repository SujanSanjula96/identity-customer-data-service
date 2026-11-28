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
	"sync"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// introspectionScope is the scope that the introspection endpoint requires.
const introspectionScope = "internal_oauth2_introspect"

const (
	// tokenExpirySkew: a cached token is not used in the last part of its lifetime.
	tokenExpirySkew = 60 * time.Second
	// defaultTokenLifetime is used when the response has no expires_in.
	defaultTokenLifetime = 5 * time.Minute
)

// now is time.Now, replaced in tests.
var now = time.Now

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// introspectionTokens caches the introspection token of each organization and
// client. It is at package level because an IdentityClient is created for each
// request.
var introspectionTokens = struct {
	sync.Mutex
	tokens map[string]cachedToken
}{tokens: map[string]cachedToken{}}

func introspectionTokenKey(orgHandle, clientID string) string {
	return orgHandle + "\x00" + clientID
}

// introspectionToken returns a cached or new token of the client with the scope
// internal_oauth2_introspect, from the token endpoint of orgHandle.
func (c *IdentityClient) introspectionToken(orgHandle, clientID, clientSecret string) (string, error) {
	key := introspectionTokenKey(orgHandle, clientID)
	introspectionTokens.Lock()
	cached, found := introspectionTokens.tokens[key]
	introspectionTokens.Unlock()
	if found && now().Before(cached.expiresAt) {
		return cached.token, nil
	}

	authConfig := config.GetCDSRuntime().Config.AuthServer
	endpoint, err := c.buildTokenEndpoint(orgHandle, authConfig.TokenEndpoint)
	if err != nil {
		return "", err
	}
	token, expiresIn, err := c.requestTokenWithLifetime(endpoint, clientID, clientSecret,
		clientCredentialsForm(introspectionScope), orgHandle)
	if err != nil {
		return "", err
	}

	introspectionTokens.Lock()
	introspectionTokens.tokens[key] = cachedToken{token: token, expiresAt: now().Add(usableLifetime(expiresIn))}
	introspectionTokens.Unlock()
	log.GetLogger().Debug(fmt.Sprintf("Cached a new introspection token of the organization %s.", orgHandle))
	return token, nil
}

// usableLifetime is the lifetime less tokenExpirySkew, or half the lifetime when
// it is shorter than twice the skew.
func usableLifetime(expiresIn int64) time.Duration {
	lifetime := time.Duration(expiresIn) * time.Second
	if expiresIn <= 0 {
		lifetime = defaultTokenLifetime
	}
	if lifetime < 2*tokenExpirySkew {
		return lifetime / 2
	}
	return lifetime - tokenExpirySkew
}

// dropIntrospectionToken removes the cached token of the organization and client.
func dropIntrospectionToken(orgHandle, clientID string) {
	introspectionTokens.Lock()
	delete(introspectionTokens.tokens, introspectionTokenKey(orgHandle, clientID))
	introspectionTokens.Unlock()
}
