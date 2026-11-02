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

	orgHandler "github.com/wso2/identity-customer-data-service/internal/organization/handler"
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

// OrganizationService registers the organization endpoints.
type OrganizationService struct {
	organizations *orgHandler.OrganizationHandler
	mux           *http.ServeMux
}

func NewOrganizationService(mux *http.ServeMux) *OrganizationService {

	s := &OrganizationService{
		organizations: orgHandler.NewOrganizationHandler(),
		mux:           mux,
	}

	// Sub organization paths are /t/{primary_handle}/o/{org_id}/...
	utils.SubOrgResolver = orgService.SubOrgHandleOf
	utils.IsSubOrgHandle = orgService.IsSubOrgHandle

	const base = constants.ApiBasePath + "/v1"
	s.mux.HandleFunc("POST "+base+"/organizations/event-sync", s.organizations.SyncOrganization)
	s.mux.HandleFunc("POST "+base+"/organizations/sync", s.organizations.StartTreeSync)
	s.mux.HandleFunc("GET "+base+"/organizations/sync-status", s.organizations.GetTreeSync)

	const orgAccess = base + "/organizations/access-policies"
	s.mux.HandleFunc("POST "+orgAccess, s.organizations.CreateOrgAccess)
	s.mux.HandleFunc("GET "+orgAccess, s.organizations.ListOrgAccess)
	s.mux.HandleFunc("GET "+orgAccess+"/{orgPolicyId}", s.organizations.GetOrgAccess)
	s.mux.HandleFunc("PUT "+orgAccess+"/{orgPolicyId}", s.organizations.UpdateOrgAccess)
	s.mux.HandleFunc("DELETE "+orgAccess+"/{orgPolicyId}", s.organizations.DeleteOrgAccess)

	return s
}
