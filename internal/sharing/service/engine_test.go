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
		{Id: "R", Handle: "r", Path: "/R/", Depth: 0, Active: true},
		{Id: "A", Handle: "a", ParentId: "R", Path: "/R/A/", Depth: 1, Active: true},
		{Id: "B", Handle: "b", ParentId: "R", Path: "/R/B/", Depth: 1, Active: true},
		{Id: "A1", Handle: "a1", ParentId: "A", Path: "/R/A/A1/", Depth: 2, Active: true},
		{Id: "A2", Handle: "a2", ParentId: "A", Path: "/R/A/A2/", Depth: 2, Active: false},
		{Id: "A1x", Handle: "a1x", ParentId: "A1", Path: "/R/A/A1/A1x/", Depth: 3, Active: true},
	}
}

func policy(resourceType, resourceId, initiator string, targets []model.Target, excluded ...string) model.Policy {
	return model.Policy{PolicyId: resourceId + "-p", ResourceType: resourceType, ResourceId: resourceId,
		OwnerOrgId: initiator, InitiatingOrgId: initiator, Stage: model.StageShare, Targets: targets,
		ExcludedOrgIds: excluded}
}

func TestReach(t *testing.T) {

	cases := []struct {
		name     string
		policy   model.Policy
		expected []string
	}{
		{"all descendants skips inactive orgs",
			policy(model.ResourceSchemaAttribute, "x", "R", []model.Target{{Scope: model.ScopeAllDescendants}}),
			[]string{"A", "B", "A1", "A1x"}},
		{"one child only",
			policy(model.ResourceSchemaAttribute, "x", "R", []model.Target{{Scope: model.ScopeOrg, OrgId: "A"}}),
			[]string{"A"}},
		{"child subtree",
			policy(model.ResourceSchemaAttribute, "x", "R", []model.Target{{Scope: model.ScopeOrgSubtree, OrgId: "A"}}),
			[]string{"A", "A1", "A1x"}},
		{"an exclusion removes the full subtree",
			policy(model.ResourceSchemaAttribute, "x", "R", []model.Target{{Scope: model.ScopeAllDescendants}}, "A1"),
			[]string{"A", "B"}},
		{"a named target that is not a direct child reaches nothing",
			policy(model.ResourceSchemaAttribute, "x", "R", []model.Target{{Scope: model.ScopeOrg, OrgId: "A1"}}),
			[]string{}},
		{"a sub org shares with its own subtree",
			policy(model.ResourceSchemaAttribute, "x", "A", []model.Target{{Scope: model.ScopeAllDescendants}}),
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
			[]model.Target{{Scope: model.ScopeOrgSubtree, OrgId: "A"}}, "A1"), ""},
		{"no targets", policy(model.ResourceSchemaAttribute, "x", "R", nil), "at least one target"},
		{"not a direct child", policy(model.ResourceSchemaAttribute, "x", "R",
			[]model.Target{{Scope: model.ScopeOrg, OrgId: "A1"}}), "not a direct child"},
		{"inactive child", policy(model.ResourceSchemaAttribute, "x", "A",
			[]model.Target{{Scope: model.ScopeOrg, OrgId: "A2"}}), "not active"},
		{"unknown scope", policy(model.ResourceSchemaAttribute, "x", "R",
			[]model.Target{{Scope: "ALL_OUS"}}), "not supported"},
		{"org_id on all descendants", policy(model.ResourceSchemaAttribute, "x", "R",
			[]model.Target{{Scope: model.ScopeAllDescendants, OrgId: "A"}}), "does not take an org_id"},
		{"exclusion outside the reach", policy(model.ResourceSchemaAttribute, "x", "R",
			[]model.Target{{Scope: model.ScopeOrg, OrgId: "A"}}, "B"), "not in the reach"},
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

	all := []model.Target{{Scope: model.ScopeAllDescendants}}
	in := EngineInput{
		Orgs: testTree(),
		Attributes: []AttributeInfo{
			{Id: "root-tier", Name: "traits.tier", ValueType: "string", OwnerOrgId: "R"},
			{Id: "b-tier", Name: "traits.tier", ValueType: "string", OwnerOrgId: "B"},
			{Id: "a-tier", Name: "traits.tier", ValueType: "string", OwnerOrgId: "A"},
		},
		// The root policy is older, so it wins in orgs where both shared attributes reach.
		Policies: []model.Policy{
			policy(model.ResourceSchemaAttribute, "root-tier", "R", all),
			policy(model.ResourceSchemaAttribute, "a-tier", "A", all),
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
	// A conflicted org still passes the share to its descendants: each gets its own state.
	if root["A1"].State != model.StateActive || root["A1x"].State != model.StateActive {
		t.Errorf("expected the root attribute to be active in A1 and A1x, got %+v %+v", root["A1"], root["A1x"])
	}
	a := statesByOrg(states, "a-tier")
	if a["A1"].State != model.StateConflicted || a["A1"].Reason != model.ReasonSharedNameConflict ||
		a["A1"].ConflictingResourceId != "root-tier" {
		t.Errorf("expected a shared conflict in A1, got %+v", a["A1"])
	}
}

func TestEvaluateRuleDependencies(t *testing.T) {

	all := []model.Target{{Scope: model.ScopeAllDescendants}}
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
