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

// Each organisation's origins round-trip through the store independently of the others.
func TestCORSAllowedOriginsRoundTripPerOrg(t *testing.T) {

	ctx := context.Background()

	acme := adminConfigModel.AdminConfig{
		OrgHandle:          "cors-acme",
		CDSEnabled:         true,
		SystemApplications: []string{},
		CORSAllowedOrigins: []string{"https://app.acme.com", "http://localhost:3000"},
	}
	globex := adminConfigModel.AdminConfig{
		OrgHandle:          "cors-globex",
		CDSEnabled:         true,
		SystemApplications: []string{},
		CORSAllowedOrigins: []string{"https://app.globex.com"},
	}

	for _, config := range []adminConfigModel.AdminConfig{acme, globex} {
		if err := adminConfigStore.UpdateAdminConfig(ctx, config, config.OrgHandle); err != nil {
			t.Fatalf("failed to store config for %s: %v", config.OrgHandle, err)
		}
	}

	for _, want := range []adminConfigModel.AdminConfig{acme, globex} {
		stored, err := adminConfigStore.GetAdminConfig(ctx, want.OrgHandle)
		if err != nil {
			t.Fatalf("failed to read config for %s: %v", want.OrgHandle, err)
		}
		if !reflect.DeepEqual(stored.CORSAllowedOrigins, want.CORSAllowedOrigins) {
			t.Errorf("origins for %s = %v, want %v",
				want.OrgHandle, stored.CORSAllowedOrigins, want.CORSAllowedOrigins)
		}
	}
}

// An organisation that has never been configured reports no origins, so the deployment
// fallback applies to it.
func TestCORSAllowedOriginsEmptyForUnconfiguredOrg(t *testing.T) {

	stored, err := adminConfigStore.GetAdminConfig(context.Background(), "cors-never-configured")
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	if len(stored.CORSAllowedOrigins) != 0 {
		t.Errorf("origins = %v, want empty", stored.CORSAllowedOrigins)
	}
}

// The origins of every organisation that configured any are read in one go, which is what
// lets the middleware answer a lookup without a read per request.
func TestAllCORSAllowedOriginsAreReadTogether(t *testing.T) {

	ctx := context.Background()

	configs := []adminConfigModel.AdminConfig{
		{
			OrgHandle:          "cors-all-acme",
			CDSEnabled:         true,
			SystemApplications: []string{},
			CORSAllowedOrigins: []string{"https://app.acme.com", "http://localhost:3000"},
		},
		{
			OrgHandle:          "cors-all-globex",
			CDSEnabled:         true,
			SystemApplications: []string{},
			CORSAllowedOrigins: []string{"https://app.globex.com"},
		},
		{
			OrgHandle:          "cors-all-initech",
			CDSEnabled:         true,
			SystemApplications: []string{},
			CORSAllowedOrigins: []string{},
		},
	}
	for _, config := range configs {
		if err := adminConfigStore.UpdateAdminConfig(ctx, config, config.OrgHandle); err != nil {
			t.Fatalf("failed to store config for %s: %v", config.OrgHandle, err)
		}
	}

	originsByOrg, err := adminConfigStore.GetAllCORSAllowedOrigins(ctx)
	if err != nil {
		t.Fatalf("failed to read the origins of every organisation: %v", err)
	}
	for _, config := range configs[:2] {
		if got := originsByOrg[config.OrgHandle]; !reflect.DeepEqual(got, config.CORSAllowedOrigins) {
			t.Errorf("origins for %s = %v, want %v", config.OrgHandle, got, config.CORSAllowedOrigins)
		}
	}
	if got, found := originsByOrg["cors-all-initech"]; found {
		t.Errorf("origins for cors-all-initech = %v, want no entry for an organisation with none", got)
	}
}

// The stored origins drive the middleware end to end: each organisation's own origin is
// allowed on its own path and refused on the other's.
func TestStoredOriginsDriveTheMiddleware(t *testing.T) {

	ctx := context.Background()

	configs := []adminConfigModel.AdminConfig{
		{
			OrgHandle:          "cors-mw-acme",
			CDSEnabled:         true,
			SystemApplications: []string{},
			CORSAllowedOrigins: []string{"https://app.acme.com"},
		},
		{
			OrgHandle:          "cors-mw-globex",
			CDSEnabled:         true,
			SystemApplications: []string{},
			CORSAllowedOrigins: []string{"https://app.globex.com"},
		},
	}
	for _, config := range configs {
		if err := adminConfigStore.UpdateAdminConfig(ctx, config, config.OrgHandle); err != nil {
			t.Fatalf("failed to store config for %s: %v", config.OrgHandle, err)
		}
	}

	// Read straight through the store rather than the service, so the assertion is about
	// what was persisted and not about what a cache happens to hold.
	lookup := func(ctx context.Context, orgHandle string) []string {
		stored, err := adminConfigStore.GetAdminConfig(ctx, orgHandle)
		if err != nil || stored == nil {
			return nil
		}
		return stored.CORSAllowedOrigins
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
