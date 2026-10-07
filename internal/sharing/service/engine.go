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
	"sort"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/sharing/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// The engine computes the share state of a shared resource in an org. It is pure: the service
// loads the orgs, the resources, and the policies. CDS does not store the result. The read for
// one org loads only the ancestor chain of the org, the local resources of the org, and the
// policies that reach it, and calls EvaluateOrg.

// TreeOrg is an org of the tree, as the engine needs it.
type TreeOrg struct {
	Id       string
	Handle   string
	ParentId string
	Depth    int
	Active   bool
}

// AttributeInfo is a profile schema attribute, as the engine needs it.
type AttributeInfo struct {
	Id         string
	Name       string
	ValueType  string
	OwnerOrgId string
}

// RuleInfo is a unification rule, as the engine needs it.
type RuleInfo struct {
	Id           string
	PropertyName string
	PropertyId   string
	OwnerOrgId   string
}

// EngineInput is everything the engine reads for one tree.
type EngineInput struct {
	Orgs       []TreeOrg
	Attributes []AttributeInfo
	Rules      []RuleInfo
	// Policies must be sorted oldest first. When two shared resources have the same name in one
	// org, the resource of the older policy wins.
	Policies []model.Policy
}

type tree struct {
	byId     map[string]TreeOrg
	children map[string][]string
}

func newTree(orgs []TreeOrg) tree {

	t := tree{byId: map[string]TreeOrg{}, children: map[string][]string{}}
	for _, o := range orgs {
		t.byId[o.Id] = o
	}
	for _, o := range orgs {
		if o.ParentId != "" {
			t.children[o.ParentId] = append(t.children[o.ParentId], o.Id)
		}
	}
	for id := range t.children {
		sort.Strings(t.children[id])
	}
	return t
}

// subtree returns the org and all orgs below it.
func (t tree) subtree(orgId string) []string {

	result := []string{orgId}
	for i := 0; i < len(result); i++ {
		result = append(result, t.children[result[i]]...)
	}
	return result
}

// descendants returns all orgs below the org.
func (t tree) descendants(orgId string) []string {
	return t.subtree(orgId)[1:]
}

// Reach returns the active orgs that a policy reaches, sorted by depth and then ID.
func Reach(orgs []TreeOrg, p model.Policy) []string {
	return reach(newTree(orgs), p)
}

func reach(t tree, p model.Policy) []string {

	reached := map[string]bool{}
	for _, target := range p.Targets {
		switch target.Scope {
		case model.ScopeAllChildren:
			for _, id := range t.descendants(p.InitiatingOrgId) {
				reached[id] = true
			}
		case model.ScopeOrg:
			if t.byId[target.OrgId].ParentId == p.InitiatingOrgId {
				reached[target.OrgId] = true
			}
		case model.ScopeOrgSubtree:
			if t.byId[target.OrgId].ParentId == p.InitiatingOrgId {
				for _, id := range t.subtree(target.OrgId) {
					reached[id] = true
				}
			}
		}
	}
	result := make([]string, 0, len(reached))
	for id := range reached {
		if org, ok := t.byId[id]; ok && org.Active {
			result = append(result, id)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := t.byId[result[i]], t.byId[result[j]]
		if a.Depth != b.Depth {
			return a.Depth < b.Depth
		}
		return a.Id < b.Id
	})
	return result
}

// ValidatePolicy checks a policy against the tree. It returns a list of problems, which is
// empty when the policy is valid. A request for more than the initiator can give is refused,
// and not trimmed.
func ValidatePolicy(orgs []TreeOrg, p model.Policy) []string {

	t := newTree(orgs)
	var problems []string
	initiator, ok := t.byId[p.InitiatingOrgId]
	if !ok {
		return []string{fmt.Sprintf("the initiating organization '%s' is not known", p.InitiatingOrgId)}
	}
	if len(p.Targets) == 0 {
		problems = append(problems, "at least one target is required")
	}
	for _, target := range p.Targets {
		switch target.Scope {
		case model.ScopeAllChildren:
			if target.OrgId != initiator.Id {
				problems = append(problems, "the ALL_CHILDREN scope must name the initiating organization")
			}
		case model.ScopeOrg, model.ScopeOrgSubtree:
			org, known := t.byId[target.OrgId]
			switch {
			case target.OrgId == "":
				problems = append(problems, fmt.Sprintf("the %s scope requires an org_id", target.Scope))
			case !known:
				problems = append(problems, fmt.Sprintf("the organization '%s' is not known", target.OrgId))
			case org.ParentId != initiator.Id:
				problems = append(problems, fmt.Sprintf(
					"the organization '%s' is not a direct child of the initiating organization", target.OrgId))
			case !org.Active:
				problems = append(problems, fmt.Sprintf("the organization '%s' is not active", target.OrgId))
			}
		default:
			problems = append(problems, fmt.Sprintf("the scope '%s' is not supported", target.Scope))
		}
	}
	return problems
}

// Evaluate returns the state of every shared resource in every org that its policy reaches. The
// share-time check of a rule uses it for the orgs of the tree.
func Evaluate(in EngineInput) []model.State {
	return evaluate(in, "")
}

// EvaluateOrg returns the state of every shared resource that reaches the org. The input needs
// only the ancestor chain of the org, the local resources of the org, and the policies that reach
// it, with their resources.
func EvaluateOrg(in EngineInput, orgId string) []model.State {
	return evaluate(in, orgId)
}

func evaluate(in EngineInput, only string) []model.State {

	t := newTree(in.Orgs)
	attributesById := map[string]AttributeInfo{}
	localAttributes := map[string]map[string]AttributeInfo{} // org -> name -> attribute
	for _, a := range in.Attributes {
		attributesById[a.Id] = a
		if localAttributes[a.OwnerOrgId] == nil {
			localAttributes[a.OwnerOrgId] = map[string]AttributeInfo{}
		}
		localAttributes[a.OwnerOrgId][a.Name] = a
	}
	rulesById := map[string]RuleInfo{}
	localRules := map[string]map[string]RuleInfo{} // org -> property name -> rule
	for _, r := range in.Rules {
		rulesById[r.Id] = r
		if localRules[r.OwnerOrgId] == nil {
			localRules[r.OwnerOrgId] = map[string]RuleInfo{}
		}
		localRules[r.OwnerOrgId][r.PropertyName] = r
	}

	var states []model.State

	// Attributes first, because the rule states depend on the effective schema of each org.
	activeShared := map[string]map[string]AttributeInfo{} // org -> name -> active shared attribute
	for _, p := range in.Policies {
		if p.ResourceType != model.ResourceSchemaAttribute {
			continue
		}
		attr, ok := attributesById[p.ResourceId]
		if !ok {
			continue
		}
		for _, orgId := range reach(t, p) {
			if only != "" && orgId != only {
				continue
			}
			state := model.State{ResourceType: p.ResourceType, ResourceId: p.ResourceId, OrgId: orgId,
				OrgHandle: t.byId[orgId].Handle, State: model.StateActive}
			if local, exists := localAttributes[orgId][attr.Name]; exists {
				state.State, state.Reason, state.ConflictingResourceId = model.StateConflicted,
					model.ReasonLocalNameConflict, local.Id
			} else if other, exists := activeShared[orgId][attr.Name]; exists && other.Id != attr.Id {
				state.State, state.Reason, state.ConflictingResourceId = model.StateConflicted,
					model.ReasonSharedNameConflict, other.Id
			} else {
				if activeShared[orgId] == nil {
					activeShared[orgId] = map[string]AttributeInfo{}
				}
				activeShared[orgId][attr.Name] = attr
			}
			states = append(states, state)
		}
	}

	// Rules.
	activeSharedRules := map[string]map[string]RuleInfo{} // org -> property name -> active shared rule
	for _, p := range in.Policies {
		if p.ResourceType != model.ResourceUnificationRule {
			continue
		}
		rule, ok := rulesById[p.ResourceId]
		if !ok {
			continue
		}
		for _, orgId := range reach(t, p) {
			if only != "" && orgId != only {
				continue
			}
			state := model.State{ResourceType: p.ResourceType, ResourceId: p.ResourceId, OrgId: orgId,
				OrgHandle: t.byId[orgId].Handle, State: model.StateActive}
			if local, exists := localRules[orgId][rule.PropertyName]; exists {
				state.State, state.Reason, state.ConflictingResourceId = model.StateConflicted,
					model.ReasonLocalNameConflict, local.Id
			} else if other, exists := activeSharedRules[orgId][rule.PropertyName]; exists && other.Id != rule.Id {
				state.State, state.Reason, state.ConflictingResourceId = model.StateConflicted,
					model.ReasonSharedNameConflict, other.Id
			} else if !hasCompatibleAttribute(rule, attributesById, localAttributes[orgId], activeShared[orgId]) {
				state.State, state.Reason = model.StateInactiveMissingAttribute, model.ReasonMissingAttribute
			} else {
				if activeSharedRules[orgId] == nil {
					activeSharedRules[orgId] = map[string]RuleInfo{}
				}
				activeSharedRules[orgId][rule.PropertyName] = rule
			}
			states = append(states, state)
		}
	}
	return states
}

// hasCompatibleAttribute reports whether the org sees an attribute with the name of the rule
// property and the same value type as the attribute of the rule in its owner org.
func hasCompatibleAttribute(rule RuleInfo, attributesById map[string]AttributeInfo,
	local map[string]AttributeInfo, shared map[string]AttributeInfo) bool {

	candidate, ok := local[rule.PropertyName]
	if !ok {
		candidate, ok = shared[rule.PropertyName]
	}
	if !ok || candidate.ValueType == constants.ComplexDataType {
		return false
	}
	ownerAttribute, known := attributesById[rule.PropertyId]
	if !known {
		return true
	}
	return strings.EqualFold(ownerAttribute.ValueType, candidate.ValueType)
}
