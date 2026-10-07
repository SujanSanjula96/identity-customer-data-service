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
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/sharing/model"
)

// ToTargets maps the target_org_scope of a request to the stored targets. The scope must have
// exactly one mode, and must name each child once. It returns the problems of the request, which
// are empty when the request is valid.
func ToTargets(scope *model.TargetOrgScope, initiatingOrgId string) ([]model.Target, []string) {

	if scope == nil {
		return nil, []string{"target_org_scope is required"}
	}
	switch {
	case scope.AllChildren && len(scope.ChildOrgs) > 0:
		return nil, []string{"use all_children or child_orgs, not both"}
	case scope.AllChildren:
		return []model.Target{{Scope: model.ScopeAllChildren, OrgId: initiatingOrgId}}, nil
	case len(scope.ChildOrgs) == 0:
		return nil, []string{"target_org_scope must have all_children: true or at least one child_orgs entry"}
	}

	var problems []string
	seen := map[string]bool{}
	targets := make([]model.Target, 0, len(scope.ChildOrgs))
	for _, child := range scope.ChildOrgs {
		orgId := strings.TrimSpace(child.OrgId)
		switch {
		case orgId == "":
			problems = append(problems, "each child_orgs entry requires an org_id")
			continue
		case seen[orgId]:
			problems = append(problems, fmt.Sprintf("the organization '%s' is in child_orgs more than once", orgId))
			continue
		}
		seen[orgId] = true
		target := model.Target{Scope: model.ScopeOrg, OrgId: orgId}
		if child.AllChildren {
			target.Scope = model.ScopeOrgSubtree
		}
		targets = append(targets, target)
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return targets, nil
}

// ToTargetOrgScope maps the stored targets to the target_org_scope of a response.
func ToTargetOrgScope(targets []model.Target) model.TargetOrgScope {

	var scope model.TargetOrgScope
	for _, t := range targets {
		switch t.Scope {
		case model.ScopeAllChildren:
			scope.AllChildren = true
		case model.ScopeOrg:
			scope.ChildOrgs = append(scope.ChildOrgs, model.ChildOrg{OrgId: t.OrgId})
		case model.ScopeOrgSubtree:
			scope.ChildOrgs = append(scope.ChildOrgs, model.ChildOrg{OrgId: t.OrgId, AllChildren: true})
		}
	}
	return scope
}

// IsBlanket reports whether the targets are the all_children mode.
func IsBlanket(targets []model.Target) bool {

	for _, t := range targets {
		if t.Scope == model.ScopeAllChildren {
			return true
		}
	}
	return false
}
