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

// TreeOrg is an org of the tree, as the engine needs it. CDS stores no depth: the engine computes
// the level of each org from the parent links.
type TreeOrg struct {
	Id       string
	Handle   string
	ParentId string
	Active   bool
	// NotEnabled is true when the organization access of the root does not reach the org. A share
	// does not apply in such an org, but it passes down to the orgs below it.
	NotEnabled bool
}

// AttributeInfo is a profile schema attribute, as the engine needs it. AppId is the application
// identifier of an application data attribute, and empty for the other scopes.
type AttributeInfo struct {
	Id         string
	Name       string
	ValueType  string
	OwnerOrgId string
	AppId      string
}

// nameKey is the key of the attribute for name conflicts. Application data attributes are scoped
// to their app, so two apps can use the same attribute name.
func (a AttributeInfo) nameKey() string {

	if a.AppId == "" {
		return a.Name
	}
	return a.AppId + "\x00" + a.Name
}

// RuleInfo is a unification rule, as the engine needs it.
type RuleInfo struct {
	Id           string
	PropertyName string
	PropertyId   string
	OwnerOrgId   string
}

// EngineInput is everything the engine reads for one tree. IdentitySourceOrgId is the org whose
// identity attributes the other orgs of the tree inherit (R-017). When it is empty, each org sees
// only its own.
type EngineInput struct {
	Orgs                []TreeOrg
	Attributes          []AttributeInfo
	Rules               []RuleInfo
	Policies            []model.Policy
	IdentitySourceOrgId string
}

type tree struct {
	byId     map[string]TreeOrg
	children map[string][]string
}

// level returns the number of parents of the org that the tree has.
func (t tree) level(orgId string) int {

	level := 0
	for id := t.byId[orgId].ParentId; id != "" && level < 64; id = t.byId[id].ParentId {
		if _, ok := t.byId[id]; !ok {
			break
		}
		level++
	}
	return level
}

// isBelow reports whether the org is a descendant of the ancestor.
func (t tree) isBelow(orgId, ancestorId string) bool {

	for id, guard := t.byId[orgId].ParentId, 0; id != "" && guard < 64; id, guard = t.byId[id].ParentId, guard+1 {
		if id == ancestorId {
			return true
		}
	}
	return false
}

// isTargetOf reports whether a named target can be reached by the policy. A share policy names only
// direct children of the initiating org. Organization access names any descendant of the root.
func (t tree) isTargetOf(orgId string, p model.Policy) bool {

	if p.ResourceType == model.ResourceOrganizationAccess {
		return t.isBelow(orgId, p.InitiatingOrgId)
	}
	return t.byId[orgId].ParentId == p.InitiatingOrgId
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

// Reach returns the active, enabled orgs that a policy reaches, sorted by level and then ID.
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
			if t.isTargetOf(target.OrgId, p) {
				reached[target.OrgId] = true
			}
		case model.ScopeOrgSubtree:
			if t.isTargetOf(target.OrgId, p) {
				for _, id := range t.subtree(target.OrgId) {
					reached[id] = true
				}
			}
		}
	}
	result := make([]string, 0, len(reached))
	for id := range reached {
		if org, ok := t.byId[id]; ok && org.Active && !org.NotEnabled {
			result = append(result, id)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := t.level(result[i]), t.level(result[j])
		if a != b {
			return a < b
		}
		return result[i] < result[j]
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
			case !t.isTargetOf(target.OrgId, p) && p.ResourceType == model.ResourceOrganizationAccess:
				problems = append(problems, fmt.Sprintf(
					"the organization '%s' is not a descendant of the root organization", target.OrgId))
			case !t.isTargetOf(target.OrgId, p):
				problems = append(problems, fmt.Sprintf(
					"the organization '%s' is not a direct child of the initiating organization", target.OrgId))
			case !org.Active:
				problems = append(problems, fmt.Sprintf("the organization '%s' is not active", target.OrgId))
			}
		default:
			problems = append(problems, fmt.Sprintf("the scope '%s' is not supported", target.Scope))
		}
	}
	if len(problems) == 0 && p.ResourceType == model.ResourceOrganizationAccess {
		problems = checkSelectedAncestors(t, p)
	}
	return problems
}

// checkSelectedAncestors checks that each org between the root and a selected org is selected too.
func checkSelectedAncestors(t tree, p model.Policy) []string {

	selected := map[string]bool{}
	for _, id := range reach(t, p) {
		selected[id] = true
	}
	var problems []string
	for _, target := range p.Targets {
		if target.Scope == model.ScopeAllChildren {
			continue
		}
		for id := t.byId[target.OrgId].ParentId; id != "" && id != p.InitiatingOrgId; id = t.byId[id].ParentId {
			if !selected[id] {
				problems = append(problems, fmt.Sprintf("the organization '%s' is selected, but the organization "+
					"'%s' above it is not. Select each organization between the root and a selected organization",
					target.OrgId, id))
				break
			}
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
	localAttributes := map[string]map[string]AttributeInfo{} // org -> name key -> attribute
	for _, a := range in.Attributes {
		attributesById[a.Id] = a
		if localAttributes[a.OwnerOrgId] == nil {
			localAttributes[a.OwnerOrgId] = map[string]AttributeInfo{}
		}
		localAttributes[a.OwnerOrgId][a.nameKey()] = a
	}
	inherited := map[string]AttributeInfo{} // name -> identity attribute of the source org
	for key, a := range localAttributes[in.IdentitySourceOrgId] {
		if in.IdentitySourceOrgId != "" && strings.HasPrefix(a.Name, constants.IdentityAttributes+".") {
			inherited[key] = a
		}
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

	// Precedence (R-009). For attributes, the local attribute of the org wins, and then the shared
	// attribute of the nearest owner: the policies of deeper owners come first. For rules, the local
	// rule wins, and then the shared rule of the farthest owner, which is also the run order.
	policies := append([]model.Policy(nil), in.Policies...)
	sort.SliceStable(policies, func(i, j int) bool {
		pi, pj := policies[i], policies[j]
		if pi.ResourceType != pj.ResourceType {
			return pi.ResourceType < pj.ResourceType
		}
		a, b := t.level(pi.OwningOrgId), t.level(pj.OwningOrgId)
		if a != b {
			if pi.ResourceType == model.ResourceUnificationRule {
				return a < b
			}
			return a > b
		}
		return pi.PolicyId < pj.PolicyId
	})

	// Attributes first, because the rule states depend on the effective schema of each org.
	activeShared := map[string]map[string]AttributeInfo{} // org -> name key -> active shared attribute
	for _, p := range policies {
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
			key := attr.nameKey()
			if local, exists := localAttributes[orgId][key]; exists {
				state.State, state.Reason, state.ConflictingResourceId = model.StateConflicted,
					model.ReasonLocalNameConflict, local.Id
			} else if other, exists := activeShared[orgId][key]; exists && other.Id != attr.Id {
				state.State, state.Reason, state.ConflictingResourceId = model.StateConflicted,
					model.ReasonSharedNameConflict, other.Id
			} else {
				if activeShared[orgId] == nil {
					activeShared[orgId] = map[string]AttributeInfo{}
				}
				activeShared[orgId][key] = attr
			}
			states = append(states, state)
		}
	}

	// Rules.
	activeSharedRules := map[string]map[string]RuleInfo{} // org -> property name -> active shared rule
	for _, p := range policies {
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
			} else if !hasCompatibleAttribute(rule, attributesById, localAttributes[orgId], inherited,
				activeShared[orgId]) {
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
// property and the same value type as the attribute of the rule in its owner org. The org sees its
// local attributes, the inherited identity attributes, and its active shared attributes.
func hasCompatibleAttribute(rule RuleInfo, attributesById map[string]AttributeInfo,
	local, inherited, shared map[string]AttributeInfo) bool {

	candidate, ok := local[rule.PropertyName]
	if !ok {
		candidate, ok = inherited[rule.PropertyName]
	}
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
