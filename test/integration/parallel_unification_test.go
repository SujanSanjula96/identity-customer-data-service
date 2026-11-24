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

package integration

import (
	"context"
	"sync"
	"testing"

	"github.com/wso2/identity-customer-data-service/test/setup"

	irStore "github.com/wso2/identity-customer-data-service/internal/identity_resolution/store"
	irWorker "github.com/wso2/identity-customer-data-service/internal/identity_resolution/worker"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	urService "github.com/wso2/identity-customer-data-service/internal/unification_rules/service"
)

// Test_PairEvaluatedByTwoWorkers_RaisesOneReviewTask covers two workers that evaluate both
// profiles of a pair at the same time. Each finds the other as a candidate, and before a task
// was raised under the pair's locks each missed the other's task and raised one in each
// direction, so the administrator was asked about the same pair twice.
func Test_PairEvaluatedByTwoWorkers_RaisesOneReviewTask(t *testing.T) {
	conf := config.Config{}
	conf.IdentityResolution.FuzzyMatchingEnabled = true
	config.OverrideCDSRuntime(conf)

	ctx := context.Background()
	org := newOrg("parallel-review")
	ref := setup.TestOrg(t, org)
	insertRule(t, org, "email", "traits.email", 1, constants.AttributeTypeEmail,
		constants.UnificationMethodDeterministic)
	insertRule(t, org, "name", "traits.name", 2, constants.AttributeTypeName, constants.UnificationMethodFuzzy)

	// Same name, different emails, so each pair is raised for review rather than merged. Both
	// profiles of every pair are indexed before either is evaluated, so each finds the other.
	const pairs = 5
	type pair struct{ first, second string }
	created := make([]pair, 0, pairs)
	names := []string{"Marcus Delacroix", "Imelda Ferreira", "Tobias Lindqvist", "Ayesha Rahman", "Oren Castellano"}
	for i := range pairs {
		first := insertBareProfile(t, org, "", map[string]interface{}{
			"name": names[i], "email": "first" + string(rune('a'+i)) + "@cdsparallel.io"})
		second := insertBareProfile(t, org, "", map[string]interface{}{
			"name": names[i], "email": "second" + string(rune('a'+i)) + "@othermail.io"})
		created = append(created, pair{first.ProfileID, second.ProfileID})
	}
	rules, err := urService.GetUnificationRuleService().GetUnificationRules(ctx, ref.ID)
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	for _, rule := range rules {
		irWorker.IndexNewAttribute(ctx, ref.ID, rule)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, p := range created {
		for _, id := range []string{p.first, p.second} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				irWorker.ResolveProfileAsync(ctx, ref.ID, profileModel.Profile{ProfileID: id, OrgID: ref.ID})
			}()
		}
	}
	close(start)
	wg.Wait()

	pending, _, err := irStore.GetPendingReviewTasks(ctx, ref.ID, 50)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	perPair := map[pair]int{}
	for _, task := range pending {
		for _, p := range created {
			if (task.IncomingProfileID == p.first && task.CandidateProfileID == p.second) ||
				(task.IncomingProfileID == p.second && task.CandidateProfileID == p.first) {
				perPair[p]++
			}
		}
	}
	for i, p := range created {
		if perPair[p] != 1 {
			t.Errorf("pair %d (%s) has %d pending review tasks, want 1", i, names[i], perPair[p])
		}
	}
}
