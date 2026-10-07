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
	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/sharing/model"
	"github.com/wso2/identity-customer-data-service/internal/sharing/store"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

// CDS does not store the share state. A write stores only the policy and its targets. A read for
// one org resolves the state of each shared resource from the ancestor chain of the org, the
// local resources of the org, and the policies that reach it.

// ResolveOrg returns the org that CDS knows for the handle, or nil.
func ResolveOrg(ctx context.Context, orgHandle string) (*orgModel.Organization, error) {
	return orgStore.GetOrganizationByHandle(ctx, orgHandle)
}

// orgView is the result of the read for one org.
type orgView struct {
	states     []model.State
	attributes map[string]store.ChainAttribute
	rules      map[string]store.ChainRule
}

// resolveOrg computes the state of each shared resource that reaches the org.
func resolveOrg(ctx context.Context, org orgModel.Organization) (*orgView, error) {

	view := &orgView{attributes: map[string]store.ChainAttribute{}, rules: map[string]store.ChainRule{}}
	policies, err := store.GetPoliciesReachingOrg(ctx, org.OrgId)
	if err != nil || len(policies) == 0 {
		return view, err
	}
	chain, err := orgStore.GetOrganizationChain(ctx, org.OrgId)
	if err != nil {
		return nil, err
	}
	attrs, err := store.GetAttributesOfChain(ctx, org.OrgId)
	if err != nil {
		return nil, err
	}
	rules, err := store.GetRulesOfChain(ctx, org.OrgId)
	if err != nil {
		return nil, err
	}

	input := EngineInput{Orgs: treeOrgs(chain), Policies: policies}
	for _, a := range attrs {
		view.attributes[a.Attribute.AttributeId] = a
		input.Attributes = append(input.Attributes, AttributeInfo{Id: a.Attribute.AttributeId,
			Name: a.Attribute.AttributeName, ValueType: a.Attribute.ValueType, OwnerOrgId: a.OwnerOrgId})
	}
	for _, r := range rules {
		view.rules[r.Rule.RuleId] = r
		input.Rules = append(input.Rules, RuleInfo{Id: r.Rule.RuleId, PropertyName: r.Rule.PropertyName,
			PropertyId: r.Rule.PropertyId, OwnerOrgId: r.OwnerOrgId})
	}
	view.states = EvaluateOrg(input, org.OrgId)
	for i := range view.states {
		view.states[i].OrgHandle = org.OrgHandle
	}
	return view, nil
}

func treeOrgs(orgs []orgModel.Organization) []TreeOrg {

	result := make([]TreeOrg, 0, len(orgs))
	for _, o := range orgs {
		result = append(result, TreeOrg{Id: o.OrgId, Handle: o.OrgHandle, ParentId: o.ParentOrgId, Depth: o.Depth,
			Active: o.Status == orgModel.StatusActive})
	}
	return result
}

func viewOfHandle(ctx context.Context, orgHandle string) (*orgView, error) {

	org, err := orgStore.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil || org == nil {
		return nil, err
	}
	return resolveOrg(ctx, *org)
}

// ActiveSharedAttributes returns the shared attributes that are active in the org of the handle.
func ActiveSharedAttributes(ctx context.Context, orgHandle string) ([]store.SharedAttribute, error) {

	view, err := viewOfHandle(ctx, orgHandle)
	if err != nil || view == nil {
		return nil, err
	}
	var result []store.SharedAttribute
	for _, s := range view.states {
		if s.ResourceType == model.ResourceSchemaAttribute && s.State == model.StateActive {
			result = append(result, view.attributes[s.ResourceId].SharedAttribute)
		}
	}
	return result, nil
}

// ActiveSharedRules returns the shared rules that are active in the org of the handle, in the
// evaluation order: the group of the farthest ancestor first, and the owner priority in each
// group.
func ActiveSharedRules(ctx context.Context, orgHandle string) ([]store.SharedRule, error) {

	view, err := viewOfHandle(ctx, orgHandle)
	if err != nil || view == nil {
		return nil, err
	}
	var rules []store.SharedRule
	for _, s := range view.states {
		if s.ResourceType == model.ResourceUnificationRule && s.State == model.StateActive {
			rules = append(rules, view.rules[s.ResourceId].SharedRule)
		}
	}
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].OwnerDepth != rules[j].OwnerDepth {
			return rules[i].OwnerDepth < rules[j].OwnerDepth
		}
		return rules[i].Rule.Priority < rules[j].Rule.Priority
	})
	return rules, nil
}

// StatesForOrg returns the states of the shared resources of one type in the org of the handle.
func StatesForOrg(ctx context.Context, resourceType, orgHandle string) (map[string]model.State, error) {

	result := map[string]model.State{}
	view, err := viewOfHandle(ctx, orgHandle)
	if err != nil || view == nil {
		return result, err
	}
	for _, s := range view.states {
		if s.ResourceType == resourceType {
			result[s.ResourceId] = s
		}
	}
	return result, nil
}

// CreatePolicy creates the policy of the initiating org for a resource that it owns. The caller
// checks that the resource exists, that the initiating org owns it, and that it can be shared.
func CreatePolicy(ctx context.Context, resourceType, resourceId string, initiator orgModel.Organization,
	req model.PolicyRequest) (*model.PolicyResponse, error) {

	existing, err := store.GetPolicy(ctx, resourceType, resourceId, initiator.OrgId)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, PolicyExistsError(existing.PolicyId)
	}
	targets, problems := ToTargets(req.TargetOrgScope, initiator.OrgId)
	if len(problems) > 0 {
		return nil, badRequest(errors2.SHARE_BAD_REQUEST, strings.Join(problems, "; "))
	}
	candidate := model.Policy{
		PolicyId:        uuid.New().String(),
		ResourceType:    resourceType,
		ResourceId:      resourceId,
		OwningOrgId:     initiator.OrgId,
		InitiatingOrgId: initiator.OrgId,
		Stage:           model.StageShare,
		Targets:         targets,
	}
	if err := validateCandidate(ctx, initiator, candidate); err != nil {
		return nil, err
	}
	if err := store.CreatePolicy(ctx, candidate); err != nil {
		return nil, err
	}
	resp := ToResponse(candidate)
	return &resp, nil
}

// ListPolicies returns the policies of the initiating org for the resource. In the crawl phase,
// the list has a maximum of one policy.
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

// GetPolicy returns the policy, with the state in each org of one page of the reached orgs.
func GetPolicy(ctx context.Context, resourceType, resourceId, policyId string, initiator orgModel.Organization,
	limit, offset int) (*model.PolicyWithStates, error) {

	p, err := policyOf(ctx, resourceType, resourceId, policyId, initiator)
	if err != nil {
		return nil, err
	}
	page, total, err := store.GetReachedOrgsPage(ctx, p.PolicyId, limit, offset)
	if err != nil {
		return nil, err
	}
	resp := &model.PolicyWithStates{PolicyResponse: ToResponse(*p), TotalStates: total, States: []model.State{}}
	for _, reached := range page {
		org, err := orgStore.GetOrganizationById(ctx, reached.OrgId)
		if err != nil {
			return nil, err
		}
		if org == nil {
			continue
		}
		view, err := resolveOrg(ctx, *org)
		if err != nil {
			return nil, err
		}
		for _, s := range view.states {
			if s.ResourceType == resourceType && s.ResourceId == resourceId {
				resp.States = append(resp.States, s)
			}
		}
	}
	return resp, nil
}

// UpdatePolicy replaces the targets of the policy. The last PUT wins.
func UpdatePolicy(ctx context.Context, resourceType, resourceId, policyId string, initiator orgModel.Organization,
	req model.PolicyRequest) (*model.PolicyResponse, error) {

	p, err := policyOf(ctx, resourceType, resourceId, policyId, initiator)
	if err != nil {
		return nil, err
	}
	targets, problems := ToTargets(req.TargetOrgScope, initiator.OrgId)
	if len(problems) > 0 {
		return nil, badRequest(errors2.SHARE_BAD_REQUEST, strings.Join(problems, "; "))
	}
	p.Targets = targets
	if err := validateCandidate(ctx, initiator, *p); err != nil {
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

	p, err := policyOf(ctx, resourceType, resourceId, policyId, initiator)
	if err != nil {
		return err
	}
	return store.DeletePolicy(ctx, p.PolicyId)
}

// DeletePoliciesOfResource deletes all policies of a resource that its owner deletes.
func DeletePoliciesOfResource(ctx context.Context, resourceType, resourceId string) error {
	return store.DeletePoliciesOfResource(ctx, resourceType, resourceId)
}

// policyOf returns the policy of the path. A policy of another resource, or of another
// initiating org, is not found.
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

// validateCandidate checks the targets against the tree. For a rule, it also checks that the
// attribute of the rule is visible in each org that the policy reaches.
func validateCandidate(ctx context.Context, initiator orgModel.Organization, candidate model.Policy) error {

	input, orgs, err := loadTree(ctx, initiator.RootOrgId)
	if err != nil {
		return err
	}
	if problems := ValidatePolicy(input.Orgs, candidate); len(problems) > 0 {
		return badRequest(errors2.SHARE_BAD_REQUEST, strings.Join(problems, "; "))
	}
	if candidate.ResourceType != model.ResourceUnificationRule {
		return nil
	}
	replaced := false
	for i, p := range input.Policies {
		if p.PolicyId == candidate.PolicyId {
			input.Policies[i] = candidate
			replaced = true
		}
	}
	if !replaced {
		input.Policies = append(input.Policies, candidate)
	}
	var missing []string
	for _, s := range Evaluate(input) {
		if s.ResourceId == candidate.ResourceId && s.State == model.StateInactiveMissingAttribute {
			missing = append(missing, handleOf(orgs, s.OrgId))
		}
	}
	if len(missing) > 0 {
		return badRequest(errors2.SHARE_MISSING_ATTRIBUTE, fmt.Sprintf("No compatible attribute for the rule "+
			"property is visible in these organizations: %s. Share the attribute first, or change the targets.",
			strings.Join(missing, ", ")))
	}
	return nil
}

// loadTree loads the orgs, the resources, and the share policies of one customer tree, for the
// checks at share time.
func loadTree(ctx context.Context, rootOrgId string) (EngineInput, []orgModel.Organization, error) {

	orgs, err := orgStore.GetOrganizationsByRoot(ctx, rootOrgId)
	if err != nil {
		return EngineInput{}, nil, err
	}
	attrs, err := store.GetAttributesByRoot(ctx, rootOrgId)
	if err != nil {
		return EngineInput{}, nil, err
	}
	rules, err := store.GetRulesByRoot(ctx, rootOrgId)
	if err != nil {
		return EngineInput{}, nil, err
	}
	policies, err := store.GetPoliciesByRoot(ctx, rootOrgId)
	if err != nil {
		return EngineInput{}, nil, err
	}

	input := EngineInput{Orgs: treeOrgs(orgs), Policies: policies}
	for _, a := range attrs {
		input.Attributes = append(input.Attributes, AttributeInfo{Id: a.Id, Name: a.Name, ValueType: a.ValueType,
			OwnerOrgId: a.OrgId})
	}
	for _, r := range rules {
		input.Rules = append(input.Rules, RuleInfo{Id: r.Id, PropertyName: r.PropertyName, PropertyId: r.PropertyId,
			OwnerOrgId: r.OrgId})
	}
	return input, orgs, nil
}

// ToResponse maps a policy to the API.
func ToResponse(p model.Policy) model.PolicyResponse {
	return model.PolicyResponse{
		Id:              p.PolicyId,
		ResourceType:    p.ResourceType,
		ResourceId:      p.ResourceId,
		OwningOrgId:     p.OwningOrgId,
		InitiatingOrgId: p.InitiatingOrgId,
		TargetOrgScope:  ToTargetOrgScope(p.Targets),
	}
}

// IsOrgEnabled reports whether the organization access policy of the root reaches the sub org.
// The caller checks the enablement of the root.
func IsOrgEnabled(ctx context.Context, org orgModel.Organization) (bool, error) {

	if org.IsRoot() {
		return true, nil
	}
	policies, err := store.GetOrgAccessPoliciesReachingOrg(ctx, org.OrgId)
	if err != nil || len(policies) == 0 {
		return false, err
	}
	chain, err := orgStore.GetOrganizationChain(ctx, org.OrgId)
	if err != nil {
		return false, err
	}
	orgs := treeOrgs(chain)
	for _, p := range policies {
		for _, id := range Reach(orgs, p) {
			if id == org.OrgId {
				return true, nil
			}
		}
	}
	return false, nil
}

// PolicyExistsError is the error for a second POST from the same org.
func PolicyExistsError(policyId string) error {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:    errors2.SHARE_POLICY_EXISTS.Code,
		Message: errors2.SHARE_POLICY_EXISTS.Message,
		Description: fmt.Sprintf("The organization already has the policy '%s' for this resource. Use PUT on that "+
			"policy to change it.", policyId),
	}, http.StatusConflict)
}

// PolicyNotFoundError is the error for a policy ID that is not a policy of the path.
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

// ReadOnlyError is the error for a write to a shared resource from a target org.
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

func handleOf(orgs []orgModel.Organization, orgId string) string {

	for _, o := range orgs {
		if o.OrgId == orgId {
			return o.OrgHandle
		}
	}
	return orgId
}
