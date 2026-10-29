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
	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/engine/algorithms"
	irStore "github.com/wso2/identity-customer-data-service/internal/identity_resolution/store"
	irWorker "github.com/wso2/identity-customer-data-service/internal/identity_resolution/worker"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	dbmodel "github.com/wso2/identity-customer-data-service/internal/system/database/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	urStore "github.com/wso2/identity-customer-data-service/internal/unification_rules/store"
)

// fuzzyRuleInsert writes the rule row directly, leaving property_id unset. The store always
// populates that column and it is a foreign key onto the schema attribute, so going through
// the store would mean seeding a profile schema this test has no interest in.
var fuzzyRuleInsert = dbmodel.DBQuery{
	ID: "TEST-FUZZY-01",
	Query: `INSERT INTO unification_rules
		(rule_id, {{org_cols}}, rule_name, property_name, priority, is_active,
		 attribute_type, unification_method, created_at, updated_at)
		VALUES ($1, {{org_val $2}}, $3, $4, $5, $6, $7, $8, $9, $10)`,
}

// insertFuzzyNameRule stores a rule asking for tolerant matching on a name. It is written
// straight to the table rather than through the handler on purpose: the handler refuses
// fuzzy while the server setting is off, and this test is about what the engine does with a
// rule that is already stored.
func insertFuzzyNameRule(t *testing.T, org string) {
	t.Helper()

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		t.Fatalf("db client: %v", err)
	}
	defer dbClient.Close()

	now := time.Now().UTC()
	_, err = dbClient.ExecuteQueryContext(context.Background(), fuzzyRuleInsert,
		uuid.New().String(), writeOrgKey(t, org), "name", "traits.name", 1, true,
		constants.AttributeTypeName, constants.UnificationMethodFuzzy, now, now)
	if err != nil {
		t.Fatalf("store fuzzy rule: %v", err)
	}
}

// findableUnder reports whether the profile can be reached through one blocking key value.
func findableUnder(t *testing.T, org, attribute, keyValue, profileID string) bool {
	t.Helper()

	ids, err := irStore.FindCandidateIDsByKeys(context.Background(), org, attribute,
		[]string{keyValue}, "none", 100)
	if err != nil {
		t.Fatalf("candidate lookup: %v", err)
	}
	for _, id := range ids {
		if id == profileID {
			return true
		}
	}

	return false
}

// Test_FuzzyMatchingSwitch drives the server-level setting end to end.
//
// The unit tests cover the setting being read and a rule being coerced. What they cannot
// show is that the coercion reaches the index: the blocker decides whether to write
// phonetic and LSH keys from the method it is handed, so a switch that failed to reach it
// would leave the engine still gathering tolerant candidates while reporting itself off.
//
// Phonetic keys are the observable: a tolerant name rule writes one, an exact one does not,
// and both write the normalized value.
func Test_FuzzyMatchingSwitch(t *testing.T) {
	const fullName = "Jonathan Smith"

	phonetic, _ := algorithms.DoubleMetaphonePhrase(fullName)
	if phonetic == "" {
		t.Fatal("no phonetic code for the test name; the assertion below would be vacuous")
	}

	t.Run("on — a tolerant rule widens the index", func(t *testing.T) {
		conf := config.Config{}
		conf.IdentityResolution.FuzzyMatchingEnabled = true
		config.OverrideCDSRuntime(conf)

		org := newOrg("fuzzy-on")
		insertFuzzyNameRule(t, org)
		profile := insertBareProfile(t, org, "", map[string]interface{}{"name": fullName})

		irWorker.ResolveProfileAsync(context.Background(), profile)

		if !findableUnder(t, org, "traits.name", "jonathan smith", profile.ProfileId) {
			t.Error("the exact key was not written")
		}
		if !findableUnder(t, org, "traits.name", phonetic, profile.ProfileId) {
			t.Errorf("no phonetic key %q: tolerant matching is on but the index was not widened", phonetic)
		}
	})

	t.Run("off — the same rule is indexed as an exact match", func(t *testing.T) {
		conf := config.Config{}
		conf.IdentityResolution.FuzzyMatchingEnabled = false
		config.OverrideCDSRuntime(conf)

		org := newOrg("fuzzy-off")
		insertFuzzyNameRule(t, org)
		profile := insertBareProfile(t, org, "", map[string]interface{}{"name": fullName})

		irWorker.ResolveProfileAsync(context.Background(), profile)

		if !findableUnder(t, org, "traits.name", "jonathan smith", profile.ProfileId) {
			t.Error("the exact key must still be written; the rule is not disabled, only narrowed")
		}
		if findableUnder(t, org, "traits.name", phonetic, profile.ProfileId) {
			t.Errorf("phonetic key %q written while tolerant matching is switched off", phonetic)
		}
	})
}

// Test_FuzzyMatchingSwitch_SearchPath covers the synchronous search, which reaches the same
// engine through its own entry point rather than through the worker.
//
// Both subtests index while tolerant matching is ON, so the phonetic keys exist either way
// and blocking finds the candidate in both. Only the search differs. That is deliberate: an
// earlier version of this test indexed with the switch already off, found nothing, and
// passed for the wrong reason — the candidate set was empty, so it proved the indexing gate
// and said nothing about scoring. The gap this covers is a profile indexed before the
// switch was flipped, which is also the realistic case.
func Test_FuzzyMatchingSwitch_SearchPath(t *testing.T) {
	const stored = "Jonathan Smith"
	const typo = "Jonathon Smith"

	fuzzyOn := func() {
		conf := config.Config{}
		conf.IdentityResolution.FuzzyMatchingEnabled = true
		config.OverrideCDSRuntime(conf)
	}

	// indexWithFuzzyOn leaves the profile carrying phonetic and LSH keys whatever the
	// search is later run under.
	indexWithFuzzyOn := func(t *testing.T, org string) {
		t.Helper()

		fuzzyOn()
		insertFuzzyNameRule(t, org)
		profile := insertBareProfile(t, org, "", map[string]interface{}{"name": stored})
		irWorker.ResolveProfileAsync(context.Background(), profile)

		phonetic, _ := algorithms.DoubleMetaphonePhrase(stored)
		if !findableUnder(t, org, "traits.name", phonetic, profile.ProfileId) {
			t.Fatal("setup failed: the profile was not indexed for tolerant matching, " +
				"so a search finding nothing below would prove nothing")
		}
	}

	search := func(t *testing.T, org string) int {
		t.Helper()

		results, err := profileService.GetProfilesService().GetProfilesWithFuzzyResolution(
			context.Background(), org, []string{"traits.name eq " + typo}, 0.5, 10)
		if err != nil {
			t.Fatalf("fuzzy search: %v", err)
		}

		return len(results)
	}

	t.Run("on — a near miss is found", func(t *testing.T) {
		org := newOrg("fuzzy-search-on")
		indexWithFuzzyOn(t, org)

		if n := search(t, org); n == 0 {
			t.Error("tolerant matching is on but the search found no near miss")
		}
	})

	t.Run("off — the indexed candidate is no longer a tolerant match", func(t *testing.T) {
		org := newOrg("fuzzy-search-off")
		indexWithFuzzyOn(t, org)

		conf := config.Config{}
		conf.IdentityResolution.FuzzyMatchingEnabled = false
		config.OverrideCDSRuntime(conf)

		if n := search(t, org); n != 0 {
			t.Errorf("search scored %d tolerant match(es) while the server has tolerant "+
				"matching off; blocking still reaches the candidate, so the rule has to be "+
				"read as exact", n)
		}
	})
}

// Test_FuzzyMatchingSwitch_Backfill covers the index backfill, which writes blocking keys
// straight from the rule rather than through the evaluation path.
//
// Activating a rule rebuilds that attribute's keys across every profile in the org. The
// backfill therefore has its own reading of the rule's method, and has to honour the server
// switch like the rest — otherwise an org on a server with tolerant matching switched off
// still has its whole index widened the moment a fuzzy-stored rule is toggled on.
func Test_FuzzyMatchingSwitch_Backfill(t *testing.T) {
	const fullName = "Jonathan Smith"

	phonetic, _ := algorithms.DoubleMetaphonePhrase(fullName)

	backfill := func(t *testing.T, org string, fuzzyOn bool) (exact, tolerant bool) {
		t.Helper()

		conf := config.Config{}
		conf.IdentityResolution.FuzzyMatchingEnabled = fuzzyOn
		config.OverrideCDSRuntime(conf)

		// The profile exists before the rule, so the only thing that can index it is the
		// backfill the rule's activation triggers.
		profile := insertBareProfile(t, org, "", map[string]interface{}{"name": fullName})
		insertFuzzyNameRule(t, org)

		rules, err := urStore.GetUnificationRules(context.Background(), org)
		if err != nil || len(rules) == 0 {
			t.Fatalf("load rules for %s: %v", org, err)
		}
		irWorker.IndexNewAttribute(context.Background(), org, rules[0])

		return findableUnder(t, org, "traits.name", "jonathan smith", profile.ProfileId),
			findableUnder(t, org, "traits.name", phonetic, profile.ProfileId)
	}

	t.Run("on — the backfill widens the index", func(t *testing.T) {
		exact, tolerant := backfill(t, newOrg("backfill-on"), true)
		if !exact {
			t.Error("the backfill did not write the exact key")
		}
		if !tolerant {
			t.Errorf("the backfill wrote no phonetic key %q while tolerant matching is on", phonetic)
		}
	})

	t.Run("off — the backfill writes only the exact key", func(t *testing.T) {
		exact, tolerant := backfill(t, newOrg("backfill-off"), false)
		if !exact {
			t.Error("the backfill must still write the exact key; the rule is narrowed, not disabled")
		}
		if tolerant {
			t.Errorf("the backfill wrote phonetic key %q while tolerant matching is switched off", phonetic)
		}
	})
}
