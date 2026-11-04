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

	"github.com/wso2/identity-customer-data-service/internal/unification_rules/handler"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

type UnificationRulesService struct {
	unificationRulesHandler *handler.UnificationRulesHandler
	mux                     *http.ServeMux
}

func NewUnificationRulesService(mux *http.ServeMux) *UnificationRulesService {
	s := &UnificationRulesService{
		unificationRulesHandler: handler.NewUnificationRulesHandler(),
		mux:                     mux,
	}

	const base = constants.ApiBasePath + "/v1"
	// Register routes using Go 1.22 ServeMux patterns on shared mux
	s.mux.HandleFunc("GET "+base+"/unification-rules/options", s.unificationRulesHandler.GetUnificationOptions)
	s.mux.HandleFunc("POST "+base+"/unification-rules", s.unificationRulesHandler.AddUnificationRule)
	s.mux.HandleFunc("GET "+base+"/unification-rules", s.unificationRulesHandler.GetUnificationRules)
	s.mux.HandleFunc("GET "+base+"/unification-rules/{ruleId}", s.unificationRulesHandler.GetUnificationRule)
	s.mux.HandleFunc("PATCH "+base+"/unification-rules/{ruleId}", s.unificationRulesHandler.PatchUnificationRule)
	s.mux.HandleFunc("DELETE "+base+"/unification-rules/{ruleId}", s.unificationRulesHandler.DeleteUnificationRule)

	if config.OrganizationManagementEnabled() {
		const policies = base + "/unification-rules/{ruleId}/sharing-policies"
		s.mux.HandleFunc("POST "+policies, s.unificationRulesHandler.CreateSharingPolicy)
		s.mux.HandleFunc("GET "+policies, s.unificationRulesHandler.ListSharingPolicies)
		s.mux.HandleFunc("GET "+policies+"/{policyId}", s.unificationRulesHandler.GetSharingPolicy)
		s.mux.HandleFunc("PUT "+policies+"/{policyId}", s.unificationRulesHandler.UpdateSharingPolicy)
		s.mux.HandleFunc("DELETE "+policies+"/{policyId}", s.unificationRulesHandler.DeleteSharingPolicy)
	}

	return s
}

// Route handles all tenant-aware unification rules endpoints
func (s *UnificationRulesService) Route(w http.ResponseWriter, r *http.Request) {
	if trimmed := strings.TrimSuffix(r.URL.Path, "/"); trimmed != "" {
		r.URL.Path = trimmed
	}
	s.mux.ServeHTTP(w, r)
}
