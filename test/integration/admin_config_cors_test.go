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

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	adminConfigModel "github.com/wso2/identity-customer-data-service/internal/admin_config/model"
	adminConfigStore "github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	"github.com/wso2/identity-customer-data-service/internal/system/cors"
)

// storeCORSOrigins stores the origins as the configuration of the organisation, which it
// stores first as a request would, and returns the org ID the configuration is kept under.
func storeCORSOrigins(t *testing.T, orgHandle string, origins []string) string {

	t.Helper()
	orgId := resolveOrgId(t, orgHandle)
	config := adminConfigModel.AdminConfig{
		OrgId:              orgId,
		CDSEnabled:         true,
		SystemApplications: []string{},
		CORSAllowedOrigins: origins,
	}
	if err := adminConfigStore.UpdateAdminConfig(context.Background(), config, orgId); err != nil {
		t.Fatalf("failed to store config for %s: %v", orgHandle, err)
	}
	return orgId
}

// Each organisation's origins round-trip through the store independently of the others.
func TestCORSAllowedOriginsRoundTripPerOrg(t *testing.T) {

	want := map[string][]string{
		"cors-acme":   {"https://app.acme.com", "http://localhost:3000"},
		"cors-globex": {"https://app.globex.com"},
	}
	orgIds := map[string]string{}
	for orgHandle, origins := range want {
		orgIds[orgHandle] = storeCORSOrigins(t, orgHandle, origins)
	}

	for orgHandle, origins := range want {
		stored, err := adminConfigStore.GetAdminConfig(context.Background(), orgIds[orgHandle])
		if err != nil {
			t.Fatalf("failed to read config for %s: %v", orgHandle, err)
		}
		if !reflect.DeepEqual(stored.CORSAllowedOrigins, origins) {
			t.Errorf("origins for %s = %v, want %v", orgHandle, stored.CORSAllowedOrigins, origins)
		}
	}
}

// An organisation that has never been configured reports no origins, so the deployment
// fallback applies to it.
func TestCORSAllowedOriginsEmptyForUnconfiguredOrg(t *testing.T) {

	stored, err := adminConfigStore.GetAdminConfig(context.Background(), resolveOrgId(t, "cors-never-configured"))
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	if len(stored.CORSAllowedOrigins) != 0 {
		t.Errorf("origins = %v, want empty", stored.CORSAllowedOrigins)
	}
}

// The origins of every organisation that configured any are read in one go, which is what
// lets the middleware answer a lookup without a read per request. They are keyed by org
// handle, the only identifier the middleware has before the request is dispatched, although
// the configuration is stored by org ID.
func TestAllCORSAllowedOriginsAreReadTogether(t *testing.T) {

	want := map[string][]string{
		"cors-all-acme":   {"https://app.acme.com", "http://localhost:3000"},
		"cors-all-globex": {"https://app.globex.com"},
	}
	for orgHandle, origins := range want {
		storeCORSOrigins(t, orgHandle, origins)
	}
	storeCORSOrigins(t, "cors-all-initech", []string{})

	originsByOrg, err := adminConfigStore.GetAllCORSAllowedOrigins(context.Background())
	if err != nil {
		t.Fatalf("failed to read the origins of every organisation: %v", err)
	}
	for orgHandle, origins := range want {
		if got := originsByOrg[orgHandle]; !reflect.DeepEqual(got, origins) {
			t.Errorf("origins for %s = %v, want %v", orgHandle, got, origins)
		}
	}
	if got, found := originsByOrg["cors-all-initech"]; found {
		t.Errorf("origins for cors-all-initech = %v, want no entry for an organisation with none", got)
	}
}

// The stored origins drive the middleware end to end: each organisation's own origin is
// allowed on its own path and refused on the other's.
func TestStoredOriginsDriveTheMiddleware(t *testing.T) {

	storeCORSOrigins(t, "cors-mw-acme", []string{"https://app.acme.com"})
	storeCORSOrigins(t, "cors-mw-globex", []string{"https://app.globex.com"})

	// Read straight through the store rather than the service, so the assertion is about
	// what was persisted and not about what a cache happens to hold. Like the service, it
	// answers a sub organisation with the origins of its primary organisation.
	lookup := func(ctx context.Context, org cors.Org) []string {
		originsByOrg, err := adminConfigStore.GetAllCORSAllowedOrigins(ctx)
		if err != nil {
			return nil
		}
		return originsByOrg[org.Handle]
	}

	handler := cors.Middleware(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		lookup,
		func() []string { return []string{"https://console.example.com"} },
	)

	cases := []struct {
		name   string
		path   string
		origin string
		want   string
	}{
		{"own origin allowed", "/t/cors-mw-acme/cds/api/v1/profiles", "https://app.acme.com",
			"https://app.acme.com"},
		{"primary origin allowed on a sub organisation path",
			"/t/cors-mw-acme/o/4b1e/cds/api/v1/profiles", "https://app.acme.com", "https://app.acme.com"},
		{"foreign org origin refused", "/t/cors-mw-globex/cds/api/v1/profiles", "https://app.acme.com", ""},
		{"deployment origin refused once org configures its own",
			"/t/cors-mw-acme/cds/api/v1/profiles", "https://console.example.com", ""},
		{"deployment origin allowed for unconfigured org",
			"/t/cors-never-configured/cds/api/v1/profiles", "https://console.example.com",
			"https://console.example.com"},
		{"deployment origin allowed with no org in path", "/health", "https://console.example.com",
			"https://console.example.com"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, c.path, nil)
			r.Header.Set("Origin", c.origin)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			if got := w.Header().Get("Access-Control-Allow-Origin"); got != c.want {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, c.want)
			}
		})
	}
}
