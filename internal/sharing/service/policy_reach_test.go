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

	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
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
func testTree() []orgModel.Organization {

	org := func(id, parent, status string) orgModel.Organization {
		return orgModel.Organization{OrgId: id, OrgHandle: strings.ToLower(id), ParentOrgId: parent,
			PrimaryOrgId: "R", Status: status}
	}
	return []orgModel.Organization{
		org("R", "", orgModel.StatusActive),
		org("A", "R", orgModel.StatusActive),
		org("B", "R", orgModel.StatusActive),
		org("A1", "A", orgModel.StatusActive),
		org("A2", "A", orgModel.StatusDisabled),
		org("A1x", "A1", orgModel.StatusActive),
	}
}

// ancestorsIn returns the ancestors of the organization in the tree, the parent first.
func ancestorsIn(tree []orgModel.Organization, orgId string) []orgModel.Ancestor {

	byId := map[string]orgModel.Organization{}
	for _, o := range tree {
		byId[o.OrgId] = o
	}
	var result []orgModel.Ancestor
	for id := byId[orgId].ParentOrgId; id != ""; id = byId[id].ParentOrgId {
		result = append(result, orgModel.Ancestor{Organization: byId[id], Depth: len(result) + 1})
	}
	return result
}

// reachedOrgs returns the organizations of the tree that the policy reaches, in tree order.
func reachedOrgs(p model.Policy) []string {

	tree := testTree()
	reached := []string{}
	for _, org := range tree {
		if policyReachesOrg(p, org, ancestorsIn(tree, org.OrgId)) {
			reached = append(reached, org.OrgId)
		}
	}
	return reached
}

func policy(initiator string, targets ...model.Target) model.Policy {
	return model.Policy{PolicyId: "p", ResourceType: "TEST_RESOURCE", ResourceId: "x", OwningOrgId: initiator,
		InitiatingOrgId: initiator, Targets: targets}
}

func TestPolicyReachesOrg(t *testing.T) {

	cases := []struct {
		name     string
		policy   model.Policy
		expected []string
	}{
		{"all children", policy("R", model.Target{Scope: model.ScopeAllChildren, OrgId: "R"}),
			[]string{"A", "B", "A1", "A2", "A1x"}},
		{"one child only", policy("R", model.Target{Scope: model.ScopeOrg, OrgId: "A"}), []string{"A"}},
		{"child subtree", policy("R", model.Target{Scope: model.ScopeOrgSubtree, OrgId: "A"}),
			[]string{"A", "A1", "A2", "A1x"}},
		{"a child and a child subtree", policy("R", model.Target{Scope: model.ScopeOrg, OrgId: "A"},
			model.Target{Scope: model.ScopeOrgSubtree, OrgId: "B"}), []string{"A", "B"}},
		{"a named target that is not a direct child reaches nothing",
			policy("R", model.Target{Scope: model.ScopeOrg, OrgId: "A1"}), []string{}},
		{"a sub org shares with its own subtree", policy("A", model.Target{Scope: model.ScopeAllChildren, OrgId: "A"}),
			[]string{"A1", "A2", "A1x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reachedOrgs(c.policy); !reflect.DeepEqual(got, c.expected) {
				t.Fatalf("expected %v, got %v", c.expected, got)
			}
		})
	}
}

func TestTargetProblems(t *testing.T) {

	cases := []struct {
		name    string
		policy  model.Policy
		problem string
	}{
		{"a child subtree", policy("R", model.Target{Scope: model.ScopeOrgSubtree, OrgId: "A"}), ""},
		{"a child", policy("R", model.Target{Scope: model.ScopeOrg, OrgId: "A"}), ""},
		{"no targets", policy("R"), "at least one target"},
		{"not a direct child", policy("R", model.Target{Scope: model.ScopeOrg, OrgId: "A1"}), "not a direct child"},
		{"a grandchild with its parent", policy("R", model.Target{Scope: model.ScopeOrg, OrgId: "A"},
			model.Target{Scope: model.ScopeOrg, OrgId: "A1"}), "'A1' is not a direct child"},
		{"inactive child", policy("A", model.Target{Scope: model.ScopeOrg, OrgId: "A2"}), "not active"},
		{"unknown organization", policy("R", model.Target{Scope: model.ScopeOrg, OrgId: "C"}), "'C' is not known"},
		{"unknown scope", policy("R", model.Target{Scope: "ALL_OUS"}), "not supported"},
		{"all children of another org", policy("R", model.Target{Scope: model.ScopeAllChildren, OrgId: "A"}),
			"must name the initiating organization"},
	}
	tree := testTree()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var initiator orgModel.Organization
			for _, o := range tree {
				if o.OrgId == c.policy.InitiatingOrgId {
					initiator = o
				}
			}
			problems := targetProblems(initiator, tree, c.policy.Targets)
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
}
