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

	"github.com/wso2/identity-customer-data-service/internal/health_check/handler"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// HealthService handles routing for health and readiness endpoints.
type HealthService struct {
	handler *handler.HealthHandler
	mux     *http.ServeMux
}

// NewHealthService creates a new HealthService instance using a shared mux.
func NewHealthService(mux *http.ServeMux) *HealthService {
	s := &HealthService{
		handler: handler.NewHealthHandler(),
		mux:     mux,
	}

	// Register routes using Go 1.22 ServeMux patterns
	const base = constants.APIBasePath + "/v1"
	s.mux.HandleFunc("GET "+base+"/health", s.handler.HandleHealth)
	s.mux.HandleFunc("GET "+base+"/ready", s.handler.HandleReadiness)

	return s
}

// Route dispatches health and readiness requests (not typically needed when using shared mux).
func (s *HealthService) Route(w http.ResponseWriter, r *http.Request) {
	// Normalize trailing slash for consistent matching
	if trimmed := strings.TrimSuffix(r.URL.Path, "/"); trimmed != "" {
		r.URL.Path = trimmed
	}
	s.mux.ServeHTTP(w, r)
}
