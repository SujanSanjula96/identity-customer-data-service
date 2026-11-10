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

	"github.com/wso2/identity-customer-data-service/internal/profile_schema/handler"
	"github.com/wso2/identity-customer-data-service/internal/system/audit"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

type ProfileSchemaService struct {
	handler *handler.ProfileSchemaHandler
	mux     *http.ServeMux
}

func NewProfileSchemaService(mux *http.ServeMux) *ProfileSchemaService {
	s := &ProfileSchemaService{
		handler: handler.NewProfileSchemaHandler(),
		mux:     mux,
	}

	const base = constants.ApiBasePath + "/v1"
	// Register routes using Go 1.22 ServeMux patterns on shared mux
	s.mux.HandleFunc("GET "+base+"/profile-schema", audit.Wrap(audit.ProfileSchemaRead, s.handler.GetProfileSchema))
	s.mux.HandleFunc("DELETE "+base+"/profile-schema", audit.Wrap(audit.ProfileSchemaDelete, s.handler.DeleteProfileSchema))
	s.mux.HandleFunc("POST "+base+"/profile-schema/sync", audit.Wrap(audit.ProfileSchemaSync, s.handler.SyncProfileSchema))

	// Scope-level
	s.mux.HandleFunc("POST "+base+"/profile-schema/{scope}", audit.Wrap(audit.ProfileSchemaAttributeCreate, s.handler.AddProfileSchemaAttributesForScope))
	s.mux.HandleFunc("GET "+base+"/profile-schema/{scope}", audit.Wrap(audit.ProfileSchemaAttributeList, s.handler.GetProfileSchemaAttributeForScope))
	s.mux.HandleFunc("DELETE "+base+"/profile-schema/{scope}", audit.Wrap(audit.ProfileSchemaAttributeDelete, s.handler.DeleteProfileSchemaAttributeForScope))

	// Attribute-level (preserve original verb mapping)
	s.mux.HandleFunc("GET "+base+"/profile-schema/{scope}/{attrID}", audit.Wrap(audit.ProfileSchemaAttributeRead, s.handler.GetProfileSchemaAttributeById))
	s.mux.HandleFunc("PUT "+base+"/profile-schema/{scope}/{attrID}", audit.Wrap(audit.ProfileSchemaAttributeUpdate, s.handler.PatchProfileSchemaAttributeById))
	s.mux.HandleFunc("DELETE "+base+"/profile-schema/{scope}/{attrID}", audit.Wrap(audit.ProfileSchemaAttributeDelete, s.handler.DeleteProfileSchemaAttributeById))

	if config.OrganizationManagementEnabled() {
		const policies = base + "/profile-schema/{scope}/{attrID}/sharing-policies"
		s.mux.HandleFunc("POST "+policies, audit.Wrap(audit.ProfileSchemaSharingPolicyCreate, s.handler.CreateSharingPolicy))
		s.mux.HandleFunc("GET "+policies, audit.Wrap(audit.ProfileSchemaSharingPolicyList, s.handler.ListSharingPolicies))
		s.mux.HandleFunc("GET "+policies+"/{policyId}", audit.Wrap(audit.ProfileSchemaSharingPolicyRead, s.handler.GetSharingPolicy))
		s.mux.HandleFunc("PUT "+policies+"/{policyId}", audit.Wrap(audit.ProfileSchemaSharingPolicyUpdate, s.handler.UpdateSharingPolicy))
		s.mux.HandleFunc("DELETE "+policies+"/{policyId}", audit.Wrap(audit.ProfileSchemaSharingPolicyDelete, s.handler.DeleteSharingPolicy))
	}

	return s
}

// Route handles tenant-aware profile-schema endpoints
func (s *ProfileSchemaService) Route(w http.ResponseWriter, r *http.Request) {
	if trimmed := strings.TrimSuffix(r.URL.Path, "/"); trimmed != "" {
		r.URL.Path = trimmed
	}
	s.mux.ServeHTTP(w, r)
}
