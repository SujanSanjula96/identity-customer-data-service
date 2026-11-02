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
package service

import (
	"fmt"

	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
	"github.com/wso2/identity-customer-data-service/internal/sharing/model"
)

// policyReachesOrg reports whether a target of the policy reaches the organization. The ancestors
// of the organization are in order, the parent first.
func policyReachesOrg(p model.Policy, org orgModel.Organization, ancestors []orgModel.Ancestor) bool {

	for _, target := range p.Targets {
		switch target.Scope {
		case model.ScopeAllChildren:
			if isAncestor(p.InitiatingOrgId, ancestors) {
				return true
			}
		case model.ScopeOrg:
			if target.OrgId == org.OrgId && org.ParentOrgId == p.InitiatingOrgId {
				return true
			}
		case model.ScopeOrgSubtree:
			if target.OrgId == org.OrgId && org.ParentOrgId == p.InitiatingOrgId {
				return true
			}
			for _, a := range ancestors {
				if target.OrgId == a.OrgId && a.ParentOrgId == p.InitiatingOrgId {
					return true
				}
			}
		}
	}
	return false
}

func isAncestor(orgId string, ancestors []orgModel.Ancestor) bool {

	for _, a := range ancestors {
		if a.OrgId == orgId {
			return true
		}
	}
	return false
}

// targetProblems checks the targets of a policy of the initiating organization, and returns the
// problems. The target organizations are the stored organizations that the targets name. The
// result is empty when the targets are valid.
func targetProblems(initiator orgModel.Organization, targetOrgs []orgModel.Organization,
	targets []model.Target) []string {

	byId := make(map[string]orgModel.Organization, len(targetOrgs))
	for _, o := range targetOrgs {
		byId[o.OrgId] = o
	}
	var problems []string
	if len(targets) == 0 {
		problems = append(problems, "at least one target is required")
	}
	for _, target := range targets {
		switch target.Scope {
		case model.ScopeAllChildren:
			if target.OrgId != initiator.OrgId {
				problems = append(problems, "the ALL_CHILDREN scope must name the initiating organization")
			}
		case model.ScopeOrg, model.ScopeOrgSubtree:
			org, known := byId[target.OrgId]
			switch {
			case target.OrgId == "":
				problems = append(problems, fmt.Sprintf("the %s scope requires an org_id", target.Scope))
			case !known:
				problems = append(problems, fmt.Sprintf("the organization '%s' is not known", target.OrgId))
			case org.ParentOrgId != initiator.OrgId:
				problems = append(problems, fmt.Sprintf(
					"the organization '%s' is not a direct child of the initiating organization", target.OrgId))
			case !org.IsActive():
				problems = append(problems, fmt.Sprintf("the organization '%s' is not active", target.OrgId))
			}
		default:
			problems = append(problems, fmt.Sprintf("the scope '%s' is not supported", target.Scope))
		}
	}
	return problems
}
