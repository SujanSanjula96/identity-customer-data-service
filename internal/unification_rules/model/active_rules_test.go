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

package model

import (
	"testing"

	shareModel "github.com/wso2/identity-customer-data-service/internal/sharing/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

func activeRuleNames(rules []UnificationRule) []string {
	names := make([]string, 0, len(rules))
	for _, rule := range ActiveSortedByPriority(rules, constants.AttributeTypePrimitiveExact,
		constants.UnificationMethodDeterministic, constants.DefaultMatchStrength,
		constants.DefaultMismatchStrength) {
		names = append(names, rule.RuleName)
	}
	return names
}

func TestActiveSortedByPriorityKeepsTheRankOrder(t *testing.T) {
	rules := []UnificationRule{
		{RuleName: "owned-email", Priority: 1, IsActive: true, State: shareModel.StateActive, Rank: 2},
		{RuleName: "shared-phone", Priority: 5, IsActive: true, State: shareModel.StateActive, Rank: 1},
		{RuleName: "owned-name", Priority: 2, IsActive: true, State: shareModel.StateActive, Rank: 3},
	}
	got := activeRuleNames(rules)
	want := []string{"shared-phone", "owned-email", "owned-name"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestActiveSortedByPrioritySkipsRulesThatDoNotRun(t *testing.T) {
	rules := []UnificationRule{
		{RuleName: "active", Priority: 1, IsActive: true, State: shareModel.StateActive, Rank: 1},
		{RuleName: "conflicted", Priority: 2, IsActive: true, State: shareModel.StateConflicted, Rank: 2},
		{RuleName: "missing-attribute", Priority: 3, IsActive: true, State: StateInactiveMissingAttribute, Rank: 3},
		{RuleName: "inactive", Priority: 4, IsActive: false, State: shareModel.StateActive, Rank: 4},
		{RuleName: "from-store", Priority: 5, IsActive: true},
	}
	got := activeRuleNames(rules)
	if len(got) != 2 || got[0] != "from-store" || got[1] != "active" {
		t.Fatalf("got %v, want [from-store active]", got)
	}
}
