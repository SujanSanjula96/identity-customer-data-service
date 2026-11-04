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
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	"github.com/wso2/identity-customer-data-service/internal/organization/suborg"
	"github.com/wso2/identity-customer-data-service/internal/sharing/model"
	"github.com/wso2/identity-customer-data-service/internal/sharing/store"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

// GetSharedResources returns the resources of the type that the ancestors of the organization
// share with it, the nearest owner first. The result does not include the resources that the
// organization owns.
func GetSharedResources(ctx context.Context, resourceType string,
	org orgModel.Organization) ([]model.SharedResource, error) {

	if org.IsPrimary() || !org.IsActive() {
		return nil, nil
	}
	ancestors, err := orgService.GetAncestors(ctx, org)
	if err != nil || len(ancestors) == 0 {
		return nil, err
	}
	enabled, err := isOrgEnabled(ctx, org, ancestors)
	if err != nil || !enabled {
		return nil, err
	}
	return sharedResources(ctx, resourceType, org, ancestors)
}

// GetSharedResource returns the resource when an ancestor of the organization shares it with the
// organization, or nil.
func GetSharedResource(ctx context.Context, resourceType, resourceId string,
	org orgModel.Organization) (*model.SharedResource, error) {

	shared, err := GetSharedResources(ctx, resourceType, org)
	if err != nil {
		return nil, err
	}
	for _, r := range shared {
		if r.ResourceId == resourceId {
			return &r, nil
		}
	}
	return nil, nil
}

// sharedResources returns the resources of the type that the policies of the ancestors share
// with the organization.
func sharedResources(ctx context.Context, resourceType string, org orgModel.Organization,
	ancestors []orgModel.Ancestor) ([]model.SharedResource, error) {

	ownerIds := make([]string, 0, len(ancestors))
	byId := make(map[string]orgModel.Ancestor, len(ancestors))
	for _, a := range ancestors {
		ownerIds = append(ownerIds, a.OrgId)
		byId[a.OrgId] = a
	}
	policies, err := store.GetPoliciesByOwners(ctx, resourceType, ownerIds)
	if err != nil {
		return nil, err
	}
	var result []model.SharedResource
	seen := map[string]bool{}
	for _, p := range policies {
		if seen[p.ResourceId] || !policyReachesOrg(p, org, ancestors) {
			continue
		}
		seen[p.ResourceId] = true
		owner := byId[p.OwningOrgId]
		result = append(result, model.SharedResource{
			ResourceId:     p.ResourceId,
			OwnerOrgId:     owner.OrgId,
			OwnerOrgHandle: owner.OrgHandle,
			OwnerDepth:     owner.Depth,
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].OwnerDepth != result[j].OwnerDepth {
			return result[i].OwnerDepth < result[j].OwnerDepth
		}
		return result[i].ResourceId < result[j].ResourceId
	})
	return result, nil
}

// CreatePolicy creates the policy of the initiating organization for a resource that it owns.
func CreatePolicy(ctx context.Context, resourceType, resourceId string, initiator orgModel.Organization,
	req model.PolicyRequest) (*model.PolicyResponse, error) {

	if !initiator.IsPrimary() {
		return nil, suborg.WriteNotAllowed()
	}
	existing, err := store.GetPolicy(ctx, resourceType, resourceId, initiator.OrgId)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, PolicyExistsError(existing.PolicyId)
	}
	targets, problems := ToTargets(req.ChildOrgs, initiator.OrgId)
	if len(problems) > 0 {
		return nil, badRequest(errors2.SHARE_BAD_REQUEST, strings.Join(problems, "; "))
	}
	candidate := model.Policy{
		PolicyId:        uuid.New().String(),
		ResourceType:    resourceType,
		ResourceId:      resourceId,
		OwningOrgId:     initiator.OrgId,
		InitiatingOrgId: initiator.OrgId,
		Targets:         targets,
	}
	if err := ValidateTargets(ctx, initiator, candidate); err != nil {
		return nil, err
	}
	if err := store.CreatePolicy(ctx, candidate); err != nil {
		return nil, err
	}
	resp := ToResponse(candidate)
	return &resp, nil
}

// ListPolicies returns the policies of the initiating organization for the resource.
func ListPolicies(ctx context.Context, resourceType, resourceId string,
	initiator orgModel.Organization) (*model.PolicyList, error) {

	list := &model.PolicyList{Policies: []model.PolicyResponse{}}
	p, err := store.GetPolicy(ctx, resourceType, resourceId, initiator.OrgId)
	if err != nil {
		return nil, err
	}
	if p != nil {
		list.Policies = append(list.Policies, ToResponse(*p))
	}
	list.TotalResults = len(list.Policies)
	return list, nil
}

// GetPolicy returns the policy.
func GetPolicy(ctx context.Context, resourceType, resourceId, policyId string,
	initiator orgModel.Organization) (*model.PolicyResponse, error) {

	p, err := policyOf(ctx, resourceType, resourceId, policyId, initiator)
	if err != nil {
		return nil, err
	}
	resp := ToResponse(*p)
	return &resp, nil
}

// UpdatePolicy replaces the targets of the policy.
func UpdatePolicy(ctx context.Context, resourceType, resourceId, policyId string, initiator orgModel.Organization,
	req model.PolicyRequest) (*model.PolicyResponse, error) {

	if !initiator.IsPrimary() {
		return nil, suborg.WriteNotAllowed()
	}
	p, err := policyOf(ctx, resourceType, resourceId, policyId, initiator)
	if err != nil {
		return nil, err
	}
	targets, problems := ToTargets(req.ChildOrgs, initiator.OrgId)
	if len(problems) > 0 {
		return nil, badRequest(errors2.SHARE_BAD_REQUEST, strings.Join(problems, "; "))
	}
	p.Targets = targets
	if err := ValidateTargets(ctx, initiator, *p); err != nil {
		return nil, err
	}
	if err := store.ReplaceTargets(ctx, p.PolicyId, targets); err != nil {
		return nil, err
	}
	resp := ToResponse(*p)
	return &resp, nil
}

// DeletePolicy deletes the policy.
func DeletePolicy(ctx context.Context, resourceType, resourceId, policyId string,
	initiator orgModel.Organization) error {

	if !initiator.IsPrimary() {
		return suborg.WriteNotAllowed()
	}
	p, err := policyOf(ctx, resourceType, resourceId, policyId, initiator)
	if err != nil {
		return err
	}
	return store.DeletePolicy(ctx, p.PolicyId)
}

// DeletePoliciesOfResource deletes all policies of the resource. It does nothing when organization
// management is disabled.
func DeletePoliciesOfResource(ctx context.Context, resourceType, resourceId string) error {
	if !config.OrganizationManagementEnabled() {
		return nil
	}
	return store.DeletePoliciesOfResource(ctx, resourceType, resourceId)
}

// policyOf returns the policy with the ID when it belongs to the resource and the initiating
// organization.
func policyOf(ctx context.Context, resourceType, resourceId, policyId string,
	initiator orgModel.Organization) (*model.Policy, error) {

	p, err := store.GetPolicyById(ctx, policyId)
	if err != nil {
		return nil, err
	}
	if p == nil || p.ResourceType != resourceType || p.ResourceId != resourceId ||
		p.InitiatingOrgId != initiator.OrgId {
		return nil, PolicyNotFoundError(policyId)
	}
	return p, nil
}

// ValidateTargets checks the targets of the policy against the initiating organization and the
// target organizations.
func ValidateTargets(ctx context.Context, initiator orgModel.Organization, p model.Policy) error {

	var targetOrgs []orgModel.Organization
	if !IsAllChildren(p.Targets) {
		ids := make([]string, 0, len(p.Targets))
		for _, t := range p.Targets {
			ids = append(ids, t.OrgId)
		}
		orgs, err := orgService.GetOrganizationsByIds(ctx, ids)
		if err != nil {
			return err
		}
		targetOrgs = orgs
	}
	if problems := targetProblems(initiator, targetOrgs, p.Targets); len(problems) > 0 {
		return BadRequest(strings.Join(problems, "; "))
	}
	return nil
}

// ToTargets maps the child_orgs of a request to stored targets, and returns the problems of the
// request.
func ToTargets(childOrgs *model.ChildOrgs, initiatingOrgId string) ([]model.Target, []string) {

	if childOrgs == nil {
		return nil, []string{"child_orgs is required"}
	}
	switch childOrgs.Mode {
	case "":
		return nil, []string{"child_orgs.mode is required"}
	case model.ChildOrgsModeAll:
		if len(childOrgs.Orgs) > 0 {
			return nil, []string{"child_orgs.orgs must be empty when child_orgs.mode is all"}
		}
		return []model.Target{{Scope: model.ScopeAllChildren, OrgId: initiatingOrgId}}, nil
	case model.ChildOrgsModeSelected:
		if len(childOrgs.Orgs) == 0 {
			return nil, []string{"child_orgs.orgs must have at least one organization when child_orgs.mode is " +
				"selected"}
		}
	default:
		return nil, []string{fmt.Sprintf("child_orgs.mode '%s' is not valid. Use all or selected",
			childOrgs.Mode)}
	}

	var problems []string
	seen := map[string]bool{}
	targets := make([]model.Target, 0, len(childOrgs.Orgs))
	for _, child := range childOrgs.Orgs {
		orgId := strings.TrimSpace(child.OrgId)
		if orgId == "" {
			problems = append(problems, "each child_orgs.orgs entry requires an org_id")
			continue
		}
		if seen[orgId] {
			problems = append(problems, fmt.Sprintf("the organization '%s' is in child_orgs.orgs more than once",
				orgId))
			continue
		}
		seen[orgId] = true
		var scope string
		switch child.Reach {
		case model.ReachOrg:
			scope = model.ScopeOrg
		case model.ReachSubtree:
			scope = model.ScopeOrgSubtree
		case "":
			problems = append(problems, fmt.Sprintf("the organization '%s' requires a reach", orgId))
			continue
		default:
			problems = append(problems, fmt.Sprintf("the reach '%s' of the organization '%s' is not valid. "+
				"Use org or subtree", child.Reach, orgId))
			continue
		}
		targets = append(targets, model.Target{Scope: scope, OrgId: orgId})
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return targets, nil
}

// ToChildOrgs maps the stored targets to the child_orgs of a response.
func ToChildOrgs(targets []model.Target) model.ChildOrgs {

	if IsAllChildren(targets) {
		return model.ChildOrgs{Mode: model.ChildOrgsModeAll}
	}
	childOrgs := model.ChildOrgs{Mode: model.ChildOrgsModeSelected}
	for _, t := range targets {
		switch t.Scope {
		case model.ScopeOrg:
			childOrgs.Orgs = append(childOrgs.Orgs, model.ChildOrg{OrgId: t.OrgId, Reach: model.ReachOrg})
		case model.ScopeOrgSubtree:
			childOrgs.Orgs = append(childOrgs.Orgs, model.ChildOrg{OrgId: t.OrgId, Reach: model.ReachSubtree})
		}
	}
	return childOrgs
}

// IsAllChildren reports whether the targets have the ALL_CHILDREN scope.
func IsAllChildren(targets []model.Target) bool {

	for _, t := range targets {
		if t.Scope == model.ScopeAllChildren {
			return true
		}
	}
	return false
}

// ToResponse maps a policy to the API.
func ToResponse(p model.Policy) model.PolicyResponse {
	return model.PolicyResponse{
		Id:              p.PolicyId,
		ResourceType:    p.ResourceType,
		ResourceId:      p.ResourceId,
		OwningOrgId:     p.OwningOrgId,
		InitiatingOrgId: p.InitiatingOrgId,
		ChildOrgs:       ToChildOrgs(p.Targets),
	}
}

// PolicyExistsError is the error for a second policy of the same organization.
func PolicyExistsError(policyId string) error {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:    errors2.SHARE_POLICY_EXISTS.Code,
		Message: errors2.SHARE_POLICY_EXISTS.Message,
		Description: fmt.Sprintf("The organization already has the policy '%s' for this resource. Use PUT on that "+
			"policy to change it.", policyId),
	}, http.StatusConflict)
}

// PolicyNotFoundError is the error for an unknown policy ID.
func PolicyNotFoundError(policyId string) error {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.SHARE_POLICY_NOT_FOUND.Code,
		Message:     errors2.SHARE_POLICY_NOT_FOUND.Message,
		Description: fmt.Sprintf("The policy '%s' is not found for this resource and organization.", policyId),
	}, http.StatusNotFound)
}

// ConflictError is the error for a local resource whose name is used by an active shared
// resource.
func ConflictError(description string) error {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.SHARED_NAME_CONFLICT.Code,
		Message:     errors2.SHARED_NAME_CONFLICT.Message,
		Description: description,
	}, http.StatusConflict)
}

// ReadOnlyError is the error for a write to a shared resource from a target organization.
func ReadOnlyError() error {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.SHARED_RESOURCE_READ_ONLY.Code,
		Message:     errors2.SHARED_RESOURCE_READ_ONLY.Message,
		Description: errors2.SHARED_RESOURCE_READ_ONLY.Description,
	}, http.StatusForbidden)
}

// BadRequest is the error for a request that is not valid.
func BadRequest(description string) error {
	return badRequest(errors2.SHARE_BAD_REQUEST, description)
}

func badRequest(msg errors2.ErrorMessage, description string) error {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        msg.Code,
		Message:     msg.Message,
		Description: description,
	}, http.StatusBadRequest)
}
