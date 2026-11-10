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

package service

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
	"github.com/wso2/identity-customer-data-service/internal/system/cache"
	"github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

func TestValidateCORSOriginsAcceptsWellFormedOrigins(t *testing.T) {

	origins := []string{
		"https://app.acme.com",
		"http://localhost:3000",
		"https://app.acme.com:8443",
	}

	got, err := validateCORSOrigins(origins)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, origins) {
		t.Errorf("got %v, want %v", got, origins)
	}
}

func TestValidateCORSOriginsNormalizesAndDeduplicates(t *testing.T) {

	got, err := validateCORSOrigins([]string{
		"HTTPS://App.Acme.COM",
		" https://app.acme.com ",
		"https://other.acme.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"https://app.acme.com", "https://other.acme.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestValidateCORSOriginsEmptyListIsAllowed(t *testing.T) {

	got, err := validateCORSOrigins([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want an empty list", got)
	}
}

func TestValidateCORSOriginsRejectsMalformedOrigins(t *testing.T) {

	cases := map[string]string{
		"wildcard":        "*",
		"trailing slash":  "https://app.acme.com/",
		"with path":       "https://app.acme.com/callback",
		"with query":      "https://app.acme.com?a=b",
		"with fragment":   "https://app.acme.com#x",
		"no scheme":       "app.acme.com",
		"bare host":       "//app.acme.com",
		"wrong scheme":    "ftp://app.acme.com",
		"empty":           "",
		"blank":           "   ",
		"credentials":     "https://user:pass@app.acme.com",
		"wildcard subdom": "https://*.acme.com",
	}

	for name, origin := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := validateCORSOrigins([]string{origin})
			if err == nil {
				t.Fatalf("validateCORSOrigins(%q) = %v, want an error", origin, got)
			}
			clientError, ok := err.(*errors.ClientError)
			if !ok {
				t.Fatalf("error is %T, want *errors.ClientError", err)
			}
			if clientError.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", clientError.StatusCode, http.StatusBadRequest)
			}
		})
	}
}

// One bad entry rejects the whole update, so a partially valid list is never stored.
func TestValidateCORSOriginsRejectsWholeListOnOneBadEntry(t *testing.T) {

	_, err := validateCORSOrigins([]string{"https://app.acme.com", "*"})
	if err == nil {
		t.Fatal("expected an error for a list containing a wildcard")
	}
}

// The origins are stored as one JSON array in a column of limited width, so a list that would
// not fit is refused with a client error rather than failing in the database.
func TestValidateCORSOriginsRejectsAListTooLongToStore(t *testing.T) {

	var origins []string
	for i := 0; i < 30; i++ {
		origins = append(origins, fmt.Sprintf("https://app-%02d.acme.com", i))
	}
	_, err := validateCORSOrigins(origins)
	if err == nil {
		t.Fatal("expected an error for a list too long to store")
	}
	if clientError, ok := err.(*errors.ClientError); !ok || clientError.StatusCode != http.StatusBadRequest {
		t.Errorf("error = %v, want a bad request client error", err)
	}

	// The same list cut down to fit is stored.
	if _, err := validateCORSOrigins(origins[:10]); err != nil {
		t.Errorf("unexpected error for a list that fits: %v", err)
	}
}

// countingLoader stands in for the database read, counting how often each organisation is read.
type countingLoader struct {
	reads   map[string]int
	origins map[string][]string
	err     error
}

func newCountingLoader(origins map[string][]string) *countingLoader {
	return &countingLoader{reads: map[string]int{}, origins: origins}
}

func (l *countingLoader) load(_ context.Context, orgId string) ([]string, error) {
	l.reads[orgId]++
	if l.err != nil {
		return nil, l.err
	}
	return l.origins[orgId], nil
}

func newTestCORSOriginsCache(t *testing.T, loader *countingLoader, ttl time.Duration) *corsOriginsCache {

	t.Helper()
	if err := log.Init("ERROR"); err != nil {
		t.Fatalf("failed to set up the logger: %v", err)
	}
	return &corsOriginsCache{load: loader.load, entries: cache.NewCache(ttl)}
}

// Each organisation is read once per TTL, including one that has configured no origins.
func TestCORSOriginsCacheReadsEachOrganisationOnce(t *testing.T) {

	loader := newCountingLoader(map[string][]string{"acme-id": {"https://app.acme.com"}})
	origins := newTestCORSOriginsCache(t, loader, time.Hour)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if got, err := origins.get(ctx, "acme-id"); err != nil ||
			!reflect.DeepEqual(got, []string{"https://app.acme.com"}) {
			t.Errorf("origins of acme = %v, %v, want those it configured", got, err)
		}
		if got, err := origins.get(ctx, "globex-id"); err != nil || got != nil {
			t.Errorf("origins of globex = %v, %v, want none", got, err)
		}
	}
	if loader.reads["acme-id"] != 1 || loader.reads["globex-id"] != 1 {
		t.Errorf("reads = %v, want one per organisation", loader.reads)
	}
}

func TestCORSOriginsCacheRereadsOnceExpired(t *testing.T) {

	loader := newCountingLoader(map[string][]string{})
	origins := newTestCORSOriginsCache(t, loader, 0)
	ctx := context.Background()

	_, _ = origins.get(ctx, "acme-id")
	loader.origins["acme-id"] = []string{"https://app.acme.com"}
	if got, _ := origins.get(ctx, "acme-id"); !reflect.DeepEqual(got, []string{"https://app.acme.com"}) {
		t.Errorf("origins of acme = %v, want those read once the entry expired", got)
	}
	if loader.reads["acme-id"] != 2 {
		t.Errorf("reads = %d, want 2", loader.reads["acme-id"])
	}
}

// An update drops the entry of the organisation and no other.
func TestCORSOriginsCacheRereadsAfterAnUpdate(t *testing.T) {

	loader := newCountingLoader(map[string][]string{})
	origins := newTestCORSOriginsCache(t, loader, time.Hour)
	ctx := context.Background()

	_, _ = origins.get(ctx, "acme-id")
	_, _ = origins.get(ctx, "globex-id")
	loader.origins["acme-id"] = []string{"https://app.acme.com"}
	origins.invalidate("acme-id")

	if got, _ := origins.get(ctx, "acme-id"); !reflect.DeepEqual(got, []string{"https://app.acme.com"}) {
		t.Errorf("origins of acme = %v, want the updated ones", got)
	}
	_, _ = origins.get(ctx, "globex-id")
	if loader.reads["acme-id"] != 2 || loader.reads["globex-id"] != 1 {
		t.Errorf("reads = %v, want acme read again and globex served from the cache", loader.reads)
	}
}

// A read that started before an update committed is not cached over the update.
func TestCORSOriginsCacheDoesNotCacheAReadOverlappingAnUpdate(t *testing.T) {

	loader := newCountingLoader(map[string][]string{})
	origins := newTestCORSOriginsCache(t, loader, time.Hour)
	origins.load = func(ctx context.Context, orgId string) ([]string, error) {
		read, err := loader.load(ctx, orgId)
		if loader.reads[orgId] == 1 {
			origins.invalidate(orgId)
		}
		return read, err
	}
	ctx := context.Background()

	_, _ = origins.get(ctx, "acme-id")
	_, _ = origins.get(ctx, "acme-id")
	if loader.reads["acme-id"] != 2 {
		t.Errorf("reads = %d, want the read that overlapped the update left uncached", loader.reads["acme-id"])
	}
}

// A failed read is returned to the caller and not cached, so the next request reads again.
func TestCORSOriginsCacheDoesNotCacheAFailedRead(t *testing.T) {

	loader := newCountingLoader(map[string][]string{"acme-id": {"https://app.acme.com"}})
	loader.err = fmt.Errorf("database unavailable")
	origins := newTestCORSOriginsCache(t, loader, time.Hour)
	ctx := context.Background()

	if _, err := origins.get(ctx, "acme-id"); err == nil {
		t.Fatal("expected the error of the read")
	}
	loader.err = nil
	if got, err := origins.get(ctx, "acme-id"); err != nil || !reflect.DeepEqual(got, []string{"https://app.acme.com"}) {
		t.Errorf("origins of acme = %v, %v, want those read once the database answers", got, err)
	}
}

// countingOrgLookup stands in for the organization lookup, counting how often each handle is read.
type countingOrgLookup struct {
	reads map[string]int
	orgs  map[string]string
	err   error
}

func (l *countingOrgLookup) lookup(_ context.Context, orgHandle string) (orgModel.Ref, bool, error) {
	l.reads[orgHandle]++
	if l.err != nil {
		return orgModel.Ref{}, false, l.err
	}
	orgId, found := l.orgs[orgHandle]
	return orgModel.Ref{ID: orgId, Handle: orgHandle}, found, nil
}

func newTestCORSOrgIdResolver(t *testing.T, lookup *countingOrgLookup) *corsOrgIdResolver {

	t.Helper()
	if err := log.Init("ERROR"); err != nil {
		t.Fatalf("failed to set up the logger: %v", err)
	}
	return &corsOrgIdResolver{lookup: lookup.lookup, unknown: cache.NewCache(time.Hour)}
}

// A handle CDS has no organization for, as an unauthenticated preflight may carry, is read once
// and then answered without a read.
func TestCORSOrgIdResolverRemembersUnknownHandles(t *testing.T) {

	lookup := &countingOrgLookup{reads: map[string]int{}, orgs: map[string]string{"acme": "acme-id"}}
	resolver := newTestCORSOrgIdResolver(t, lookup)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if orgId, found, err := resolver.resolve(ctx, "acme"); err != nil || !found || orgId != "acme-id" {
			t.Errorf("resolve(acme) = %q, %v, %v, want acme-id", orgId, found, err)
		}
		if _, found, err := resolver.resolve(ctx, "made-up"); err != nil || found {
			t.Errorf("resolve(made-up) found = %v, err = %v, want not found", found, err)
		}
	}
	if lookup.reads["made-up"] != 1 {
		t.Errorf("reads of made-up = %d, want 1", lookup.reads["made-up"])
	}
}

// An organization stored after its handle was found unknown is resolved once it is forgotten.
func TestCORSOrgIdResolverResolvesAForgottenHandle(t *testing.T) {

	lookup := &countingOrgLookup{reads: map[string]int{}, orgs: map[string]string{}}
	resolver := newTestCORSOrgIdResolver(t, lookup)
	ctx := context.Background()

	_, _, _ = resolver.resolve(ctx, "acme")
	lookup.orgs["acme"] = "acme-id"
	resolver.forget("acme")

	if orgId, found, err := resolver.resolve(ctx, "acme"); err != nil || !found || orgId != "acme-id" {
		t.Errorf("resolve(acme) = %q, %v, %v, want acme-id", orgId, found, err)
	}
}

// A failed lookup is returned and not taken to mean the handle is unknown.
func TestCORSOrgIdResolverDoesNotRememberAFailedLookup(t *testing.T) {

	lookup := &countingOrgLookup{reads: map[string]int{}, orgs: map[string]string{"acme": "acme-id"},
		err: fmt.Errorf("database unavailable")}
	resolver := newTestCORSOrgIdResolver(t, lookup)
	ctx := context.Background()

	if _, _, err := resolver.resolve(ctx, "acme"); err == nil {
		t.Fatal("expected the error of the lookup")
	}
	lookup.err = nil
	if orgId, found, err := resolver.resolve(ctx, "acme"); err != nil || !found || orgId != "acme-id" {
		t.Errorf("resolve(acme) = %q, %v, %v, want acme-id once the database answers", orgId, found, err)
	}
}
