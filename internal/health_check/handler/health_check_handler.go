/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
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

package handler

import (
	"encoding/json"
	"github.com/wso2/identity-customer-data-service/internal/health_check/provider"
	"net/http"
)

// HealthHandler implements health and readiness endpoints.
type HealthHandler struct{}

// NewHealthHandler creates a new instance of HealthHandler.
func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// HandleHealth responds to /health requests.
func (h *HealthHandler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	response := map[string]string{"status": "healthy"}
	writeJSONResponse(w, http.StatusOK, response)
}

// HandleReadiness responds to /ready requests.
func (h *HealthHandler) HandleReadiness(w http.ResponseWriter, r *http.Request) {
	healthCheckService := provider.NewHealthCheckProvider().GetHealthCheckService()
	if err := healthCheckService.CheckReadiness(r.Context()); err != nil {
		response := map[string]string{
			"status": "not ready",
			"error":  err.Error(),
		}
		writeJSONResponse(w, http.StatusServiceUnavailable, response)
		return
	}

	response := map[string]string{"status": "ready"}
	writeJSONResponse(w, http.StatusOK, response)
}

// writeJSONResponse is a common helper for JSON encoding.
func writeJSONResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}
