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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// serve sends a request through the tenant dispatcher and returns the status, the org handle
// and the path that the handler got.
func serve(t *testing.T, path string) (int, string, string) {
	t.Helper()
	mux := http.NewServeMux()
	var gotOrg, gotPath string
	MountTenantDispatcher(mux, func(w http.ResponseWriter, r *http.Request) {
		gotOrg, gotPath = ExtractOrgHandleFromPath(r), r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, gotOrg, gotPath
}

func TestTenantDispatcherOrgPath(t *testing.T) {

	defer func(resolver func(context.Context, string, string) (string, error), isSub func(context.Context,
		string) bool) {
		SubOrgResolver, IsSubOrgHandle = resolver, isSub
	}(SubOrgResolver, IsSubOrgHandle)
	SubOrgResolver = func(_ context.Context, rootHandle, orgId string) (string, error) {
		if rootHandle == "carbon.super" && orgId == "4f088c20" {
			return "region1", nil
		}
		return "", errors.New("not in the tree")
	}
	IsSubOrgHandle = func(_ context.Context, orgHandle string) bool { return orgHandle == "region1" }

	if code, org, path := serve(t, "/t/carbon.super/cds/api/v1/config"); code != 200 || org != "carbon.super" ||
		path != "/cds/api/v1/config" {
		t.Errorf("root path: got %d %q %q", code, org, path)
	}
	if code, org, path := serve(t, "/t/carbon.super/o/4f088c20/cds/api/v1/config"); code != 200 ||
		org != "region1" || path != "/cds/api/v1/config" {
		t.Errorf("sub org path: got %d %q %q", code, org, path)
	}
	if code, _, _ := serve(t, "/t/wso2.com/o/4f088c20/cds/api/v1/config"); code != http.StatusNotFound {
		t.Errorf("sub org path of another root: expected 404, got %d", code)
	}
	if code, _, _ := serve(t, "/t/region1/cds/api/v1/config"); code != http.StatusBadRequest {
		t.Errorf("sub org handle on the root path: expected 400, got %d", code)
	}
	if code, _, _ := serve(t, "/t/carbon.super/o/cds/api/v1/config"); code != http.StatusNotFound {
		t.Errorf("sub org path without an org ID: expected 404, got %d", code)
	}
	SubOrgResolver = nil
	if code, _, _ := serve(t, "/t/carbon.super/o/4f088c20/cds/api/v1/config"); code != http.StatusNotFound {
		t.Errorf("sub org path without a resolver: expected 404, got %d", code)
	}
}
