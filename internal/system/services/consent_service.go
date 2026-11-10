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

package services

import (
	"net/http"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/consent/handler"
	"github.com/wso2/identity-customer-data-service/internal/system/audit"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

type ConsentCategoryService struct {
	handler *handler.ConsentCategoryHandler
	mux     *http.ServeMux
}

func NewConsentCategoryService(mux *http.ServeMux) *ConsentCategoryService {
	s := &ConsentCategoryService{
		handler: handler.NewConsentCategoryHandler(),
		mux:     mux,
	}

	const base = constants.ApiBasePath + "/v1"
	// Register routes with Go 1.22 ServeMux patterns on shared mux
	s.mux.HandleFunc("GET "+base+"/consent-categories", audit.Wrap(audit.ConsentCategoryList, s.handler.GetAllConsentCategories))
	s.mux.HandleFunc("POST "+base+"/consent-categories", audit.Wrap(audit.ConsentCategoryCreate, s.handler.AddConsentCategory))
	s.mux.HandleFunc("GET "+base+"/consent-categories/{categoryId}", audit.Wrap(audit.ConsentCategoryRead, s.handler.GetConsentCategory))
	s.mux.HandleFunc("PUT "+base+"/consent-categories/{categoryId}", audit.Wrap(audit.ConsentCategoryUpdate, s.handler.UpdateConsentCategory))
	s.mux.HandleFunc("DELETE "+base+"/consent-categories/{categoryId}", audit.Wrap(audit.ConsentCategoryDelete, s.handler.DeleteConsentCategory))

	return s
}

// Route handles tenant-aware routing for consent categories
func (s *ConsentCategoryService) Route(w http.ResponseWriter, r *http.Request) {
	// Normalize trailing slashes for consistent matching
	if trimmed := strings.TrimSuffix(r.URL.Path, "/"); trimmed != "" {
		r.URL.Path = trimmed
	}
	s.mux.ServeHTTP(w, r)
}
