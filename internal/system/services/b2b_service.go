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
	shareHandler "github.com/wso2/identity-customer-data-service/internal/sharing/handler"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

// B2BService registers the organization and sharing endpoints.
type B2BService struct {
	organizations *orgHandler.OrganizationHandler
	shares        *shareHandler.ShareHandler
	mux           *http.ServeMux
}

func NewB2BService(mux *http.ServeMux) *B2BService {

	s := &B2BService{
		organizations: orgHandler.NewOrganizationHandler(),
		shares:        shareHandler.NewShareHandler(),
		mux:           mux,
	}

	// The path of a sub org is /t/{root_handle}/o/{org_id}/... A sub org handle is not valid in
	// /t/{handle}/...
	utils.SubOrgResolver = orgService.SubOrgHandleOf
	utils.IsSubOrgHandle = orgService.IsSubOrgHandle

	const base = constants.ApiBasePath + "/v1"
	s.mux.HandleFunc("GET "+base+"/organizations", s.organizations.ListOrganizations)
	s.mux.HandleFunc("POST "+base+"/organizations/sync", s.organizations.SyncOrganization)

	const orgAccess = base + "/config/organization-access"
	s.mux.HandleFunc("POST "+orgAccess, s.organizations.CreateOrgAccess)
	s.mux.HandleFunc("GET "+orgAccess, s.organizations.ListOrgAccess)
	s.mux.HandleFunc("GET "+orgAccess+"/{orgPolicyId}", s.organizations.GetOrgAccess)
	s.mux.HandleFunc("PUT "+orgAccess+"/{orgPolicyId}", s.organizations.UpdateOrgAccess)
	s.mux.HandleFunc("DELETE "+orgAccess+"/{orgPolicyId}", s.organizations.DeleteOrgAccess)

	const attrPolicies = base + "/profile-schema/{scope}/{attrID}/sharing-policies"
	s.mux.HandleFunc("POST "+attrPolicies, s.shares.CreateSchemaAttributePolicy)
	s.mux.HandleFunc("GET "+attrPolicies, s.shares.ListSchemaAttributePolicies)
	s.mux.HandleFunc("GET "+attrPolicies+"/{policyId}", s.shares.GetSchemaAttributePolicy)
	s.mux.HandleFunc("PUT "+attrPolicies+"/{policyId}", s.shares.UpdateSchemaAttributePolicy)
	s.mux.HandleFunc("DELETE "+attrPolicies+"/{policyId}", s.shares.DeleteSchemaAttributePolicy)

	const rulePolicies = base + "/unification-rules/{ruleId}/sharing-policies"
	s.mux.HandleFunc("POST "+rulePolicies, s.shares.CreateUnificationRulePolicy)
	s.mux.HandleFunc("GET "+rulePolicies, s.shares.ListUnificationRulePolicies)
	s.mux.HandleFunc("GET "+rulePolicies+"/{policyId}", s.shares.GetUnificationRulePolicy)
	s.mux.HandleFunc("PUT "+rulePolicies+"/{policyId}", s.shares.UpdateUnificationRulePolicy)
	s.mux.HandleFunc("DELETE "+rulePolicies+"/{policyId}", s.shares.DeleteUnificationRulePolicy)
	return s
}
