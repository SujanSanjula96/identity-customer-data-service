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

package setup

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// testSigningKeyID is the key ID of the key that signs the test tokens.
const testSigningKeyID = "cds-test-signing-key"

var (
	signingKeyOnce sync.Once
	signingKey     *rsa.PrivateKey
	signingKeyErr  error
)

func testSigningKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	signingKeyOnce.Do(func() {
		signingKey, signingKeyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	if signingKeyErr != nil {
		t.Fatalf("failed to generate the test signing key: %v", signingKeyErr)
	}
	return signingKey
}

// SignJWT returns the claims as a JWT signed with RS256 by the test signing key, whose public key
// ServeJWKS publishes.
func SignJWT(t testing.TB, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = testSigningKeyID
	signed, err := token.SignedString(testSigningKey(t))
	if err != nil {
		t.Fatalf("failed to sign a token: %v", err)
	}
	return signed
}

// IsJWKSRequest reports whether the request is for the JWKS endpoint of an organization.
func IsJWKSRequest(r *http.Request) bool {
	return r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/oauth2/jwks")
}

// ServeJWKS writes a JWKS document with the public key of the test signing key.
func ServeJWKS(t testing.TB, w http.ResponseWriter) {
	public := testSigningKey(t).PublicKey
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"keys": []map[string]string{{
			"kty": "RSA",
			"kid": testSigningKeyID,
			"use": "sig",
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(public.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(public.E)).Bytes()),
		}},
	})
}
