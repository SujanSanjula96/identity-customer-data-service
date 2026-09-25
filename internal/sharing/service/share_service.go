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
	"sync"

	"github.com/google/uuid"
	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/sharing/model"
	"github.com/wso2/identity-customer-data-service/internal/sharing/store"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// rootLocks serializes the evaluation of one customer tree, so that two changes do not write
// the share states of the tree at the same time. It works for one CDS instance only.
var rootLocks sync.Map

func lockRoot(rootOrgId string) func() {

	value, _ := rootLocks.LoadOrStore(rootOrgId, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// ResolveOrg returns the org that CDS knows for the handle, or nil.
func ResolveOrg(ctx context.Context, orgHandle string) (*orgModel.Organization, error) {
	return orgStore.GetOrganizationByHandle(ctx, orgHandle)
}

// PutPolicy creates or replaces the policy of the initiating org for a resource that it owns.
// The caller checks that the resource exists, that the initiating org owns it, and that the
// resource can be shared.
func PutPolicy(ctx context.Context, resourceType, resourceId string, initiator orgModel.Organization,
	req model.ShareRequest) (*model.ShareResponse, error) {

	unlock := lockRoot(initiator.RootOrgId)
	defer unlock()

	input, orgs, err := loadInput(ctx, initiator.RootOrgId)
	if err != nil {
		return nil, err
	}

	candidate := model.Policy{
		PolicyId:        uuid.New().String(),
		ResourceType:    resourceType,
		ResourceId:      resourceId,
		OwnerOrgId:      initiator.OrgId,
		InitiatingOrgId: initiator.OrgId,
		Stage:           model.StageShare,
		Targets:         dedupeTargets(req.Targets),
		ExcludedOrgIds:  dedupeStrings(req.ExcludedOrgIds),
	}
	if problems := ValidatePolicy(input.Orgs, candidate); len(problems) > 0 {
		return nil, badRequest(errors2.SHARE_BAD_REQUEST, strings.Join(problems, "; "))
	}

	// Evaluate the tree with the candidate policy before it is stored.
	replaced := false
	for i, p := range input.Policies {
		if p.ResourceType == resourceType && p.ResourceId == resourceId && p.InitiatingOrgId == initiator.OrgId {
			candidate.PolicyId = p.PolicyId
			input.Policies[i] = candidate
			replaced = true
		}
	}
	if !replaced {
		input.Policies = append(input.Policies, candidate)
	}
	states := Evaluate(input)

	// A rule needs its attribute in each reached org at share time.
	if resourceType == model.ResourceUnificationRule {
		var missing []string
		for _, s := range states {
			if s.ResourceId == resourceId && s.State == model.StateInactiveMissingAttribute {
				missing = append(missing, handleOf(orgs, s.OrgId))
			}
		}
		if len(missing) > 0 {
			return nil, badRequest(errors2.SHARE_MISSING_ATTRIBUTE, fmt.Sprintf("No compatible attribute for the "+
				"rule property is visible in these organizations: %s. Share the attribute first, or exclude the "+
				"organizations.", strings.Join(missing, ", ")))
		}
	}

	if _, err := store.SavePolicy(ctx, candidate); err != nil {
		return nil, err
	}
	if err := recomputeLocked(ctx, initiator.RootOrgId); err != nil {
		return nil, err
	}
	return getPolicy(ctx, resourceType, resourceId, initiator)
}

// GetPolicy returns the policy of the initiating org for the resource, with the state in each
// reached org.
func GetPolicy(ctx context.Context, resourceType, resourceId string,
	initiator orgModel.Organization) (*model.ShareResponse, error) {
	return getPolicy(ctx, resourceType, resourceId, initiator)
}

func getPolicy(ctx context.Context, resourceType, resourceId string,
	initiator orgModel.Organization) (*model.ShareResponse, error) {

	policies, err := store.GetPoliciesByRoot(ctx, initiator.RootOrgId)
	if err != nil {
		return nil, err
	}
	for _, p := range policies {
		if p.ResourceType != resourceType || p.ResourceId != resourceId || p.InitiatingOrgId != initiator.OrgId {
			continue
		}
		states, err := store.GetStatesOfResource(ctx, resourceType, resourceId)
		if err != nil {
			return nil, err
		}
		excluded := p.ExcludedOrgIds
		if excluded == nil {
			excluded = []string{}
		}
		return &model.ShareResponse{
			PolicyId:        p.PolicyId,
			ResourceType:    p.ResourceType,
			ResourceId:      p.ResourceId,
			OwnerOrgId:      p.OwnerOrgId,
			InitiatingOrgId: p.InitiatingOrgId,
			Stage:           p.Stage,
			Version:         p.Version,
			Targets:         p.Targets,
			ExcludedOrgIds:  excluded,
			CreatedAt:       p.CreatedAt,
			UpdatedAt:       p.UpdatedAt,
			States:          states,
		}, nil
	}
	return nil, errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.SHARE_POLICY_NOT_FOUND.Code,
		Message:     errors2.SHARE_POLICY_NOT_FOUND.Message,
		Description: fmt.Sprintf("The organization has no share policy for the resource '%s'.", resourceId),
	}, http.StatusNotFound)
}

// DeletePolicy deletes the policy of the initiating org for the resource.
func DeletePolicy(ctx context.Context, resourceType, resourceId string, initiator orgModel.Organization) error {

	unlock := lockRoot(initiator.RootOrgId)
	defer unlock()

	existing, err := getPolicy(ctx, resourceType, resourceId, initiator)
	if err != nil {
		return err
	}
	if err := store.DeletePolicy(ctx, existing.PolicyId); err != nil {
		return err
	}
	return recomputeLocked(ctx, initiator.RootOrgId)
}

// DeletePoliciesOfResource deletes all policies of a resource that its owner deletes.
func DeletePoliciesOfResource(ctx context.Context, resourceType, resourceId, rootOrgId string) error {

	unlock := lockRoot(rootOrgId)
	defer unlock()

	if err := store.DeletePoliciesOfResource(ctx, resourceType, resourceId); err != nil {
		return err
	}
	return recomputeLocked(ctx, rootOrgId)
}

// RecomputeTree evaluates all policies of one customer tree again, and stores the states.
func RecomputeTree(ctx context.Context, rootOrgId string) error {

	unlock := lockRoot(rootOrgId)
	defer unlock()
	return recomputeLocked(ctx, rootOrgId)
}

// RecomputeForOrgHandle evaluates the tree of the org again. It does nothing when CDS does not
// know the org.
func RecomputeForOrgHandle(ctx context.Context, orgHandle string) error {

	org, err := orgStore.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil || org == nil {
		return err
	}
	return RecomputeTree(ctx, org.RootOrgId)
}

func recomputeLocked(ctx context.Context, rootOrgId string) error {

	input, _, err := loadInput(ctx, rootOrgId)
	if err != nil {
		return err
	}
	states := Evaluate(input)
	if err := store.ReplaceStatesOfRoot(ctx, rootOrgId, states); err != nil {
		return err
	}
	log.GetLogger().Debug(fmt.Sprintf("Stored %d share states for the tree of root: %s", len(states), rootOrgId))
	return nil
}

func loadInput(ctx context.Context, rootOrgId string) (EngineInput, []orgModel.Organization, error) {

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

	input := EngineInput{Policies: policies}
	for _, o := range orgs {
		input.Orgs = append(input.Orgs, TreeOrg{Id: o.OrgId, Handle: o.OrgHandle, ParentId: o.ParentOrgId,
			Path: o.Path, Depth: o.Depth, Active: o.Status == orgModel.StatusActive})
	}
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

// ActiveSharedAttributes returns the shared attributes that are active in the org of the handle.
func ActiveSharedAttributes(ctx context.Context, orgHandle string) ([]store.SharedAttribute, error) {

	org, err := orgStore.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil || org == nil {
		return nil, err
	}
	return store.GetActiveSharedAttributes(ctx, org.OrgId)
}

// ActiveSharedRules returns the shared rules that are active in the org of the handle, in the
// evaluation order: the group of the farthest ancestor first, and the owner priority in each
// group.
func ActiveSharedRules(ctx context.Context, orgHandle string) ([]store.SharedRule, error) {

	org, err := orgStore.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil || org == nil {
		return nil, err
	}
	rules, err := store.GetActiveSharedRules(ctx, org.OrgId)
	if err != nil {
		return nil, err
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

	org, err := orgStore.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil || org == nil {
		return map[string]model.State{}, err
	}
	return store.GetStatesForOrg(ctx, resourceType, org.OrgId)
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

func dedupeTargets(targets []model.Target) []model.Target {

	seen := map[string]bool{}
	result := make([]model.Target, 0, len(targets))
	for _, t := range targets {
		t.Scope = strings.ToUpper(strings.TrimSpace(t.Scope))
		t.OrgId = strings.TrimSpace(t.OrgId)
		key := t.Scope + "|" + t.OrgId
		if !seen[key] {
			seen[key] = true
			result = append(result, t)
		}
	}
	return result
}

func dedupeStrings(values []string) []string {

	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}
