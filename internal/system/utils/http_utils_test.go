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

package utils

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	error2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// serve sends a request through the tenant dispatcher and returns the status, the organization
// handle and the path that the handler got.
func serve(t *testing.T, path string) (int, string, string) {
	t.Helper()
	rec, gotOrg, gotPath := serveRecorded(t, path)
	return rec.Code, gotOrg, gotPath
}

// serveRecorded is serve, and also returns the recorded response.
func serveRecorded(t *testing.T, path string) (*httptest.ResponseRecorder, string, string) {
	t.Helper()
	mux := http.NewServeMux()
	var gotOrg, gotPath string
	MountTenantDispatcher(mux, func(w http.ResponseWriter, r *http.Request) {
		gotOrg, gotPath = ExtractOrgHandleFromPath(r), r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec, gotOrg, gotPath
}

func TestTenantDispatcherOrgPath(t *testing.T) {

	defer func(resolver func(context.Context, string, string) (string, error), isSub func(context.Context,
		string) (bool, error)) {
		SubOrgResolver, IsSubOrgHandle = resolver, isSub
	}(SubOrgResolver, IsSubOrgHandle)
	SubOrgResolver = func(_ context.Context, primaryHandle, orgId string) (string, error) {
		if primaryHandle == "carbon.super" && orgId == "4f088c20" {
			return "region1", nil
		}
		return "", error2.NewClientError(error2.ORGANIZATION_NOT_FOUND, http.StatusNotFound)
	}
	IsSubOrgHandle = func(_ context.Context, orgHandle string) (bool, error) { return orgHandle == "region1", nil }

	if code, org, path := serve(t, "/t/carbon.super/cds/api/v1/config"); code != 200 || org != "carbon.super" ||
		path != "/cds/api/v1/config" {
		t.Errorf("primary org path: got %d %q %q", code, org, path)
	}
	if code, org, path := serve(t, "/t/carbon.super/o/4f088c20/cds/api/v1/config"); code != 200 ||
		org != "region1" || path != "/cds/api/v1/config" {
		t.Errorf("sub org path: got %d %q %q", code, org, path)
	}
	if code, _, _ := serve(t, "/t/wso2.com/o/4f088c20/cds/api/v1/config"); code != http.StatusNotFound {
		t.Errorf("sub org path of another primary org: expected 404, got %d", code)
	}
	if code, _, _ := serve(t, "/t/carbon.super/o/cds/api/v1/config"); code != http.StatusNotFound {
		t.Errorf("sub org path without an org ID: expected 404, got %d", code)
	}
	SubOrgResolver = nil
	if code, _, _ := serve(t, "/t/carbon.super/o/4f088c20/cds/api/v1/config"); code != http.StatusNotFound {
		t.Errorf("sub org path without a resolver: expected 404, got %d", code)
	}
}

func TestTenantDispatcherSubOrgHandleOnPrimaryOrgPath(t *testing.T) {

	defer func(isSub func(context.Context, string) (bool, error)) { IsSubOrgHandle = isSub }(IsSubOrgHandle)
	IsSubOrgHandle = func(_ context.Context, orgHandle string) (bool, error) { return orgHandle == "region1", nil }

	rec, org, _ := serveRecorded(t, "/t/region1/cds/api/v1/config")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if org != "" {
		t.Errorf("the handler must not run, but it got org %q", org)
	}
	raw := rec.Body.String()
	var body map[string]string
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("the body is not JSON: %q", raw)
	}
	want := map[string]string{
		"code":        error2.ORGANIZATION_PATH_NOT_SUPPORTED.Code,
		"message":     "Organization path not supported.",
		"description": "The requested organization cannot be accessed using this API path.",
	}
	if len(body) != len(want) {
		t.Errorf("unexpected fields in the body: %q", raw)
	}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("%s: expected %q, got %q", key, value, body[key])
		}
	}
	if body["code"] != "CDS-17009" {
		t.Errorf("the numeric error code changed: %q", body["code"])
	}
	for _, leak := range []string{"region1", "sub organization", "/o/", "/t/"} {
		if strings.Contains(raw, leak) {
			t.Errorf("the body reveals %q: %q", leak, raw)
		}
	}

	// The dispatcher accepts a handle that is not a stored sub organization.
	if code, org, _ := serve(t, "/t/unknown.org/cds/api/v1/config"); code != http.StatusOK || org != "unknown.org" {
		t.Errorf("unknown handle: got %d %q", code, org)
	}
}

func TestTenantDispatcherLookupFailure(t *testing.T) {

	if err := log.Init("ERROR"); err != nil {
		t.Fatalf("failed to initialize the logger: %v", err)
	}

	defer func(resolver func(context.Context, string, string) (string, error), isSub func(context.Context,
		string) (bool, error)) {
		SubOrgResolver, IsSubOrgHandle = resolver, isSub
	}(SubOrgResolver, IsSubOrgHandle)
	failure := error2.NewServerError(error2.ErrorMessage{
		Code:        error2.ORGANIZATION_STORE.Code,
		Message:     error2.ORGANIZATION_STORE.Message,
		Description: "Failed to read the organization.",
	}, errors.New("connection refused"))
	SubOrgResolver = func(_ context.Context, _, _ string) (string, error) { return "", failure }
	IsSubOrgHandle = func(_ context.Context, _ string) (bool, error) { return false, failure }

	for _, path := range []string{"/t/carbon.super/o/4f088c20/cds/api/v1/config", "/t/carbon.super/cds/api/v1/config"} {
		rec, org, _ := serveRecorded(t, path)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: expected 500, got %d", path, rec.Code)
		}
		if org != "" {
			t.Errorf("%s: the handler must not run, but it got org %q", path, org)
		}
		if strings.Contains(rec.Body.String(), "connection refused") {
			t.Errorf("%s: the body reveals the cause: %q", path, rec.Body.String())
		}
	}
}

func TestTenantDispatcherOrgBasePath(t *testing.T) {

	defer func(resolver func(context.Context, string, string) (string, error), isSub func(context.Context,
		string) (bool, error)) {
		SubOrgResolver, IsSubOrgHandle = resolver, isSub
	}(SubOrgResolver, IsSubOrgHandle)
	SubOrgResolver = func(_ context.Context, _, _ string) (string, error) { return "region1", nil }
	IsSubOrgHandle = func(_ context.Context, _ string) (bool, error) { return false, nil }

	basePathOf := func(path string) string {
		mux := http.NewServeMux()
		var basePath string
		MountTenantDispatcher(mux, func(w http.ResponseWriter, r *http.Request) {
			basePath = ExtractOrgBasePath(r)
		})
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, nil))
		return basePath
	}
	if got := basePathOf("/t/carbon.super/cds/api/v1/profiles"); got != "/t/carbon.super" {
		t.Errorf("primary org path: got %q", got)
	}
	if got := basePathOf("/t/carbon.super/o/4f088c20/cds/api/v1/profiles"); got != "/t/carbon.super/o/4f088c20" {
		t.Errorf("sub org path: got %q", got)
	}
}

func TestTenantDispatcherDoesNotServeOrgRootPath(t *testing.T) {

	if code, org, _ := serve(t, "/o/4f088c20/cds/api/v1/config"); code != http.StatusNotFound || org != "" {
		t.Errorf("expected 404 without the handler, got %d %q", code, org)
	}
}
