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

package managers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/security"
)

const tenantBase = "/t/carbon.super/cds/api/v1"

// status sends a request without credentials. A registered route that the mux serves answers 401 from
// its authentication; a route the mux does not serve answers 404.
func status(handler http.Handler, method, path string) int {
	request := httptest.NewRequest(method, path, strings.NewReader("{}"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Code
}

func splitMuxes(t *testing.T) (http.Handler, http.Handler) {
	t.Helper()
	require.NoError(t, log.Init("ERROR"))
	config.OverrideCDSRuntime(config.Config{
		Sync:                   config.SyncConfig{Addr: config.AddrConfig{Port: 8901}},
		OrganizationManagement: config.OrganizationManagementConfig{Enabled: true},
	})
	mux, syncMux := http.NewServeMux(), http.NewServeMux()
	require.NoError(t, NewSplitServiceManager(mux, syncMux).RegisterServices())
	return mux, security.SyncListener(syncMux)
}

// Every pattern in syncRoutes is registered: the sync listener reaches its handler. A pattern that no
// longer matches a route would leave that route on the public listener.
func TestSyncRoutesAreServedOnTheSyncListenerOnly(t *testing.T) {
	public, sync := splitMuxes(t)

	for pattern := range syncRoutes {
		method, path, _ := strings.Cut(pattern, " ")
		target := "/t/carbon.super" + path
		assert.Equal(t, http.StatusUnauthorized, status(sync, method, target), "sync listener: %s", pattern)
		assert.Equal(t, http.StatusNotFound, status(public, method, target), "public listener: %s", pattern)
	}
}

func TestPublicRoutesAreNotServedOnTheSyncListener(t *testing.T) {
	public, sync := splitMuxes(t)

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/profiles"},
		{http.MethodGet, "/profiles/p-1"},
		{http.MethodDelete, "/profiles/p-1"},
		{http.MethodGet, "/config"},
		{http.MethodGet, "/profile-schema"},
		{http.MethodPost, "/organizations/sync"},
	} {
		target := tenantBase + route.path
		assert.Equal(t, http.StatusUnauthorized, status(public, route.method, target), "public listener: %v", route)
		assert.Equal(t, http.StatusNotFound, status(sync, route.method, target), "sync listener: %v", route)
	}
}

// The admin configuration update is on both listeners: the Console's token on the public one, the identity
// server's admin credentials on the sync one. The health endpoints answer on both.
func TestSharedRoutesAndHealthAreOnBothListeners(t *testing.T) {
	public, sync := splitMuxes(t)

	assert.Equal(t, http.StatusUnauthorized, status(public, http.MethodPatch, tenantBase+"/config"))
	assert.Equal(t, http.StatusUnauthorized, status(sync, http.MethodPatch, tenantBase+"/config"))
	for _, handler := range []http.Handler{public, sync} {
		assert.NotEqual(t, http.StatusNotFound, status(handler, http.MethodGet, "/cds/api/v1/health"))
	}
}

// Without a sync listener every route is on the one mux, as before.
func TestWithoutASyncListenerEveryRouteIsOnTheMux(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	config.OverrideCDSRuntime(config.Config{
		OrganizationManagement: config.OrganizationManagementConfig{Enabled: true},
	})
	mux := http.NewServeMux()
	require.NoError(t, NewServiceManager(mux).RegisterServices())

	for pattern := range syncRoutes {
		method, path, _ := strings.Cut(pattern, " ")
		assert.Equal(t, http.StatusUnauthorized, status(mux, method, "/t/carbon.super"+path), pattern)
	}
	assert.Equal(t, http.StatusUnauthorized, status(mux, http.MethodGet, tenantBase+"/profiles"))
}

// An identity server that sends its syncs to the public port (no sync_url, or one naming the public port) gets
// 404 on every sync. That is said once in the log, with the key and the sync port to point it at, and only for
// requests with the admin credentials.
func TestSyncRequestOnThePublicPortIsLoggedOnce(t *testing.T) {
	public, _ := splitMuxes(t)
	logPath := filepath.Join(t.TempDir(), "app.log")
	require.NoError(t, log.Configure(log.Options{Level: "WARN",
		Output: log.OutputOptions{File: log.FileOutput(logPath)}}))
	t.Cleanup(func() { _ = log.Init("ERROR") })
	syncRequestOnPublicPort = sync.Once{}

	send := func(basic bool) int {
		request := httptest.NewRequest(http.MethodPost, tenantBase+"/profiles/sync", strings.NewReader("{}"))
		if basic {
			request.SetBasicAuth("admin", "secret")
		}
		response := httptest.NewRecorder()
		public.ServeHTTP(response, request)
		return response.Code
	}
	readLog := func() string {
		content, err := os.ReadFile(logPath) // #nosec G304 -- the test reads a file in its temporary directory.
		require.NoError(t, err)
		return string(content)
	}

	assert.Equal(t, http.StatusNotFound, send(false))
	assert.NotContains(t, readLog(), "sync request to the public port", "a request without the admin credentials")

	assert.Equal(t, http.StatusNotFound, send(true))
	assert.Equal(t, http.StatusNotFound, send(true))
	assert.Equal(t, 1, strings.Count(readLog(), "sync request to the public port"))
	assert.Contains(t, readLog(), "Point [customer_data_service] sync_url of the identity server at the sync port")
	assert.Contains(t, readLog(), ":8901")
}
