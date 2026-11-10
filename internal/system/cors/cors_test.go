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

package cors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractOrgFromPath(t *testing.T) {

	cases := []struct {
		path string
		want Org
	}{
		{"/t/carbon.super/cds/api/v1/profiles", Org{Handle: "carbon.super"}},
		{"/t/acme/o/4b1e/cds/api/v1/profiles", Org{Handle: "acme", SubOrgID: "4b1e"}},
		{"/t/acme/o/4b1e", Org{Handle: "acme", SubOrgID: "4b1e"}},
		{"/t/acme/o/", Org{Handle: "acme"}},
		{"/t/acme/cds/api/v1/o/4b1e", Org{Handle: "acme"}},
		{"/o/4b1e/cds/api/v1/profiles", Org{}},
		{"/t/acme", Org{Handle: "acme"}},
		{"/t/acme/", Org{Handle: "acme"}},
		{"/health", Org{}},
		{"/", Org{}},
		{"/t/", Org{}},
		{"/other/acme/api", Org{}},
	}

	for _, c := range cases {
		if got := ExtractOrgFromPath(c.path); got != c.want {
			t.Errorf("ExtractOrgFromPath(%q) = %+v, want %+v", c.path, got, c.want)
		}
	}
}

// A sub organisation path reaches the lookup with the ID of the sub organisation, so that the
// lookup decides which origins apply to it.
func TestSubOrgPathPassesTheSubOrgToTheLookup(t *testing.T) {

	var got Org
	lookup := func(_ context.Context, org Org) []string {
		got = org
		return []string{"https://app.acme.com"}
	}
	h, _ := newTestHandler(lookup, nil)

	w := doRequest(h, http.MethodGet, "/t/acme/o/4b1e/cds/api/v1/profiles", "https://app.acme.com")
	if want := (Org{Handle: "acme", SubOrgID: "4b1e"}); got != want {
		t.Errorf("lookup got %+v, want %+v", got, want)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.acme.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the origin the lookup returned", got)
	}
}

// newTestHandler builds the middleware over a handler that records whether it ran.
func newTestHandler(lookup OriginsLookup, deployment []string) (http.Handler, *bool) {

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	return Middleware(next, lookup, func() []string { return deployment }), &reached
}

func doRequest(h http.Handler, method, path, origin string) *httptest.ResponseRecorder {

	r := httptest.NewRequest(method, path, nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// An organisation's own origin is allowed on its own path.
func TestOrgOriginIsAllowedForThatOrg(t *testing.T) {

	lookup := func(_ context.Context, org Org) []string {
		if org.Handle == "acme" {
			return []string{"https://app.acme.com"}
		}
		return nil
	}
	h, _ := newTestHandler(lookup, nil)

	w := doRequest(h, http.MethodGet, "/t/acme/cds/api/v1/profiles", "https://app.acme.com")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.acme.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want the org origin", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want true", got)
	}
}

// The regression this change exists for: one organisation's origin must not be echoed
// back on another organisation's requests.
func TestOrgOriginIsNotAllowedForAnotherOrg(t *testing.T) {

	lookup := func(_ context.Context, org Org) []string {
		switch org.Handle {
		case "acme":
			return []string{"https://app.acme.com"}
		case "globex":
			return []string{"https://app.globex.com"}
		}
		return nil
	}
	h, _ := newTestHandler(lookup, nil)

	w := doRequest(h, http.MethodGet, "/t/globex/cds/api/v1/profiles", "https://app.acme.com")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty for a foreign org origin", got)
	}
}

// An organisation that has configured no origins keeps working on the deployment list, so
// existing deployments are unaffected.
func TestFallsBackToDeploymentOriginsWhenOrgHasNone(t *testing.T) {

	lookup := func(_ context.Context, _ Org) []string { return nil }
	h, _ := newTestHandler(lookup, []string{"https://console.example.com"})

	w := doRequest(h, http.MethodGet, "/t/acme/cds/api/v1/profiles", "https://console.example.com")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://console.example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want the deployment origin", got)
	}
}

// Once an organisation configures its own origins, the deployment list no longer applies
// to it.
func TestOrgOriginsReplaceDeploymentOrigins(t *testing.T) {

	lookup := func(_ context.Context, _ Org) []string { return []string{"https://app.acme.com"} }
	h, _ := newTestHandler(lookup, []string{"https://console.example.com"})

	w := doRequest(h, http.MethodGet, "/t/acme/cds/api/v1/profiles", "https://console.example.com")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty once the org sets its own list", got)
	}
}

// A path with no organisation, such as the health check, uses the deployment list.
func TestPathWithoutOrgUsesDeploymentOrigins(t *testing.T) {

	lookup := func(_ context.Context, _ Org) []string {
		t.Error("lookup must not be called for a path carrying no organisation")
		return nil
	}
	h, _ := newTestHandler(lookup, []string{"https://console.example.com"})

	w := doRequest(h, http.MethodGet, "/health", "https://console.example.com")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://console.example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want the deployment origin", got)
	}
}

func TestDisallowedOriginGetsNoCORSHeaders(t *testing.T) {

	lookup := func(_ context.Context, _ Org) []string { return []string{"https://app.acme.com"} }
	h, _ := newTestHandler(lookup, nil)

	w := doRequest(h, http.MethodGet, "/t/acme/cds/api/v1/profiles", "https://evil.example.com")
	for _, header := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Credentials",
		"Access-Control-Allow-Methods",
	} {
		if got := w.Header().Get(header); got != "" {
			t.Errorf("%s = %q, want empty for a disallowed origin", header, got)
		}
	}
}

// A preflight is answered for the organisation's own origin and never reaches the handler.
func TestPreflightIsAnsweredForAllowedOrigin(t *testing.T) {

	lookup := func(_ context.Context, _ Org) []string { return []string{"https://app.acme.com"} }
	h, reached := newTestHandler(lookup, nil)

	r := httptest.NewRequest(http.MethodOptions, "/t/acme/cds/api/v1/profiles", nil)
	r.Header.Set("Origin", "https://app.acme.com")
	r.Header.Set("Access-Control-Request-Headers", "X-Custom")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if *reached {
		t.Error("preflight reached the wrapped handler")
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); got != "X-Custom" {
		t.Errorf("Access-Control-Allow-Headers = %q, want the requested headers", got)
	}
}

// The allowed set varies by Origin and by organisation, so the response must say so.
func TestVaryOriginIsAlwaysSet(t *testing.T) {

	lookup := func(_ context.Context, _ Org) []string { return []string{"https://app.acme.com"} }
	h, _ := newTestHandler(lookup, nil)

	for _, origin := range []string{"https://app.acme.com", "https://evil.example.com"} {
		w := doRequest(h, http.MethodGet, "/t/acme/cds/api/v1/profiles", origin)
		if got := w.Header().Get("Vary"); got != "Origin" {
			t.Errorf("Vary = %q for origin %s, want Origin", got, origin)
		}
	}
}

// A request with no Origin header is a same-origin or non-browser call and passes through.
func TestRequestWithoutOriginPassesThrough(t *testing.T) {

	lookup := func(_ context.Context, _ Org) []string { return []string{"https://app.acme.com"} }
	h, reached := newTestHandler(lookup, nil)

	w := doRequest(h, http.MethodGet, "/t/acme/cds/api/v1/profiles", "")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty with no Origin header", got)
	}
	if !*reached {
		t.Error("request did not reach the wrapped handler")
	}
}

// A configured origin differing only in scheme/host case still matches the browser's
// already-normalized Origin header.
func TestOriginMatchIsCaseInsensitiveOnSchemeAndHost(t *testing.T) {

	lookup := func(_ context.Context, _ Org) []string { return []string{"HTTPS://App.Acme.COM"} }
	h, _ := newTestHandler(lookup, nil)

	w := doRequest(h, http.MethodGet, "/t/acme/cds/api/v1/profiles", "https://app.acme.com")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.acme.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want the request origin", got)
	}
}
