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

package authn

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// stubIS is an identity server that publishes a JWKS for the primary organization acme and
// introspects tokens. It counts the calls to each endpoint.
type stubIS struct {
	rsaKey *rsa.PrivateKey
	ecKey  *ecdsa.PrivateKey

	jwksCalls          atomic.Int32
	introspectionCalls atomic.Int32
	jwksPaths          sync.Map

	mu       sync.Mutex
	inactive map[string]bool
}

func (s *stubIS) revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inactive[token] = true
}

func newStubIS(t *testing.T, authServer config.AuthServerConfig) *stubIS {
	t.Helper()
	require.NoError(t, log.Init("ERROR"))
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	stub := &stubIS{rsaKey: newRSAKey(t), ecKey: ecKey, inactive: map[string]bool{}}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/oauth2/jwks") {
			stub.jwksCalls.Add(1)
			stub.jwksPaths.Store(r.URL.Path, true)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"keys": []map[string]string{
				rsaJWK(t, "rsa-key", "RS256", &stub.rsaKey.PublicKey),
				ecJWK("ec-key", &stub.ecKey.PublicKey),
			}})
			return
		}
		stub.introspectionCalls.Add(1)
		require.NoError(t, r.ParseForm())
		token := r.PostForm.Get("token")
		stub.mu.Lock()
		active := !stub.inactive[token]
		stub.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			constants.ActiveClaim:   active,
			constants.ClientIDClaim: constants.ConsoleApp,
		})
	}))
	t.Cleanup(server.Close)

	certDir := t.TempDir()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	require.NoError(t, os.WriteFile(filepath.Join(certDir, "is.pem"), certPEM, 0o600))
	host := strings.TrimPrefix(server.URL, "https://")
	authServer.Host = host
	authServer.IntrospectionEndPoint = "/oauth2/introspect"
	config.OverrideCDSRuntime(config.Config{
		AuthServer: authServer,
		TLS:        config.TLSConfig{CertDir: certDir, TrustStore: "is.pem"},
	})
	return stub
}

func acmeClaims() jwt.MapClaims {
	return jwt.MapClaims{
		constants.OrgHandleClaim: "acme",
		"sub":                    "user-1",
		"aud":                    "iam-cds",
		"exp":                    time.Now().Add(time.Hour).Unix(),
	}
}

func sign(t *testing.T, method jwt.SigningMethod, kid string, key interface{}, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}

func validate(token string) error {
	_, err := ValidateTokenForOrg(token, "acme", "acme", "")
	return err
}

func TestValidateTokenForOrg_checksTheSignatureWithCachedKeysAndIntrospectsEveryRequest(t *testing.T) {
	stub := newStubIS(t, config.AuthServerConfig{})
	token := sign(t, jwt.SigningMethodRS256, "rsa-key", stub.rsaKey, acmeClaims())

	for i := 0; i < 3; i++ {
		claims, err := ValidateTokenForOrg(token, "acme", "acme", "")
		require.NoError(t, err)
		assert.Equal(t, "user-1", claims["sub"])
	}

	assert.Equal(t, int32(1), stub.jwksCalls.Load(), "the keys are fetched once")
	assert.Equal(t, int32(3), stub.introspectionCalls.Load(), "every request is introspected")
	_, fetchedForAcme := stub.jwksPaths.Load("/t/acme/oauth2/jwks")
	assert.True(t, fetchedForAcme, "the keys come from the JWKS endpoint of the primary organization")
}

func TestValidateTokenForOrg_checksASubOrgTokenWithTheKeysOfTheRootOrg(t *testing.T) {
	stub := newStubIS(t, config.AuthServerConfig{})
	const subOrgID = "8f9e2c1a-0d4b-4c6e-9a7f-3b2d1e0c5a6b"
	claims := acmeClaims()
	delete(claims, constants.OrgHandleClaim)
	claims[constants.OrgIDClaim] = subOrgID
	token := sign(t, jwt.SigningMethodRS256, "rsa-key", stub.rsaKey, claims)

	_, err := ValidateTokenForOrg(token, "region1", "acme", subOrgID)

	require.NoError(t, err)
	_, fetchedForRoot := stub.jwksPaths.Load("/t/acme/oauth2/jwks")
	assert.True(t, fetchedForRoot, "a sub organization's token is checked with the keys of its root organization")
	stub.jwksPaths.Range(func(path, _ interface{}) bool {
		assert.Equal(t, "/t/acme/oauth2/jwks", path, "no JWKS endpoint of the sub organization is called")
		return true
	})
}

func TestValidateTokenForOrg_usesTheConfiguredJWKSEndpoint(t *testing.T) {
	stub := newStubIS(t, config.AuthServerConfig{JWKSEndpoint: "/custom/oauth2/jwks"})

	require.NoError(t, validate(sign(t, jwt.SigningMethodRS256, "rsa-key", stub.rsaKey, acmeClaims())))
	_, fetched := stub.jwksPaths.Load("/t/acme/custom/oauth2/jwks")
	assert.True(t, fetched)
}

func TestValidateTokenForOrg_acceptsAnECSignature(t *testing.T) {
	stub := newStubIS(t, config.AuthServerConfig{})
	require.NoError(t, validate(sign(t, jwt.SigningMethodES256, "ec-key", stub.ecKey, acmeClaims())))
}

func TestValidateTokenForOrg_refusesForgedTokensWithoutCallingIntrospection(t *testing.T) {
	stub := newStubIS(t, config.AuthServerConfig{})
	publicDER, err := x509.MarshalPKIXPublicKey(&stub.rsaKey.PublicKey)
	require.NoError(t, err)
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})

	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, acmeClaims()).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	tests := map[string]string{
		"signed by another key":           sign(t, jwt.SigningMethodRS256, "rsa-key", newRSAKey(t), acmeClaims()),
		"HS256 with the public key":       sign(t, jwt.SigningMethodHS256, "rsa-key", publicPEM, acmeClaims()),
		"HS256 with a secret":             sign(t, jwt.SigningMethodHS256, "rsa-key", []byte("secret"), acmeClaims()),
		"not signed":                      unsigned,
		"no key ID":                       sign(t, jwt.SigningMethodRS256, "", stub.rsaKey, acmeClaims()),
		"a key ID the server never wrote": sign(t, jwt.SigningMethodRS256, "unknown", stub.rsaKey, acmeClaims()),
		"an RSA key ID on an EC token":    sign(t, jwt.SigningMethodES256, "rsa-key", stub.ecKey, acmeClaims()),
	}
	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, validate(token))
		})
	}
	assert.Equal(t, int32(0), stub.introspectionCalls.Load(), "a forged token never reaches introspection")
}

func TestValidateTokenForOrg_checksTheClaimsOnEveryRequest(t *testing.T) {
	stub := newStubIS(t, config.AuthServerConfig{})
	token := sign(t, jwt.SigningMethodRS256, "rsa-key", stub.rsaKey, acmeClaims())
	require.NoError(t, validate(token))

	// The token does not get into another organization.
	_, err := ValidateTokenForOrg(token, "other", "other", "")
	assert.Error(t, err)

	expired := acmeClaims()
	expired["exp"] = time.Now().Add(-time.Second).Unix()
	assert.Error(t, validate(sign(t, jwt.SigningMethodRS256, "rsa-key", stub.rsaKey, expired)))

	noExpiry := acmeClaims()
	delete(noExpiry, "exp")
	assert.Error(t, validate(sign(t, jwt.SigningMethodRS256, "rsa-key", stub.rsaKey, noExpiry)))

	wrongAudience := acmeClaims()
	wrongAudience["aud"] = "other-api"
	assert.Error(t, validate(sign(t, jwt.SigningMethodRS256, "rsa-key", stub.rsaKey, wrongAudience)))
}

func TestValidateTokenForOrg_allowsClockSkewOnNotBefore(t *testing.T) {
	stub := newStubIS(t, config.AuthServerConfig{})

	withinSkew := acmeClaims()
	withinSkew["nbf"] = time.Now().Add(10 * time.Second).Unix()
	assert.NoError(t, validate(sign(t, jwt.SigningMethodRS256, "rsa-key", stub.rsaKey, withinSkew)))

	beyondSkew := acmeClaims()
	beyondSkew["nbf"] = time.Now().Add(2 * time.Minute).Unix()
	assert.Error(t, validate(sign(t, jwt.SigningMethodRS256, "rsa-key", stub.rsaKey, beyondSkew)))
}

func TestValidateTokenForOrg_refusesARevokedTokenOnTheNextRequest(t *testing.T) {
	stub := newStubIS(t, config.AuthServerConfig{})
	token := sign(t, jwt.SigningMethodRS256, "rsa-key", stub.rsaKey, acmeClaims())
	require.NoError(t, validate(token))

	stub.revoke(token)

	assert.Error(t, validate(token))
	assert.Equal(t, int32(2), stub.introspectionCalls.Load())
}

// introspectingOnlyIDP is the adapter of an identity provider that introspects tokens but does not
// publish its signing keys.
type introspectingOnlyIDP struct {
	idp.Adapter
	active bool
}

func (a introspectingOnlyIDP) IntrospectToken(_ context.Context, _, _, _ string) (map[string]interface{}, error) {
	return map[string]interface{}{constants.ActiveClaim: a.active}, nil
}

func TestValidateTokenForOrg_reliesOnIntrospectionWhenTheIdentityProviderPublishesNoKeys(t *testing.T) {
	stub := newStubIS(t, config.AuthServerConfig{})
	idp.SetAdapter(introspectingOnlyIDP{active: true})
	t.Cleanup(func() { idp.SetAdapter(nil) })

	// The signature cannot be checked, so a token signed by any key is read, and the identity
	// provider decides.
	token := sign(t, jwt.SigningMethodRS256, "any", newRSAKey(t), acmeClaims())
	require.NoError(t, validate(token))
	assert.Equal(t, int32(0), stub.jwksCalls.Load())

	idp.SetAdapter(introspectingOnlyIDP{active: false})
	assert.Error(t, validate(sign(t, jwt.SigningMethodRS256, "any", newRSAKey(t), acmeClaims())))
}

func TestValidateTokenForOrg_introspectsAnOpaqueTokenOnEveryRequest(t *testing.T) {
	stub := newStubIS(t, config.AuthServerConfig{})

	require.NoError(t, validate("opaque-token"))
	require.NoError(t, validate("opaque-token"))
	assert.Equal(t, int32(2), stub.introspectionCalls.Load())
	assert.Equal(t, int32(0), stub.jwksCalls.Load())
}

func TestIntrospectionClient_isBuiltOncePerConfiguration(t *testing.T) {
	initRuntime(t)

	first := introspectionClient()
	assert.Same(t, first, introspectionClient(), "a request reuses the client")

	config.OverrideCDSRuntime(config.Config{})
	assert.NotSame(t, first, introspectionClient(), "a new configuration gets a new client")
}
