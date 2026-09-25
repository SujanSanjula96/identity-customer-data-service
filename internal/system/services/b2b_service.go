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
	shareHandler "github.com/wso2/identity-customer-data-service/internal/sharing/handler"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
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

	const base = constants.ApiBasePath + "/v1"
	s.mux.HandleFunc("GET "+base+"/organizations", s.organizations.ListOrganizations)
	s.mux.HandleFunc("POST "+base+"/organizations/sync", s.organizations.SyncOrganization)
	s.mux.HandleFunc("POST "+base+"/organizations/reconcile", s.organizations.ReconcileOrganizations)

	s.mux.HandleFunc("PUT "+base+"/profile-schema/{scope}/{attrID}/share", s.shares.PutSchemaAttributeShare)
	s.mux.HandleFunc("GET "+base+"/profile-schema/{scope}/{attrID}/share", s.shares.GetSchemaAttributeShare)
	s.mux.HandleFunc("DELETE "+base+"/profile-schema/{scope}/{attrID}/share", s.shares.DeleteSchemaAttributeShare)

	s.mux.HandleFunc("PUT "+base+"/unification-rules/{ruleId}/share", s.shares.PutUnificationRuleShare)
	s.mux.HandleFunc("GET "+base+"/unification-rules/{ruleId}/share", s.shares.GetUnificationRuleShare)
	s.mux.HandleFunc("DELETE "+base+"/unification-rules/{ruleId}/share", s.shares.DeleteUnificationRuleShare)
	return s
}
