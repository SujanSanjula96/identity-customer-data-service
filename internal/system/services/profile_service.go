/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
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

	"github.com/wso2/identity-customer-data-service/internal/profile/handler"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

type ProfileService struct {
	profileHandler *handler.ProfileHandler
	mux            *http.ServeMux
}

func NewProfileService(mux *http.ServeMux) *ProfileService {
	ps := &ProfileService{
		profileHandler: handler.NewProfileHandler(),
		mux:            mux,
	}

	const base = constants.ApiBasePath + "/v1"
	// Register routes using Go 1.22+ ServeMux patterns on the shared mux
	ps.mux.HandleFunc("GET "+base+"/profiles", ps.profileHandler.GetAllProfiles)
	ps.mux.HandleFunc("POST "+base+"/profiles", ps.profileHandler.InitProfile)
	ps.mux.HandleFunc("GET "+base+"/profiles/Me", ps.profileHandler.GetCurrentUserProfile)
	ps.mux.HandleFunc("PATCH "+base+"/profiles/Me", ps.profileHandler.PatchCurrentUserProfile)
	ps.mux.HandleFunc("POST "+base+"/profiles/sync", ps.profileHandler.SyncProfile)

	// Routes with path variables
	ps.mux.HandleFunc("GET "+base+"/profiles/{profileId}", ps.profileHandler.GetProfile)
	ps.mux.HandleFunc("PATCH "+base+"/profiles/{profileId}", ps.profileHandler.PatchProfile)
	ps.mux.HandleFunc("DELETE "+base+"/profiles/{profileId}", ps.profileHandler.DeleteProfile)
	ps.mux.HandleFunc("POST "+base+"/profiles/{profileId}/link", ps.profileHandler.LinkProfile)
	ps.mux.HandleFunc("GET "+base+"/profiles/{profileId}/consents", ps.profileHandler.GetProfileConsents)
	ps.mux.HandleFunc("PUT "+base+"/profiles/{profileId}/consents", ps.profileHandler.UpdateProfileConsents)

	return ps
}

// Route handles all tenant-aware profile-related endpoints by delegating to the shared mux
func (s *ProfileService) Route(w http.ResponseWriter, r *http.Request) {
	// Normalize trailing slashes to improve matching
	if trimmed := strings.TrimSuffix(r.URL.Path, "/"); trimmed != "" {
		r.URL.Path = trimmed
	}
	// Delegate to shared pattern-based mux
	s.mux.ServeHTTP(w, r)
}
