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

package reindex

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

func rule(property, attributeType, method string) model.UnificationRule {
	return model.UnificationRule{
		RuleId:            "rule-" + property,
		PropertyName:      property,
		AttributeType:     attributeType,
		UnificationMethod: method,
	}
}

func rules(rs ...model.UnificationRule) map[string]model.UnificationRule {
	m := make(map[string]model.UnificationRule, len(rs))
	for _, r := range rs {
		m[r.PropertyName] = r
	}
	return m
}

func propertiesOf(rs []model.UnificationRule) []string {
	var names []string
	for _, r := range rs {
		names = append(names, r.PropertyName)
	}
	return names
}

func TestDiff(t *testing.T) {

	email := rule("traits.email", constants.AttributeTypeEmail, constants.UnificationMethodDeterministic)
	emailFuzzy := rule("traits.email", constants.AttributeTypeEmail, constants.UnificationMethodFuzzy)
	phone := rule("traits.phone", constants.AttributeTypePhone, constants.UnificationMethodDeterministic)
	phoneAsString := rule("traits.phone", constants.AttributeTypeFuzzyString, constants.UnificationMethodDeterministic)
	name := rule("traits.name", constants.AttributeTypeName, constants.UnificationMethodDeterministic)

	tests := []struct {
		name        string
		before      map[string]model.UnificationRule
		after       map[string]model.UnificationRule
		wantRemove  []string
		wantRebuild []string
		wantAdd     []string
	}{
		{name: "no change", before: rules(email, phone), after: rules(email, phone)},
		{name: "rule shared with the organization", before: nil, after: rules(email),
			wantAdd: []string{"traits.email"}},
		{name: "rule no longer shared with the organization", before: rules(email, phone), after: rules(phone),
			wantRemove: []string{"traits.email"}},
		{name: "organization access removed", before: rules(email, phone), after: nil,
			wantRemove: []string{"traits.email", "traits.phone"}},
		{name: "unification method changed", before: rules(email), after: rules(emailFuzzy),
			wantRebuild: []string{"traits.email"}},
		{name: "attribute type changed", before: rules(phone), after: rules(phoneAsString),
			wantRebuild: []string{"traits.phone"}},
		{name: "mixed", before: rules(email, phone), after: rules(emailFuzzy, name),
			wantRemove: []string{"traits.phone"}, wantRebuild: []string{"traits.email"},
			wantAdd: []string{"traits.name"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := Diff(tt.before, tt.after)
			if !reflect.DeepEqual(plan.Remove, tt.wantRemove) {
				t.Errorf("remove: got %v, want %v", plan.Remove, tt.wantRemove)
			}
			if got := propertiesOf(plan.Rebuild); !reflect.DeepEqual(got, tt.wantRebuild) {
				t.Errorf("rebuild: got %v, want %v", got, tt.wantRebuild)
			}
			if got := propertiesOf(plan.Add); !reflect.DeepEqual(got, tt.wantAdd) {
				t.Errorf("add: got %v, want %v", got, tt.wantAdd)
			}
			if plan.IsEmpty() != (len(tt.wantRemove)+len(tt.wantRebuild)+len(tt.wantAdd) == 0) {
				t.Errorf("IsEmpty: got %v", plan.IsEmpty())
			}
		})
	}
}

// fakeTree replaces the hooks of the package with an organization tree in memory, and records the
// index work.
type fakeTree struct {
	subOrgs []string
	active  map[string]map[string]model.UnificationRule
	calls   []string
}

func (f *fakeTree) install(t *testing.T) {

	origList, origActive, origRemove, origIndex := listSubOrgIds, activeRulesOf, removeIndex, indexNewProperty
	t.Cleanup(func() {
		listSubOrgIds, activeRulesOf, removeIndex, indexNewProperty = origList, origActive, origRemove, origIndex
	})
	listSubOrgIds = func(_ context.Context, primaryOrgId string) ([]string, error) {
		if primaryOrgId != "primary" {
			t.Fatalf("unexpected primary organization %q", primaryOrgId)
		}
		return f.subOrgs, nil
	}
	activeRulesOf = func(_ context.Context, orgId string) (map[string]model.UnificationRule, error) {
		return f.active[orgId], nil
	}
	removeIndex = func(_ context.Context, orgId, property string) {
		f.calls = append(f.calls, "remove "+orgId+" "+property)
	}
	indexNewProperty = func(_ context.Context, orgId string, r model.UnificationRule) {
		f.calls = append(f.calls, "index "+orgId+" "+r.PropertyName)
	}
}

func TestReconcileChangesTheIndexOfEachSubOrganization(t *testing.T) {

	if err := log.Init("ERROR"); err != nil {
		t.Fatalf("failed to set up the logger: %v", err)
	}
	setup.EnableOrganizationManagement()

	email := rule("traits.email", constants.AttributeTypeEmail, constants.UnificationMethodDeterministic)
	emailFuzzy := rule("traits.email", constants.AttributeTypeEmail, constants.UnificationMethodFuzzy)
	phone := rule("traits.phone", constants.AttributeTypePhone, constants.UnificationMethodDeterministic)

	tree := &fakeTree{
		subOrgs: []string{"sub-a", "sub-b", "sub-c"},
		active: map[string]map[string]model.UnificationRule{
			"sub-a": rules(email),
			"sub-b": rules(email, phone),
			"sub-c": rules(phone),
		},
	}
	tree.install(t)
	ctx := context.Background()

	before, err := Take(ctx, "primary")
	if err != nil || before == nil {
		t.Fatalf("Take: got %v, %v", before, err)
	}

	// The policy of the email rule now reaches sub-c and not sub-a, and the owner changes the
	// rule to fuzzy matching. A new sub organization sub-d also gets the rule.
	tree.subOrgs = []string{"sub-a", "sub-b", "sub-c", "sub-d"}
	tree.active = map[string]map[string]model.UnificationRule{
		"sub-a": nil,
		"sub-b": rules(emailFuzzy, phone),
		"sub-c": rules(emailFuzzy, phone),
		"sub-d": rules(emailFuzzy),
	}
	Reconcile(ctx, before)

	want := []string{
		"remove sub-a traits.email",
		"remove sub-b traits.email",
		"index sub-b traits.email",
		"index sub-c traits.email",
		"index sub-d traits.email",
	}
	if !reflect.DeepEqual(tree.calls, want) {
		t.Errorf("index work:\n got  %s\n want %s", strings.Join(tree.calls, "; "), strings.Join(want, "; "))
	}
}

func TestReconcileDoesNothingWhenTheRulesDoNotChange(t *testing.T) {

	if err := log.Init("ERROR"); err != nil {
		t.Fatalf("failed to set up the logger: %v", err)
	}
	setup.EnableOrganizationManagement()

	tree := &fakeTree{
		subOrgs: []string{"sub-a"},
		active: map[string]map[string]model.UnificationRule{
			"sub-a": rules(rule("traits.email", constants.AttributeTypeEmail, constants.UnificationMethodDeterministic)),
		},
	}
	tree.install(t)
	ctx := context.Background()

	before, err := Take(ctx, "primary")
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	Reconcile(ctx, before)
	if len(tree.calls) != 0 {
		t.Errorf("index work: got %v, want none", tree.calls)
	}
}

func TestTakeReturnsNilWithoutSubOrganizations(t *testing.T) {

	setup.EnableOrganizationManagement()
	tree := &fakeTree{}
	tree.install(t)

	snapshot, err := Take(context.Background(), "primary")
	if err != nil || snapshot != nil {
		t.Errorf("Take: got %v, %v, want nil, nil", snapshot, err)
	}
	// Reconcile and ReconcileAsync accept the nil snapshot.
	Reconcile(context.Background(), snapshot)
	ReconcileAsync(context.Background(), snapshot)
}
