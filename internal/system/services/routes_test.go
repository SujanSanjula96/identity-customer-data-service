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
package services

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

type route struct {
	method string
	path   string
}

// tenantRouter mounts a service behind the tenant dispatcher, as the server does, so that a request
// takes the full /t/{org_handle}/cds/api/v1/... path.
func tenantRouter(route http.HandlerFunc) *http.ServeMux {
	root := http.NewServeMux()
	utils.MountTenantDispatcher(root, route)
	return root
}

func send(router http.Handler, method, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/t/acme/cds/api/v1"+path, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// requireRoutesRequireAuthentication checks that every route is registered, and that its handler is
// reached: a request without a token is refused with a 401, not routed to a 404 or a 405.
func requireRoutesRequireAuthentication(t *testing.T, router http.Handler, routes []route) {
	t.Helper()
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			assert.Equal(t, http.StatusUnauthorized, send(router, rt.method, rt.path).Code)
		})
	}
}

func setUp(t *testing.T) {
	t.Helper()
	require.NoError(t, log.Init("ERROR"))
	// The sharing and the organization routes exist only when organization management is on.
	setup.EnableOrganizationManagement()
}

func TestProfileRoutes(t *testing.T) {
	setUp(t)
	service := NewProfileService(http.NewServeMux())

	requireRoutesRequireAuthentication(t, tenantRouter(service.Route), []route{
		{http.MethodGet, "/profiles"},
		{http.MethodPost, "/profiles"},
		{http.MethodGet, "/profiles/Me"},
		{http.MethodPatch, "/profiles/Me"},
		{http.MethodPost, "/profiles/sync"},
		{http.MethodPost, "/profiles/link"},
		{http.MethodGet, "/profiles/p-1"},
		{http.MethodPatch, "/profiles/p-1"},
		{http.MethodDelete, "/profiles/p-1"},
		{http.MethodGet, "/profiles/p-1/consents"},
		{http.MethodPut, "/profiles/p-1/consents"},
	})
}

func TestProfileSchemaRoutes(t *testing.T) {
	setUp(t)
	service := NewProfileSchemaService(http.NewServeMux())

	requireRoutesRequireAuthentication(t, tenantRouter(service.Route), []route{
		{http.MethodGet, "/profile-schema"},
		{http.MethodDelete, "/profile-schema"},
		{http.MethodPost, "/profile-schema/sync"},
		{http.MethodPost, "/profile-schema/traits"},
		{http.MethodGet, "/profile-schema/traits"},
		{http.MethodDelete, "/profile-schema/traits"},
		{http.MethodGet, "/profile-schema/traits/a-1"},
		{http.MethodPut, "/profile-schema/traits/a-1"},
		{http.MethodDelete, "/profile-schema/traits/a-1"},
		{http.MethodPost, "/profile-schema/traits/a-1/sharing-policies"},
		{http.MethodGet, "/profile-schema/traits/a-1/sharing-policies"},
		{http.MethodGet, "/profile-schema/traits/a-1/sharing-policies/s-1"},
		{http.MethodPut, "/profile-schema/traits/a-1/sharing-policies/s-1"},
		{http.MethodDelete, "/profile-schema/traits/a-1/sharing-policies/s-1"},
	})
}

func TestUnificationRulesRoutes(t *testing.T) {
	setUp(t)
	service := NewUnificationRulesService(http.NewServeMux())

	requireRoutesRequireAuthentication(t, tenantRouter(service.Route), []route{
		{http.MethodGet, "/unification-rules/options"},
		{http.MethodPost, "/unification-rules"},
		{http.MethodGet, "/unification-rules"},
		{http.MethodGet, "/unification-rules/r-1"},
		{http.MethodPatch, "/unification-rules/r-1"},
		{http.MethodDelete, "/unification-rules/r-1"},
		{http.MethodPost, "/unification-rules/r-1/sharing-policies"},
		{http.MethodGet, "/unification-rules/r-1/sharing-policies"},
		{http.MethodGet, "/unification-rules/r-1/sharing-policies/s-1"},
		{http.MethodPut, "/unification-rules/r-1/sharing-policies/s-1"},
		{http.MethodDelete, "/unification-rules/r-1/sharing-policies/s-1"},
	})
}

func TestConsentCategoryRoutes(t *testing.T) {
	setUp(t)
	service := NewConsentCategoryService(http.NewServeMux())

	requireRoutesRequireAuthentication(t, tenantRouter(service.Route), []route{
		{http.MethodGet, "/consent-categories"},
		{http.MethodPost, "/consent-categories"},
		{http.MethodGet, "/consent-categories/c-1"},
		{http.MethodPut, "/consent-categories/c-1"},
		{http.MethodDelete, "/consent-categories/c-1"},
	})
}

func TestAdminConfigRoutes(t *testing.T) {
	setUp(t)
	service := NewAdminConfigService(http.NewServeMux())

	requireRoutesRequireAuthentication(t, tenantRouter(service.Route), []route{
		{http.MethodGet, "/config"},
		{http.MethodPatch, "/config"},
	})
}

func TestIdentityResolutionRoutes(t *testing.T) {
	setUp(t)
	// The service registers its routes on the shared mux and has no route function of its own.
	mux := http.NewServeMux()
	NewIdentityResolutionService(mux)

	requireRoutesRequireAuthentication(t, tenantRouter(mux.ServeHTTP), []route{
		{http.MethodGet, "/identity-resolution/review-tasks"},
		{http.MethodPost, "/identity-resolution/review-tasks/t-1/resolve"},
	})
}

func TestOrganizationRoutes(t *testing.T) {
	setUp(t)
	// The service installs the sub organization resolvers of the tenant dispatcher.
	resolver, isSubOrg := utils.SubOrgResolver, utils.IsSubOrgHandle
	t.Cleanup(func() { utils.SubOrgResolver, utils.IsSubOrgHandle = resolver, isSubOrg })
	mux := http.NewServeMux()
	NewOrganizationService(mux)

	requireRoutesRequireAuthentication(t, tenantRouter(mux.ServeHTTP), []route{
		{http.MethodPost, "/organizations/event-sync"},
		{http.MethodPost, "/organizations/sync"},
		{http.MethodGet, "/organizations/sync-status"},
		{http.MethodPost, "/organizations/access-policies"},
		{http.MethodGet, "/organizations/access-policies"},
		{http.MethodGet, "/organizations/access-policies/o-1"},
		{http.MethodPut, "/organizations/access-policies/o-1"},
		{http.MethodDelete, "/organizations/access-policies/o-1"},
	})
}

func TestAnUnknownRouteOrMethodIsNotRouted(t *testing.T) {
	setUp(t)
	router := tenantRouter(NewConsentCategoryService(http.NewServeMux()).Route)

	assert.Equal(t, http.StatusNotFound, send(router, http.MethodGet, "/nothing-here").Code)
	assert.Equal(t, http.StatusMethodNotAllowed, send(router, http.MethodPatch, "/consent-categories").Code)
}

func TestATrailingSlashIsIgnored(t *testing.T) {
	setUp(t)
	router := tenantRouter(NewAdminConfigService(http.NewServeMux()).Route)

	assert.Equal(t, http.StatusUnauthorized, send(router, http.MethodGet, "/config/").Code)
}

func TestHealthRoutesAreNotTenanted(t *testing.T) {
	setUp(t)
	service := NewHealthService(http.NewServeMux())

	request := httptest.NewRequest(http.MethodGet, "/cds/api/v1/health", nil)
	recorder := httptest.NewRecorder()
	service.Route(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"status":"healthy"}`, recorder.Body.String())

	missing := httptest.NewRecorder()
	service.Route(missing, httptest.NewRequest(http.MethodPost, "/cds/api/v1/health", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, missing.Code)
}
