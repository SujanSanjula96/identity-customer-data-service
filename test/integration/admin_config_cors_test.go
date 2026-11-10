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
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
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

// The CORS middleware reads the origins of one organisation by org ID, and an organisation that
// has configured none, or an empty list, reads as none.
func TestCORSAllowedOriginsAreReadPerOrg(t *testing.T) {

	ctx := context.Background()
	acmeId := storeCORSOrigins(t, "cors-one-acme", []string{"https://app.acme.com", "http://localhost:3000"})
	initechId := storeCORSOrigins(t, "cors-one-initech", []string{})

	cases := map[string]struct {
		orgId string
		want  []string
	}{
		"configured":       {acmeId, []string{"https://app.acme.com", "http://localhost:3000"}},
		"empty list":       {initechId, nil},
		"never configured": {resolveOrgId(t, "cors-one-never-configured"), nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := adminConfigStore.GetCORSAllowedOrigins(ctx, c.orgId)
			if err != nil {
				t.Fatalf("failed to read the origins: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("origins = %v, want %v", got, c.want)
			}
		})
	}
}

// The stored origins drive the middleware end to end: each organisation's own origin is
// allowed on its own path and refused on the other's.
func TestStoredOriginsDriveTheMiddleware(t *testing.T) {

	storeCORSOrigins(t, "cors-mw-acme", []string{"https://app.acme.com"})
	storeCORSOrigins(t, "cors-mw-globex", []string{"https://app.globex.com"})

	// Resolve the handle as the server does and read straight through the store rather than
	// the service, so the assertion is about what was persisted and not about what a cache
	// happens to hold. Like the service, it answers a sub organisation with the origins of its
	// primary organisation.
	lookup := func(ctx context.Context, org cors.Org) ([]string, error) {
		ref, found, err := orgService.LookupOrganization(ctx, org.Handle)
		if err != nil || !found {
			return nil, err
		}
		return adminConfigStore.GetCORSAllowedOrigins(ctx, ref.ID)
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
		{"deployment origin still allowed once org adds its own",
			"/t/cors-mw-acme/cds/api/v1/profiles", "https://console.example.com", "https://console.example.com"},
		{"deployment origin allowed for unconfigured org",
			"/t/cors-never-configured/cds/api/v1/profiles", "https://console.example.com",
			"https://console.example.com"},
		{"deployment origin allowed for a handle CDS does not know",
			"/t/cors-mw-unknown/cds/api/v1/profiles", "https://console.example.com",
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
