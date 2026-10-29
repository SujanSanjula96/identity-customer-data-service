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
	"testing"
	"time"

	"github.com/google/uuid"
	irService "github.com/wso2/identity-customer-data-service/internal/identity_resolution/service"
	irStore "github.com/wso2/identity-customer-data-service/internal/identity_resolution/store"
	irWorker "github.com/wso2/identity-customer-data-service/internal/identity_resolution/worker"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
)

// insertRule stores a unification rule straight to the table, for the same reason as
// insertFuzzyNameRule: the store would need a profile schema seeded for property_id.
func insertRule(t *testing.T, org, name, property string, priority int, attributeType, method string) {
	t.Helper()

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		t.Fatalf("db client: %v", err)
	}
	defer dbClient.Close()

	now := time.Now().UTC()
	_, err = dbClient.ExecuteQueryContext(context.Background(), fuzzyRuleInsert,
		uuid.New().String(), writeOrgKey(t, org), name, property, priority, true, attributeType, method, now, now)
	if err != nil {
		t.Fatalf("store rule %q: %v", name, err)
	}
}

// Test_RejectedPair_ReconsideredIsReviewedNotMerged checks that a pair an administrator
// rejected goes back to an administrator when stronger evidence arrives, rather than being
// merged unattended.
//
// A rejection is reconsidered when a rule agrees that did not agree before. That new
// agreement can be an exact match on a decisive rule, which scores the pair for an automatic
// merge — and merging on it would overturn a human decision with no human involved, which a
// completed merge cannot be taken back from.
func Test_RejectedPair_ReconsideredIsReviewedNotMerged(t *testing.T) {
	conf := config.Config{}
	conf.IdentityResolution.FuzzyMatchingEnabled = true
	config.OverrideCDSRuntime(conf)

	ctx := context.Background()
	org := newOrg("reject-reconsider")
	insertRule(t, org, "email", "traits.email", 1, constants.AttributeTypeEmail, constants.UnificationMethodDeterministic)
	insertRule(t, org, "name", "traits.name", 2, constants.AttributeTypeName, constants.UnificationMethodFuzzy)

	// Same name, different emails: the name agrees, the email disagrees, so the pair is
	// raised for review rather than merged.
	first := insertBareProfile(t, org, "", map[string]interface{}{
		"name": "Marcus Delacroix", "email": "marcus.d@cdsfuzz.io"})
	irWorker.ResolveProfileAsync(ctx, first)
	second := insertBareProfile(t, org, "", map[string]interface{}{
		"name": "Marcus Delacroix", "email": "mdelacroix@othermail.io"})
	irWorker.ResolveProfileAsync(ctx, second)

	pending, _, err := irStore.GetPendingReviewTasks(ctx, org, 10)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected one review task for the same-name pair, got %d", len(pending))
	}
	// The rejection is recorded against this breakdown. Without it every rule that agrees
	// later reads as new evidence, and the pair returns on any write.
	if len(pending[0].ScoreBreakdown) == 0 {
		t.Fatal("the review task was read back without its score breakdown")
	}

	svc := irService.GetIdentityResolutionService()
	if err := svc.ResolveReviewTask(ctx, org, pending[0].ID, false, "admin", "different people"); err != nil {
		t.Fatalf("reject task: %v", err)
	}

	// Evaluating the pair again on the same evidence leaves the rejection standing.
	irWorker.ResolveProfileAsync(ctx, second)
	if stillRejected, _, err := irStore.GetPendingReviewTasks(ctx, org, 10); err != nil {
		t.Fatalf("list tasks: %v", err)
	} else if len(stillRejected) != 0 {
		t.Fatalf("the rejected pair was raised again with no new evidence: %d pending tasks", len(stillRejected))
	}

	// The email now agrees as well. That is new evidence, so the rejection is reconsidered,
	// and on its own an exact email match would merge the pair.
	second.Traits["email"] = "marcus.d@cdsfuzz.io"
	if err := profileStore.UpdateProfile(ctx, second); err != nil {
		t.Fatalf("update profile: %v", err)
	}
	irWorker.ResolveProfileAsync(ctx, second)

	for _, p := range []string{first.ProfileId, second.ProfileId} {
		stored, err := profileStore.GetProfile(ctx, p, org)
		if err != nil || stored == nil {
			t.Fatalf("load profile %s: %v", p, err)
		}
		if stored.ProfileStatus != nil && stored.ProfileStatus.ReferenceProfileId != "" {
			t.Fatalf("profile %s was merged into %s without review, overturning the rejection",
				p, stored.ProfileStatus.ReferenceProfileId)
		}
	}

	reopened, _, err := irStore.GetPendingReviewTasks(ctx, org, 10)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(reopened) != 1 {
		t.Errorf("expected the reconsidered pair back in the review queue, got %d pending tasks", len(reopened))
	}
}
