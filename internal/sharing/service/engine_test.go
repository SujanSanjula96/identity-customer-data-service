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
	"reflect"
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/sharing/model"
)

// The test tree:
//
//	R
//	├── A
//	│   ├── A1
//	│   │   └── A1x
//	│   └── A2 (disabled)
//	└── B
func testTree() []TreeOrg {
	return []TreeOrg{
		{Id: "R", Handle: "r", Active: true},
		{Id: "A", Handle: "a", ParentId: "R", Active: true},
		{Id: "B", Handle: "b", ParentId: "R", Active: true},
		{Id: "A1", Handle: "a1", ParentId: "A", Active: true},
		{Id: "A2", Handle: "a2", ParentId: "A", Active: false},
		{Id: "A1x", Handle: "a1x", ParentId: "A1", Active: true},
	}
}

func policy(resourceType, resourceId, initiator string, targets []model.Target) model.Policy {
	return model.Policy{PolicyId: resourceId + "-p", ResourceType: resourceType, ResourceId: resourceId,
		OwningOrgId: initiator, InitiatingOrgId: initiator, Targets: targets}
}

func allChildren(initiator string) []model.Target {
	return []model.Target{{Scope: model.ScopeAllChildren, OrgId: initiator}}
}

func TestReach(t *testing.T) {

	cases := []struct {
		name     string
		policy   model.Policy
		expected []string
	}{
		{"all children skips inactive orgs",
			policy(model.ResourceSchemaAttribute, "x", "R", allChildren("R")),
			[]string{"A", "B", "A1", "A1x"}},
		{"one child only",
			policy(model.ResourceSchemaAttribute, "x", "R", []model.Target{{Scope: model.ScopeOrg, OrgId: "A"}}),
			[]string{"A"}},
		{"child subtree",
			policy(model.ResourceSchemaAttribute, "x", "R", []model.Target{{Scope: model.ScopeOrgSubtree, OrgId: "A"}}),
			[]string{"A", "A1", "A1x"}},
		{"a named target that is not a direct child reaches nothing",
			policy(model.ResourceSchemaAttribute, "x", "R", []model.Target{{Scope: model.ScopeOrg, OrgId: "A1"}}),
			[]string{}},
		{"a sub org shares with its own subtree",
			policy(model.ResourceSchemaAttribute, "x", "A", allChildren("A")),
			[]string{"A1", "A1x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Reach(testTree(), c.policy); !reflect.DeepEqual(got, c.expected) {
				t.Fatalf("expected %v, got %v", c.expected, got)
			}
		})
	}
}

func TestValidatePolicy(t *testing.T) {

	cases := []struct {
		name    string
		policy  model.Policy
		problem string
	}{
		{"valid", policy(model.ResourceSchemaAttribute, "x", "R",
			[]model.Target{{Scope: model.ScopeOrgSubtree, OrgId: "A"}}), ""},
		{"no targets", policy(model.ResourceSchemaAttribute, "x", "R", nil), "at least one target"},
		{"not a direct child", policy(model.ResourceSchemaAttribute, "x", "R",
			[]model.Target{{Scope: model.ScopeOrg, OrgId: "A1"}}), "not a direct child"},
		{"inactive child", policy(model.ResourceSchemaAttribute, "x", "A",
			[]model.Target{{Scope: model.ScopeOrg, OrgId: "A2"}}), "not active"},
		{"unknown scope", policy(model.ResourceSchemaAttribute, "x", "R",
			[]model.Target{{Scope: "ALL_OUS"}}), "not supported"},
		{"all children of another org", policy(model.ResourceSchemaAttribute, "x", "R",
			allChildren("A")), "must name the initiating organization"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			problems := ValidatePolicy(testTree(), c.policy)
			if c.problem == "" {
				if len(problems) != 0 {
					t.Fatalf("expected no problems, got %v", problems)
				}
				return
			}
			if len(problems) == 0 || !strings.Contains(strings.Join(problems, ";"), c.problem) {
				t.Fatalf("expected a problem with %q, got %v", c.problem, problems)
			}
		})
	}
}

func statesByOrg(states []model.State, resourceId string) map[string]model.State {
	result := map[string]model.State{}
	for _, s := range states {
		if s.ResourceId == resourceId {
			result[s.OrgId] = s
		}
	}
	return result
}

func TestEvaluateAttributeConflicts(t *testing.T) {

	in := EngineInput{
		Orgs: testTree(),
		Attributes: []AttributeInfo{
			{Id: "root-tier", Name: "traits.tier", ValueType: "string", OwnerOrgId: "R"},
			{Id: "b-tier", Name: "traits.tier", ValueType: "string", OwnerOrgId: "B"},
			{Id: "a-tier", Name: "traits.tier", ValueType: "string", OwnerOrgId: "A"},
		},
		// In A1 and A1x both shared attributes reach. The nearest owner (A) wins.
		Policies: []model.Policy{
			policy(model.ResourceSchemaAttribute, "root-tier", "R", allChildren("R")),
			policy(model.ResourceSchemaAttribute, "a-tier", "A", allChildren("A")),
		},
	}
	states := Evaluate(in)

	root := statesByOrg(states, "root-tier")
	if root["A"].State != model.StateConflicted || root["A"].Reason != model.ReasonLocalNameConflict ||
		root["A"].ConflictingResourceId != "a-tier" {
		t.Errorf("expected a local conflict in A, got %+v", root["A"])
	}
	if root["B"].State != model.StateConflicted || root["B"].ConflictingResourceId != "b-tier" {
		t.Errorf("expected a local conflict in B, got %+v", root["B"])
	}
	// The nearest owner wins: in A1 and A1x, the attribute of A wins over the attribute of the root.
	if root["A1"].State != model.StateConflicted || root["A1"].Reason != model.ReasonSharedNameConflict ||
		root["A1"].ConflictingResourceId != "a-tier" {
		t.Errorf("expected the root attribute to lose to the nearer owner in A1, got %+v", root["A1"])
	}
	a := statesByOrg(states, "a-tier")
	if a["A1"].State != model.StateActive || a["A1x"].State != model.StateActive {
		t.Errorf("expected the attribute of A to be active in A1 and A1x, got %+v %+v", a["A1"], a["A1x"])
	}
}

func TestEvaluateRuleDependencies(t *testing.T) {

	all := allChildren("R")
	in := EngineInput{
		Orgs: testTree(),
		Attributes: []AttributeInfo{
			{Id: "root-email", Name: "identity_attributes.email", ValueType: "string", OwnerOrgId: "R"},
			{Id: "a-email", Name: "identity_attributes.email", ValueType: "string", OwnerOrgId: "A"},
			{Id: "a1-email", Name: "identity_attributes.email", ValueType: "string", OwnerOrgId: "A1"},
			// B has no email attribute, and A1x has one with another type.
			{Id: "a1x-email", Name: "identity_attributes.email", ValueType: "integer", OwnerOrgId: "A1x"},
			{Id: "root-loyalty", Name: "traits.loyalty_id", ValueType: "string", OwnerOrgId: "R"},
		},
		Rules: []RuleInfo{
			{Id: "email-rule", PropertyName: "identity_attributes.email", PropertyId: "root-email", OwnerOrgId: "R"},
			{Id: "loyalty-rule", PropertyName: "traits.loyalty_id", PropertyId: "root-loyalty", OwnerOrgId: "R"},
			{Id: "a1-local-email", PropertyName: "identity_attributes.email", PropertyId: "a1-email", OwnerOrgId: "A1"},
		},
		Policies: []model.Policy{
			policy(model.ResourceSchemaAttribute, "root-loyalty", "R", []model.Target{{Scope: model.ScopeOrg,
				OrgId: "A"}}),
			policy(model.ResourceUnificationRule, "email-rule", "R", all),
			policy(model.ResourceUnificationRule, "loyalty-rule", "R", all),
		},
	}
	states := Evaluate(in)

	email := statesByOrg(states, "email-rule")
	if email["A"].State != model.StateActive {
		t.Errorf("expected the email rule to be active in A, got %+v", email["A"])
	}
	if email["B"].State != model.StateInactiveMissingAttribute {
		t.Errorf("expected a missing attribute in B, got %+v", email["B"])
	}
	if email["A1"].State != model.StateConflicted || email["A1"].ConflictingResourceId != "a1-local-email" {
		t.Errorf("expected a local rule conflict in A1, got %+v", email["A1"])
	}
	if email["A1x"].State != model.StateInactiveMissingAttribute {
		t.Errorf("expected an incompatible type in A1x to count as missing, got %+v", email["A1x"])
	}
	loyalty := statesByOrg(states, "loyalty-rule")
	if loyalty["A"].State != model.StateActive {
		t.Errorf("expected the loyalty rule to use the shared attribute in A, got %+v", loyalty["A"])
	}
	if loyalty["A1"].State != model.StateInactiveMissingAttribute {
		t.Errorf("expected a missing attribute in A1, because the attribute is shared with A only, got %+v",
			loyalty["A1"])
	}
}

// The read for one org loads only its ancestor chain, its own resources, and the resources of the
// policies that reach it. The result must be the same as for the full tree.
func TestEvaluateOrgMatchesTree(t *testing.T) {

	attrs := []AttributeInfo{
		{Id: "root-tier", Name: "traits.tier", ValueType: "string", OwnerOrgId: "R"},
		{Id: "a-tier", Name: "traits.tier", ValueType: "string", OwnerOrgId: "A"},
		{Id: "b-tier", Name: "traits.tier", ValueType: "string", OwnerOrgId: "B"},
	}
	policies := []model.Policy{
		policy(model.ResourceSchemaAttribute, "root-tier", "R", allChildren("R")),
		policy(model.ResourceSchemaAttribute, "a-tier", "A", allChildren("A")),
	}
	full := Evaluate(EngineInput{Orgs: testTree(), Attributes: attrs, Policies: policies})

	chain := []TreeOrg{testTree()[0], testTree()[1], testTree()[3], testTree()[5]} // R, A, A1, A1x
	chainAttrs := attrs[:2]                                                        // B is not in the chain
	got := EvaluateOrg(EngineInput{Orgs: chain, Attributes: chainAttrs, Policies: policies}, "A1x")

	var expected []model.State
	for _, s := range full {
		if s.OrgId == "A1x" {
			expected = append(expected, s)
		}
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected %+v, got %+v", expected, got)
	}
}

func TestToTargets(t *testing.T) {

	cases := []struct {
		name     string
		scope    *model.TargetOrgScope
		expected []model.Target
		problem  string
	}{
		{"all children", &model.TargetOrgScope{AllChildren: true},
			[]model.Target{{Scope: model.ScopeAllChildren, OrgId: "R"}}, ""},
		{"mixed children", &model.TargetOrgScope{ChildOrgs: []model.ChildOrg{{OrgId: "A", AllChildren: true},
			{OrgId: "B"}}}, []model.Target{{Scope: model.ScopeOrgSubtree, OrgId: "A"},
			{Scope: model.ScopeOrg, OrgId: "B"}}, ""},
		{"no scope", nil, nil, "required"},
		{"both modes", &model.TargetOrgScope{AllChildren: true, ChildOrgs: []model.ChildOrg{{OrgId: "A"}}}, nil,
			"not both"},
		{"empty", &model.TargetOrgScope{}, nil, "at least one"},
		{"one org twice", &model.TargetOrgScope{ChildOrgs: []model.ChildOrg{{OrgId: "A"}, {OrgId: "A",
			AllChildren: true}}}, nil, "more than once"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, problems := ToTargets(c.scope, "R")
			if c.problem != "" {
				if !strings.Contains(strings.Join(problems, ";"), c.problem) {
					t.Fatalf("expected a problem with %q, got %v", c.problem, problems)
				}
				return
			}
			if len(problems) > 0 || !reflect.DeepEqual(got, c.expected) {
				t.Fatalf("expected %v, got %v %v", c.expected, got, problems)
			}
			if back := ToTargetOrgScope(got); !reflect.DeepEqual(&back, c.scope) {
				t.Fatalf("expected the same scope back, got %+v", back)
			}
		})
	}
}

// Two shared rules on one property: the rule of the farthest owner wins, as in the run order.
func TestEvaluateRuleFarthestOwnerWins(t *testing.T) {

	in := EngineInput{
		Orgs: testTree(),
		Attributes: []AttributeInfo{
			{Id: "root-email", Name: "identity_attributes.email", ValueType: "string", OwnerOrgId: "R"},
			{Id: "a1-email", Name: "identity_attributes.email", ValueType: "string", OwnerOrgId: "A1"},
		},
		Rules: []RuleInfo{
			{Id: "root-rule", PropertyName: "identity_attributes.email", PropertyId: "root-email", OwnerOrgId: "R"},
			{Id: "a-rule", PropertyName: "identity_attributes.email", PropertyId: "root-email", OwnerOrgId: "A"},
		},
		Policies: []model.Policy{
			policy(model.ResourceUnificationRule, "a-rule", "A", allChildren("A")),
			policy(model.ResourceUnificationRule, "root-rule", "R", allChildren("R")),
		},
	}
	states := Evaluate(in)
	if s := statesByOrg(states, "root-rule")["A1"]; s.State != model.StateActive {
		t.Errorf("expected the rule of the root to win in A1, got %+v", s)
	}
	if s := statesByOrg(states, "a-rule")["A1"]; s.State != model.StateConflicted ||
		s.ConflictingResourceId != "root-rule" {
		t.Errorf("expected the rule of A to lose in A1, got %+v", s)
	}
}

// A share applies only in enabled orgs, and passes down through an org that is not enabled.
func TestReachSkipsOrgsThatAreNotEnabled(t *testing.T) {

	orgs := testTree()
	for i := range orgs {
		if orgs[i].Id == "A" {
			orgs[i].NotEnabled = true
		}
	}
	got := Reach(orgs, policy(model.ResourceSchemaAttribute, "x", "R", allChildren("R")))
	if expected := []string{"B", "A1", "A1x"}; !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

// Organization access can select any descendant, when each org between the root and it is selected.
func TestValidateOrgAccess(t *testing.T) {

	access := func(targets ...model.Target) model.Policy {
		return model.Policy{PolicyId: "oa", ResourceType: model.ResourceOrganizationAccess,
			ResourceId: model.OrganizationAccessResourceId, OwningOrgId: "R", InitiatingOrgId: "R", Targets: targets}
	}
	cases := []struct {
		name    string
		policy  model.Policy
		problem string
	}{
		{"a grandchild with its parent", access(model.Target{Scope: model.ScopeOrg, OrgId: "A"},
			model.Target{Scope: model.ScopeOrg, OrgId: "A1"}), ""},
		{"a grandchild without its parent", access(model.Target{Scope: model.ScopeOrg, OrgId: "A1"}),
			"'A' above it is not"},
		{"a grandchild below a selected subtree", access(model.Target{Scope: model.ScopeOrgSubtree, OrgId: "A"},
			model.Target{Scope: model.ScopeOrg, OrgId: "A1x"}), ""},
		{"a great-grandchild with a gap", access(model.Target{Scope: model.ScopeOrg, OrgId: "A"},
			model.Target{Scope: model.ScopeOrg, OrgId: "A1x"}), "'A1' above it is not"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			problems := ValidatePolicy(testTree(), c.policy)
			if c.problem == "" {
				if len(problems) != 0 {
					t.Fatalf("expected no problems, got %v", problems)
				}
				return
			}
			if !strings.Contains(strings.Join(problems, ";"), c.problem) {
				t.Fatalf("expected a problem with %q, got %v", c.problem, problems)
			}
		})
	}
	got := Reach(testTree(), access(model.Target{Scope: model.ScopeOrg, OrgId: "A"},
		model.Target{Scope: model.ScopeOrg, OrgId: "A1"}))
	if expected := []string{"A", "A1"}; !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}
