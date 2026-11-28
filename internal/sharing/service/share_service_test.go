/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
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
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/sharing/model"
)

func TestToTargets(t *testing.T) {
	selected := func(orgs ...model.ChildOrg) *model.ChildOrgs {
		return &model.ChildOrgs{Mode: model.ChildOrgsModeSelected, Orgs: orgs}
	}
	cases := []struct {
		name      string
		childOrgs *model.ChildOrgs
		expected  []model.Target
		problems  []string
	}{
		{"all", &model.ChildOrgs{Mode: model.ChildOrgsModeAll},
			[]model.Target{{Scope: model.ScopeAllChildren, OrgID: "R"}}, nil},
		{"selected org", selected(model.ChildOrg{OrgID: "B", Reach: model.ReachOrg}),
			[]model.Target{{Scope: model.ScopeOrg, OrgID: "B"}}, nil},
		{"selected subtree", selected(model.ChildOrg{OrgID: "A", Reach: model.ReachSubtree}),
			[]model.Target{{Scope: model.ScopeOrgSubtree, OrgID: "A"}}, nil},
		{"selected subtree and org", selected(model.ChildOrg{OrgID: "A", Reach: model.ReachSubtree},
			model.ChildOrg{OrgID: "B", Reach: model.ReachOrg}),
			[]model.Target{{Scope: model.ScopeOrgSubtree, OrgID: "A"}, {Scope: model.ScopeOrg, OrgID: "B"}}, nil},
		{"no child_orgs", nil, nil, []string{"child_orgs is required"}},
		{"no mode", &model.ChildOrgs{Orgs: []model.ChildOrg{{OrgID: "A", Reach: model.ReachOrg}}}, nil,
			[]string{"child_orgs.mode is required"}},
		{"unknown mode", &model.ChildOrgs{Mode: "some"}, nil,
			[]string{"child_orgs.mode 'some' is not valid. Use all or selected"}},
		{"orgs with mode all", &model.ChildOrgs{Mode: model.ChildOrgsModeAll,
			Orgs: []model.ChildOrg{{OrgID: "A", Reach: model.ReachOrg}}}, nil,
			[]string{"child_orgs.orgs must be empty when child_orgs.mode is all"}},
		{"no orgs with mode selected", selected(), nil,
			[]string{"child_orgs.orgs must have at least one organization when child_orgs.mode is selected"}},
		{"no reach", selected(model.ChildOrg{OrgID: "A"}), nil, []string{"the organization 'A' requires a reach"}},
		{"unknown reach", selected(model.ChildOrg{OrgID: "A", Reach: "all"}), nil,
			[]string{"the reach 'all' of the organization 'A' is not valid. Use org or subtree"}},
		{"no org_id", selected(model.ChildOrg{OrgID: " ", Reach: model.ReachOrg}), nil,
			[]string{"each child_orgs.orgs entry requires an org_id"}},
		{"one org twice", selected(model.ChildOrg{OrgID: "A", Reach: model.ReachOrg},
			model.ChildOrg{OrgID: "A", Reach: model.ReachSubtree}), nil,
			[]string{"the organization 'A' is in child_orgs.orgs more than once"}},
		{"all problems of the entries", selected(model.ChildOrg{Reach: model.ReachOrg},
			model.ChildOrg{OrgID: "A"}, model.ChildOrg{OrgID: "B", Reach: model.ReachOrg},
			model.ChildOrg{OrgID: "B", Reach: model.ReachOrg}), nil,
			[]string{"each child_orgs.orgs entry requires an org_id", "the organization 'A' requires a reach",
				"the organization 'B' is in child_orgs.orgs more than once"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, problems := ToTargets(c.childOrgs, "R")
			if c.problems != nil {
				if got != nil || !reflect.DeepEqual(problems, c.problems) {
					t.Fatalf("expected the problems %v, got %v %v", c.problems, got, problems)
				}
				return
			}
			if len(problems) > 0 || !reflect.DeepEqual(got, c.expected) {
				t.Fatalf("expected %v, got %v %v", c.expected, got, problems)
			}
			if back := ToChildOrgs(got); !reflect.DeepEqual(&back, c.childOrgs) {
				t.Fatalf("expected the same child_orgs back, got %+v", back)
			}
		})
	}
}
