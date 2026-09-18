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

package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// TestMain gives the package a logger and a runtime configuration, which the
// client reads on every call.
func TestMain(m *testing.M) {

	_ = log.Init("ERROR")
	config.OverrideCDSRuntime(config.Config{})

	os.Exit(m.Run())
}

// silentIdentityServer accepts a request and never answers it. It stands for an
// Identity Server that has stopped responding.
func silentIdentityServer(t *testing.T) *httptest.Server {

	t.Helper()

	blocked := make(chan struct{})

	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-blocked
	}))

	// The handler has to be released before the server is closed, so this runs
	// first.
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(blocked) })

	return server
}

// Test_requestToken_endsWhenTheContextDoes checks that cancellation reaches the
// request to the Identity Server.
//
// Every call was built with http.NewRequest, which carries no context, so only
// the 30 second timeout of the HTTP client could end it. Schema sync makes
// several of these calls one after another, so shutdown could run far past its
// deadline while it waited for a server that had stopped answering.
func Test_requestToken_endsWhenTheContextDoes(t *testing.T) {

	server := silentIdentityServer(t)
	identityClient := &IdentityClient{HTTPClient: server.Client()}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := identityClient.requestToken(ctx, server.URL, "client", "secret", url.Values{}, "carbon.super")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a server that never answers")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected the context to end the call, got %v", err)
	}
	// The HTTP client allows 30 seconds. Anything near that means the context
	// never reached the request.
	if elapsed > 5*time.Second {
		t.Errorf("the call took %v, so the timeout of the HTTP client ended it rather than the context", elapsed)
	}
}

// Test_FetchToken_endsWhenTheContextDoes runs the same check through the call
// every other one makes first.
//
// Schema sync fetches a token, then the local claims, then the dialects, then
// the claims of each dialect. A context that does not reach the first of those
// reaches none of them.
func Test_FetchToken_endsWhenTheContextDoes(t *testing.T) {

	server := silentIdentityServer(t)
	host := strippedHost(t, server.URL)

	// Point the runtime at the server that never answers.
	config.OverrideCDSRuntime(config.Config{
		AuthServer: config.AuthServerConfig{
			Host:          host,
			TokenEndpoint: "/oauth2/token",
			ClientID:      "client",
			ClientSecret:  "secret",
		},
	})
	t.Cleanup(func() { config.OverrideCDSRuntime(config.Config{}) })

	identityClient := &IdentityClient{BaseURL: host, HTTPClient: server.Client()}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := identityClient.FetchToken(ctx, "carbon.super")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a server that never answers")
	}
	// The request really left, rather than failing before it was sent.
	if elapsed < 250*time.Millisecond {
		t.Fatalf("the call returned after %v, so it never reached the server", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Errorf("the call took %v, so the timeout of the HTTP client ended it rather than the context", elapsed)
	}
}

// strippedHost returns the host and port of a test server URL.
func strippedHost(t *testing.T, rawURL string) string {

	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Host
}
