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

package authn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

const testEndpoint = "https://is.example/t/acme/oauth2/jwks"

func rsaJWK(t *testing.T, kid, alg string, key *rsa.PublicKey) map[string]string {
	t.Helper()
	jwk := map[string]string{
		"kty": "RSA",
		"kid": kid,
		"use": "sig",
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
	if alg != "" {
		jwk["alg"] = alg
	}
	return jwk
}

func ecJWK(kid string, key *ecdsa.PublicKey) map[string]string {
	point, _ := key.Bytes()
	size := (key.Curve.Params().BitSize + 7) / 8
	return map[string]string{
		"kty": "EC",
		"kid": kid,
		"crv": key.Curve.Params().Name,
		"x":   base64.RawURLEncoding.EncodeToString(point[1 : 1+size]),
		"y":   base64.RawURLEncoding.EncodeToString(point[1+size:]),
	}
}

func jwksDocument(t *testing.T, keys ...map[string]string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{"keys": keys})
	require.NoError(t, err)
	return body
}

func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

// initRuntime installs an empty runtime configuration, which the key cache reads to know when to
// drop the keys it holds.
func initRuntime(t *testing.T) {
	t.Helper()
	require.NoError(t, log.Init("ERROR"))
	config.OverrideCDSRuntime(config.Config{})
}

// fakeClock is a clock that a test moves by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestParseJWKS_readsRSAAndECKeysAndSkipsTheRest(t *testing.T) {
	initRuntime(t)
	rsaKey := newRSAKey(t)
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	encryption := rsaJWK(t, "enc", "", &rsaKey.PublicKey)
	encryption["use"] = "enc"
	offCurve := ecJWK("off-curve", &ecKey.PublicKey)
	offCurve["y"] = offCurve["x"]

	keys, err := parseJWKS(jwksDocument(t,
		rsaJWK(t, "rsa", "RS256", &rsaKey.PublicKey),
		ecJWK("ec", &ecKey.PublicKey),
		rsaJWK(t, "", "", &rsaKey.PublicKey),
		encryption,
		offCurve,
		map[string]string{"kty": "oct", "kid": "secret", "k": "c2VjcmV0"},
	))

	require.NoError(t, err)
	assert.Len(t, keys, 2)
	assert.Equal(t, &rsaKey.PublicKey, keys["rsa"].key)
	assert.Equal(t, "RS256", keys["rsa"].alg)
	assert.True(t, ecKey.PublicKey.Equal(keys["ec"].key))
}

func TestParseJWKS_refusesADocumentWithoutAUsableKey(t *testing.T) {
	initRuntime(t)
	_, err := parseJWKS([]byte(`{"keys":[{"kty":"oct","kid":"a","k":"c2VjcmV0"}]}`))
	assert.Error(t, err)

	_, err = parseJWKS([]byte(`not json`))
	assert.Error(t, err)
}

// countingFetcher serves a JWKS document that a test can replace or fail, and counts the fetches.
type countingFetcher struct {
	mu    sync.Mutex
	body  []byte
	err   error
	count atomic.Int32
	// gate, when set, holds every fetch until it is closed.
	gate chan struct{}
}

func (f *countingFetcher) fetch(string) ([]byte, error) {
	f.count.Add(1)
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.body, f.err
}

func (f *countingFetcher) set(body []byte, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.body, f.err = body, err
}

// newTestCache returns a cache on the clock, bound to the current runtime configuration so that its
// first lookup does not reset what a test sets.
func newTestCache(clock *fakeClock) *jwksCache {
	cache := newJWKSCache()
	cache.now = clock.Now
	cache.runtime = config.GetCDSRuntime()
	return cache
}

func TestJWKSCache_usesCachedKeysUntilTheTTL(t *testing.T) {
	initRuntime(t)
	key := newRSAKey(t)
	fetcher := &countingFetcher{body: jwksDocument(t, rsaJWK(t, "k1", "", &key.PublicKey))}
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	cache := newTestCache(clock)

	for i := 0; i < 5; i++ {
		got, err := cache.key(testEndpoint, "k1", "RS256", fetcher.fetch)
		require.NoError(t, err)
		assert.Equal(t, &key.PublicKey, got)
	}
	assert.Equal(t, int32(1), fetcher.count.Load(), "the keys are fetched once")

	clock.Add(time.Hour)
	_, err := cache.key(testEndpoint, "k1", "RS256", fetcher.fetch)
	require.NoError(t, err)
	assert.Equal(t, int32(2), fetcher.count.Load(), "the keys are fetched again after the TTL")
}

func TestJWKSCache_unknownKeyIDFetchesAtOnceButAtMostOncePerInterval(t *testing.T) {
	initRuntime(t)
	oldKey, newKey := newRSAKey(t), newRSAKey(t)
	fetcher := &countingFetcher{body: jwksDocument(t, rsaJWK(t, "old", "", &oldKey.PublicKey))}
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	cache := newTestCache(clock)

	_, err := cache.key(testEndpoint, "old", "RS256", fetcher.fetch)
	require.NoError(t, err)

	// The identity server rotates its key. A token with the new key ID fetches the keys at once,
	// even though the cached keys are within the TTL.
	clock.Add(2 * time.Minute)
	fetcher.set(jwksDocument(t, rsaJWK(t, "new", "", &newKey.PublicKey)), nil)
	got, err := cache.key(testEndpoint, "new", "RS256", fetcher.fetch)
	require.NoError(t, err)
	assert.Equal(t, &newKey.PublicKey, got)
	assert.Equal(t, int32(2), fetcher.count.Load())

	// Tokens with key IDs that the identity server never published do not fetch again within
	// the interval.
	for i := 0; i < 5; i++ {
		_, err = cache.key(testEndpoint, "forged", "RS256", fetcher.fetch)
		assert.ErrorIs(t, err, errUnknownKey)
	}
	assert.Equal(t, int32(2), fetcher.count.Load())

	clock.Add(time.Minute)
	_, err = cache.key(testEndpoint, "forged", "RS256", fetcher.fetch)
	assert.ErrorIs(t, err, errUnknownKey)
	assert.Equal(t, int32(3), fetcher.count.Load(), "the interval has passed, so it fetches once more")
}

func TestJWKSCache_keepsTheLastKeysWhenAFetchFails(t *testing.T) {
	initRuntime(t)
	key := newRSAKey(t)
	fetcher := &countingFetcher{body: jwksDocument(t, rsaJWK(t, "k1", "", &key.PublicKey))}
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	cache := newTestCache(clock)

	_, err := cache.key(testEndpoint, "k1", "RS256", fetcher.fetch)
	require.NoError(t, err)

	clock.Add(2 * time.Hour)
	fetcher.set(nil, errors.New("identity server is down"))
	got, err := cache.key(testEndpoint, "k1", "RS256", fetcher.fetch)
	require.NoError(t, err)
	assert.Equal(t, &key.PublicKey, got)

	// A document with no usable key does not replace them either.
	clock.Add(2 * time.Minute)
	fetcher.set([]byte(`{"keys":[]}`), nil)
	got, err = cache.key(testEndpoint, "k1", "RS256", fetcher.fetch)
	require.NoError(t, err)
	assert.Equal(t, &key.PublicKey, got)
	assert.Equal(t, int32(3), fetcher.count.Load())
}

func TestJWKSCache_aFailedFirstFetchIsNotRetriedWithinTheInterval(t *testing.T) {
	initRuntime(t)
	fetcher := &countingFetcher{err: errors.New("identity server is down")}
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	cache := newTestCache(clock)

	for i := 0; i < 3; i++ {
		_, err := cache.key(testEndpoint, "k1", "RS256", fetcher.fetch)
		assert.ErrorIs(t, err, errUnknownKey)
	}
	assert.Equal(t, int32(1), fetcher.count.Load())
}

func TestJWKSCache_concurrentRequestsShareOneFetch(t *testing.T) {
	initRuntime(t)
	key := newRSAKey(t)
	fetcher := &countingFetcher{body: jwksDocument(t, rsaJWK(t, "k1", "", &key.PublicKey)),
		gate: make(chan struct{})}
	cache := newJWKSCache()

	const requests = 20
	var wg sync.WaitGroup
	errs := make(chan error, requests)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cache.key(testEndpoint, "k1", "RS256", fetcher.fetch)
			errs <- err
		}()
	}
	// Let the requests queue behind the first fetch before it answers.
	require.Eventually(t, func() bool { return fetcher.count.Load() == 1 }, time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	close(fetcher.gate)
	wg.Wait()
	close(errs)

	for err := range errs {
		assert.NoError(t, err)
	}
	assert.Equal(t, int32(1), fetcher.count.Load())
}

func TestJWKSCache_refusesAKeyPublishedForAnotherAlgorithm(t *testing.T) {
	initRuntime(t)
	key := newRSAKey(t)
	fetcher := &countingFetcher{body: jwksDocument(t, rsaJWK(t, "k1", "RS256", &key.PublicKey))}
	cache := newJWKSCache()

	_, err := cache.key(testEndpoint, "k1", "PS256", fetcher.fetch)
	assert.Error(t, err)
}

func TestJWKSCache_dropsTheKeysWhenTheConfigurationIsReplaced(t *testing.T) {
	initRuntime(t)
	key := newRSAKey(t)
	fetcher := &countingFetcher{body: jwksDocument(t, rsaJWK(t, "k1", "", &key.PublicKey))}
	cache := newJWKSCache()

	_, err := cache.key(testEndpoint, "k1", "RS256", fetcher.fetch)
	require.NoError(t, err)
	config.OverrideCDSRuntime(config.Config{})
	_, err = cache.key(testEndpoint, "k1", "RS256", fetcher.fetch)
	require.NoError(t, err)
	assert.Equal(t, int32(2), fetcher.count.Load())
}

func TestJWKSCache_sweepsKeySetsThatNobodyUsed(t *testing.T) {
	initRuntime(t)
	key := newRSAKey(t)
	good := &countingFetcher{body: jwksDocument(t, rsaJWK(t, "k1", "", &key.PublicKey))}
	failing := &countingFetcher{err: errors.New("no such organization")}
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	cache := newTestCache(clock)
	cache.sweepAt = 3

	_, err := cache.key("https://is/t/acme/oauth2/jwks", "k1", "RS256", good.fetch)
	require.NoError(t, err)
	_, err = cache.key("https://is/t/unknown/oauth2/jwks", "k1", "RS256", failing.fetch)
	require.ErrorIs(t, err, errUnknownKey)

	// After the minimum refresh interval, the key set without keys is swept when a key set is
	// added, and the one with keys stays until the TTL.
	clock.Add(2 * time.Minute)
	_, err = cache.key("https://is/t/other/oauth2/jwks", "k1", "RS256", good.fetch)
	require.NoError(t, err)
	assert.Contains(t, cache.sets, "https://is/t/acme/oauth2/jwks")
	assert.NotContains(t, cache.sets, "https://is/t/unknown/oauth2/jwks")
	assert.Contains(t, cache.sets, "https://is/t/other/oauth2/jwks", "the added key set is not swept")

	// A key set in use stays: acme is used again, other is not.
	clock.Add(30 * time.Minute)
	_, err = cache.key("https://is/t/acme/oauth2/jwks", "k1", "RS256", good.fetch)
	require.NoError(t, err)
	clock.Add(31 * time.Minute)
	// The first sweep left 2 key sets, so the threshold is now 4.
	assert.Equal(t, 4, cache.sweepAt)
	for _, org := range []string{"third", "fourth"} {
		_, err = cache.key("https://is/t/"+org+"/oauth2/jwks", "k1", "RS256", good.fetch)
		require.NoError(t, err)
	}
	assert.Contains(t, cache.sets, "https://is/t/acme/oauth2/jwks")
	assert.NotContains(t, cache.sets, "https://is/t/other/oauth2/jwks", "unused for longer than the TTL")
}

func TestJWKSCache_raisesTheSweepThresholdWhenEveryKeySetIsInUse(t *testing.T) {
	initRuntime(t)
	key := newRSAKey(t)
	fetcher := &countingFetcher{body: jwksDocument(t, rsaJWK(t, "k1", "", &key.PublicKey))}
	cache := newTestCache(&fakeClock{now: time.Unix(1_000_000, 0)})
	cache.sweepAt = 4

	for i := 0; i < 4; i++ {
		_, err := cache.key(fmt.Sprintf("https://is/t/org%d/oauth2/jwks", i), "k1", "RS256", fetcher.fetch)
		require.NoError(t, err)
	}
	assert.Len(t, cache.sets, 4, "nothing is swept while every key set is in use")
	assert.Equal(t, 8, cache.sweepAt)
}

func TestJWKSCache_staysBoundedUnderRequestsForUnknownOrganizations(t *testing.T) {
	initRuntime(t)
	failing := &countingFetcher{err: errors.New("no such organization")}
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	cache := newTestCache(clock)

	for i := 0; i < 10_000; i++ {
		clock.Add(time.Second)
		_, err := cache.key(fmt.Sprintf("https://is/t/random-%d/oauth2/jwks", i), "k1", "RS256", failing.fetch)
		require.ErrorIs(t, err, errUnknownKey)
	}
	assert.LessOrEqual(t, len(cache.sets), initialJWKSSweepThreshold)
	assert.Equal(t, initialJWKSSweepThreshold, cache.sweepAt)
}
