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

// countingLoader stands in for the database read, counting how often the snapshot reads.
type countingLoader struct {
	reads   int
	origins map[string][]string
	err     error
}

func (l *countingLoader) load(context.Context) (map[string][]string, error) {
	l.reads++
	return l.origins, l.err
}

func TestCORSOriginsSnapshotReadsOnceForAnyNumberOfOrganisations(t *testing.T) {

	loader := &countingLoader{origins: map[string][]string{"acme": {"https://app.acme.com"}}}
	snapshot := &corsOriginsSnapshot{load: loader.load, interval: time.Hour}
	ctx := context.Background()

	if got := snapshot.get(ctx, "acme"); !reflect.DeepEqual(got, []string{"https://app.acme.com"}) {
		t.Errorf("origins of acme = %v, want those it configured", got)
	}
	// Handles that are not organisations, as an unauthenticated caller may send, are
	// answered from the snapshot without a read each.
	for i := 0; i < 100; i++ {
		if got := snapshot.get(ctx, fmt.Sprintf("made-up-%d", i)); got != nil {
			t.Fatalf("origins of an unknown organisation = %v, want none", got)
		}
	}
	if loader.reads != 1 {
		t.Errorf("reads = %d, want 1", loader.reads)
	}
}

func TestCORSOriginsSnapshotReloadsOnceStale(t *testing.T) {

	loader := &countingLoader{origins: map[string][]string{}}
	snapshot := &corsOriginsSnapshot{load: loader.load, interval: 0}
	ctx := context.Background()

	snapshot.get(ctx, "acme")
	loader.origins = map[string][]string{"acme": {"https://app.acme.com"}}
	if got := snapshot.get(ctx, "acme"); !reflect.DeepEqual(got, []string{"https://app.acme.com"}) {
		t.Errorf("origins of acme = %v, want those loaded by the reload", got)
	}
	if loader.reads != 2 {
		t.Errorf("reads = %d, want 2", loader.reads)
	}
}

func TestCORSOriginsSnapshotReloadsAfterAnUpdate(t *testing.T) {

	loader := &countingLoader{origins: map[string][]string{}}
	snapshot := &corsOriginsSnapshot{load: loader.load, interval: time.Hour}
	ctx := context.Background()

	snapshot.get(ctx, "acme")
	loader.origins = map[string][]string{"acme": {"https://app.acme.com"}}
	snapshot.invalidate()
	if got := snapshot.get(ctx, "acme"); !reflect.DeepEqual(got, []string{"https://app.acme.com"}) {
		t.Errorf("origins of acme = %v, want the updated ones", got)
	}
	if loader.reads != 2 {
		t.Errorf("reads = %d, want 2", loader.reads)
	}
}

func TestCORSOriginsSnapshotKeepsPreviousOriginsWhenAReloadFails(t *testing.T) {

	if err := log.Init("ERROR"); err != nil {
		t.Fatalf("failed to set up the logger: %v", err)
	}
	loader := &countingLoader{origins: map[string][]string{"acme": {"https://app.acme.com"}}}
	snapshot := &corsOriginsSnapshot{load: loader.load, interval: time.Hour}
	ctx := context.Background()

	snapshot.get(ctx, "acme")
	loader.origins, loader.err = nil, fmt.Errorf("database unavailable")
	snapshot.invalidate()
	for i := 0; i < 3; i++ {
		if got := snapshot.get(ctx, "acme"); !reflect.DeepEqual(got, []string{"https://app.acme.com"}) {
			t.Errorf("origins of acme = %v, want the previously loaded ones", got)
		}
	}
	// The failed reload counts as one, so the database is not read again on every request.
	if loader.reads != 2 {
		t.Errorf("reads = %d, want 2", loader.reads)
	}
}

func TestCORSOriginsSnapshotWithFailedFirstLoadHasNoOrigins(t *testing.T) {

	if err := log.Init("ERROR"); err != nil {
		t.Fatalf("failed to set up the logger: %v", err)
	}
	loader := &countingLoader{err: fmt.Errorf("database unavailable")}
	snapshot := &corsOriginsSnapshot{load: loader.load, interval: time.Hour}

	if got := snapshot.get(context.Background(), "acme"); got != nil {
		t.Errorf("origins of acme = %v, want none, so the deployment-wide origins apply", got)
	}
}
