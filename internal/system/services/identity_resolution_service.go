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

	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/handler"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

type IdentityResolutionService struct {
	handler *handler.IdentityResolutionHandler
	mux     *http.ServeMux
}

func NewIdentityResolutionService(mux *http.ServeMux) *IdentityResolutionService {
	s := &IdentityResolutionService{
		handler: handler.NewIdentityResolutionHandler(),
		mux:     mux,
	}

	const base = constants.ApiBasePath + "/v1"

	// Review tasks management
	s.mux.HandleFunc("GET "+base+"/identity-resolution/review-tasks", s.handler.GetReviewTasks)
	s.mux.HandleFunc("POST "+base+"/identity-resolution/review-tasks/{taskId}/resolve", s.handler.ResolveReviewTask)

	return s
}
