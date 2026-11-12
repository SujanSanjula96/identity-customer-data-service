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

// Package setup holds the helpers that the tests share: the inbuilt test database, the test organizations
// and the stub identity server.
package setup

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// The scope that the tokens of an AuthStub carry. The operations that a test names are
// authorized by it, and nothing else is.
const (
	stubScope      = "stub_scope"
	stubOtherScope = "stub_other_scope"
)

// AuthStub is an identity server that introspects tokens, so a handler's authentication and
// authorization run unmodified in a test.
type AuthStub struct {
	// ValidToken is active and carries the scope of the operations the stub was installed for.
	ValidToken string
	// OtherToken is active but carries a scope that no operation accepts.
	OtherToken string

	// jwts holds the tokens that NewJWT issued. The stub reports them active.
	jwts *sync.Map
}

// NewJWT issues a token that the stub reports active. The token is a JWT, so a handler can read its
// claims, for example the subject. It carries the organization claims, an expiry, the audience of CDS
// and the scope of the stub unless claims say otherwise. The signature is not checked.
func (s AuthStub) NewJWT(t testing.TB, claims map[string]interface{}) string {
	t.Helper()
	merged := jwt.MapClaims{
		"exp":   time.Now().Add(time.Hour).Unix(),
		"aud":   "iam-cds",
		"scope": stubScope,
	}
	for name, value := range claims {
		merged[name] = value
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, merged).SignedString([]byte("stub-signing-key"))
	if err != nil {
		t.Fatalf("failed to issue a token: %v", err)
	}
	s.jwts.Store(token, true)
	return token
}

// InstallAuthStub points the runtime configuration at a stub introspection endpoint. The
// operations are the names the handlers pass to the authorization, for example
// "profile:view". The previous configuration is restored when the test ends.
func InstallAuthStub(t testing.TB, operations ...string) AuthStub {
	t.Helper()
	stub := AuthStub{ValidToken: "stub-valid-token", OtherToken: "stub-other-token", jwts: &sync.Map{}}

	introspection := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		token := r.PostForm.Get("token")
		scope := stubScope
		if token == stub.OtherToken {
			scope = stubOtherScope
		}
		_, issued := stub.jwts.Load(token)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			constants.ActiveClaim:   token == stub.ValidToken || token == stub.OtherToken || issued,
			constants.ClientIdClaim: constants.CONSOLE_APP,
			"scope":                 scope,
		})
	}))

	certDir := t.TempDir()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: introspection.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(certDir, "trust.pem"), certPEM, 0o600); err != nil {
		t.Fatalf("failed to write the trust store: %v", err)
	}
	endpoint, err := url.Parse(introspection.URL)
	if err != nil {
		t.Fatalf("failed to read the stub address: %v", err)
	}

	requiredScopes := make(map[string][]string, len(operations)*2)
	for _, operation := range operations {
		requiredScopes[operation] = []string{stubScope}
		requiredScopes["org:"+operation] = []string{stubScope}
	}

	previous := config.GetCDSRuntime().Config
	updated := previous
	updated.AuthServer = config.AuthServerConfig{
		Host:                  endpoint.Hostname(),
		Port:                  endpoint.Port(),
		IntrospectionEndPoint: "/oauth2/introspect",
		RequiredScopes:        requiredScopes,
	}
	updated.TLS = config.TLSConfig{CertDir: certDir, TrustStore: "trust.pem"}
	updated.OrganizationManagement.Enabled = true
	config.OverrideCDSRuntime(updated)

	t.Cleanup(func() {
		introspection.Close()
		config.OverrideCDSRuntime(previous)
	})
	return stub
}
