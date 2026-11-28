/*
 * Copyright (c) (2025-2026), Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
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
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/admin_config/handler"
	"github.com/wso2/identity-customer-data-service/internal/system/audit"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// AdminConfigService registers the admin config endpoints.
type AdminConfigService struct {
	handler *handler.AdminConfigHandler
	mux     *http.ServeMux
}

// NewAdminConfigService registers the admin config routes on mux and returns the service.
func NewAdminConfigService(mux *http.ServeMux) *AdminConfigService {
	s := &AdminConfigService{
		handler: handler.NewAdminConfigHandler(),
		mux:     mux,
	}

	const base = constants.APIBasePath + "/v1"
	// Register routes with Go 1.22 ServeMux patterns on shared mux
	s.mux.HandleFunc("GET "+base+"/config", s.handler.GetAdminConfig)
	s.mux.HandleFunc("PATCH "+base+"/config", audit.Wrap(audit.AdminConfigUpdate, s.handler.UpdateAdminConfig))
	s.mux.HandleFunc("POST "+base+"/config/sync", audit.Wrap(audit.AdminConfigSync, s.handler.SyncAdminConfig))

	return s
}

// Route handles tenant-aware routing for consent categories
func (s *AdminConfigService) Route(w http.ResponseWriter, r *http.Request) {
	// Normalize trailing slashes for consistent matching
	if trimmed := strings.TrimSuffix(r.URL.Path, "/"); trimmed != "" {
		r.URL.Path = trimmed
	}
	s.mux.ServeHTTP(w, r)
}
