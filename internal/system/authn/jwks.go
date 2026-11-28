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
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// errUnknownKey is the error for a token whose key ID is not in the keys of the
// JWKS endpoint, after they were fetched or while a fetch is not allowed yet.
var errUnknownKey = errors.New("no key in the JWKS has the key ID of the token")

// verificationKey is a public key from a JWKS document.
type verificationKey struct {
	key crypto.PublicKey
	// alg is the algorithm the key is published for. It is empty when the key
	// does not name one.
	alg string
}

// keySet holds the keys of one JWKS endpoint.
type keySet struct {
	keys map[string]verificationKey
	// fetchedAt is when the keys were last fetched. It is zero until a fetch
	// succeeds.
	fetchedAt time.Time
	// attemptedAt is when the last fetch started, whether it succeeded or not.
	attemptedAt time.Time
	// fetching is closed when the fetch in progress ends. It is nil when no fetch
	// is in progress.
	fetching chan struct{}
	// lastUsed is when a request last asked for a key of the endpoint. The sweep
	// removes a key set that nobody used for a while.
	lastUsed time.Time
}

// initialJWKSSweepThreshold is the number of key sets at which the cache first
// removes the key sets that nobody used for a while.
const initialJWKSSweepThreshold = 1024

// jwksCache holds the keys of each JWKS endpoint, so the signature of a token is
// checked without a call to the identity server. The keys are fetched again when
// they are older than the TTL, or when a token has a key ID that they do not
// have, at most once in the minimum refresh interval. Requests that need a fetch
// at the same time wait for one fetch. When a fetch fails, the keys fetched last
// are used.
//
// A request for any organization adds a key set, so a key set that nobody used
// for a while is removed when the cache grows past a threshold, the same way as
// cache.Cache does it.
type jwksCache struct {
	mu   sync.Mutex
	sets map[string]*keySet
	// sweepAt is the number of key sets that triggers a sweep when a key set is
	// added. It rises with the cache, so a large set of key sets in use is not
	// swept on every addition.
	sweepAt int
	// runtime is the configuration the keys were fetched under. The keys are
	// dropped when it is replaced.
	runtime    *config.CDSRuntime
	ttl        time.Duration
	minRefresh time.Duration
	now        func() time.Time
}

func newJWKSCache() *jwksCache {
	return &jwksCache{
		sets:       map[string]*keySet{},
		sweepAt:    initialJWKSSweepThreshold,
		ttl:        constants.JWKSCacheTTL,
		minRefresh: constants.JWKSMinRefreshInterval,
		now:        time.Now,
	}
}

// key returns the key with the key ID kid from the JWKS endpoint, for a token
// signed with the algorithm alg. fetch reads the endpoint when the keys must be
// fetched.
func (c *jwksCache) key(endpoint, kid, alg string, fetch func(endpoint string) ([]byte, error)) (crypto.PublicKey,
	error) {
	ttl, minRefresh := c.ttl, c.minRefresh
	for waited := false; ; waited = true {
		c.mu.Lock()
		c.dropIfConfigChanged()
		now := c.now()
		set, ok := c.sets[endpoint]
		if !ok {
			// lastUsed is set first, so the sweep below keeps the new key set.
			set = &keySet{lastUsed: now}
			c.sets[endpoint] = set
			if len(c.sets) >= c.sweepAt {
				c.sweepUnusedLocked(now)
			}
		}
		set.lastUsed = now
		found, hasKey := set.keys[kid]
		fresh := !set.fetchedAt.IsZero() && now.Sub(set.fetchedAt) < ttl
		if hasKey && fresh {
			c.mu.Unlock()
			return found.forAlgorithm(alg)
		}
		if set.fetching != nil {
			// Another request is fetching the keys. Use what it fetches.
			done := set.fetching
			c.mu.Unlock()
			<-done
			continue
		}
		throttled := !set.attemptedAt.IsZero() && now.Sub(set.attemptedAt) < minRefresh
		if waited || throttled {
			c.mu.Unlock()
			if hasKey {
				// The keys are old, but no newer ones can be fetched now.
				return found.forAlgorithm(alg)
			}
			return nil, errUnknownKey
		}

		done := make(chan struct{})
		set.fetching = done
		set.attemptedAt = now
		c.mu.Unlock()

		keys, err := fetchKeys(fetch, endpoint)

		c.mu.Lock()
		if err != nil {
			log.GetLogger().Warn(fmt.Sprintf("Failed to fetch the JWKS from %s. The keys fetched before stay "+
				"in use.", endpoint), log.Error(err))
		} else {
			set.keys = keys
			set.fetchedAt = c.now()
		}
		set.fetching = nil
		close(done)
		c.mu.Unlock()
	}
}

// sweepUnusedLocked removes the key sets that nobody used for longer than the
// TTL. A key set without keys, as for an organization whose keys could not be
// fetched, only holds back the next fetch, so it is removed once nobody used it
// for longer than the minimum refresh interval. A key set that is being fetched
// stays. The caller holds c.mu.
func (c *jwksCache) sweepUnusedLocked(now time.Time) {
	for endpoint, set := range c.sets {
		idleLimit := c.ttl
		if len(set.keys) == 0 {
			idleLimit = c.minRefresh
		}
		if set.fetching == nil && now.Sub(set.lastUsed) > idleLimit {
			delete(c.sets, endpoint)
		}
	}

	// If the sweep freed little, the key sets in use really are this many. Raise
	// the threshold, so the next sweep does not run on every addition.
	if next := len(c.sets) * 2; next > c.sweepAt {
		c.sweepAt = next
	}
}

// dropIfConfigChanged drops every key when the runtime configuration was
// replaced since they were fetched. The caller holds c.mu.
func (c *jwksCache) dropIfConfigChanged() {
	if current := config.GetCDSRuntime(); current != c.runtime {
		c.sets = map[string]*keySet{}
		c.sweepAt = initialJWKSSweepThreshold
		c.runtime = current
	}
}

func fetchKeys(fetch func(endpoint string) ([]byte, error), endpoint string) (map[string]verificationKey, error) {
	body, err := fetch(endpoint)
	if err != nil {
		return nil, err
	}
	return parseJWKS(body)
}

// forAlgorithm returns the key, unless it is published for an algorithm other
// than alg. The JWT library checks that the type of the key suits alg.
func (k verificationKey) forAlgorithm(alg string) (crypto.PublicKey, error) {
	if k.alg != "" && k.alg != alg {
		return nil, fmt.Errorf("the key is for the algorithm %s, but the token is signed with %s", k.alg, alg)
	}
	return k.key, nil
}

// jsonWebKey is one key of a JWKS document (RFC 7517), with the fields of the RSA
// and EC keys that sign tokens.
type jsonWebKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// parseJWKS returns the signing keys of a JWKS document by key ID. A key without
// a key ID, for another use, or of a type that cannot sign tokens is skipped. A
// document without one usable key is an error, so a bad response does not
// replace the keys fetched before.
func parseJWKS(body []byte) (map[string]verificationKey, error) {
	var document struct {
		Keys []jsonWebKey `json:"keys"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("failed to parse the JWKS: %w", err)
	}

	keys := make(map[string]verificationKey, len(document.Keys))
	for _, jwk := range document.Keys {
		if jwk.Kid == "" || (jwk.Use != "" && jwk.Use != "sig") {
			continue
		}
		var key crypto.PublicKey
		var err error
		switch jwk.Kty {
		case "RSA":
			key, err = rsaKey(jwk)
		case "EC":
			key, err = ecKey(jwk)
		default:
			continue
		}
		if err != nil {
			log.GetLogger().Debug(fmt.Sprintf("Skipping the JWKS key %s.", jwk.Kid), log.Error(err))
			continue
		}
		keys[jwk.Kid] = verificationKey{key: key, alg: jwk.Alg}
	}
	if len(keys) == 0 {
		return nil, errors.New("the JWKS has no usable signing key")
	}
	return keys, nil
}

func rsaKey(jwk jsonWebKey) (*rsa.PublicKey, error) {
	n, err := decodeSegment(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("invalid modulus: %w", err)
	}
	e, err := decodeSegment(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("invalid exponent: %w", err)
	}
	if len(n) == 0 || len(e) == 0 || len(e) > 4 {
		return nil, errors.New("invalid modulus or exponent")
	}
	exponent := new(big.Int).SetBytes(e)
	if exponent.Int64() < 3 || exponent.Bit(0) == 0 {
		return nil, errors.New("invalid exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exponent.Int64())}, nil
}

func ecKey(jwk jsonWebKey) (*ecdsa.PublicKey, error) {
	var curve elliptic.Curve
	switch jwk.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, fmt.Errorf("unsupported curve %q", jwk.Crv)
	}
	x, err := decodeSegment(jwk.X)
	if err != nil {
		return nil, fmt.Errorf("invalid x coordinate: %w", err)
	}
	y, err := decodeSegment(jwk.Y)
	if err != nil {
		return nil, fmt.Errorf("invalid y coordinate: %w", err)
	}
	size := (curve.Params().BitSize + 7) / 8
	if len(x) != size || len(y) != size {
		return nil, errors.New("the coordinates do not match the curve")
	}
	point := make([]byte, 0, 1+2*size)
	point = append(point, 4)
	point = append(point, x...)
	point = append(point, y...)
	// ParseUncompressedPublicKey refuses a point that is not on the curve.
	return ecdsa.ParseUncompressedPublicKey(curve, point)
}

// decodeSegment decodes a base64url value of a JWK, with or without padding.
func decodeSegment(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
}
