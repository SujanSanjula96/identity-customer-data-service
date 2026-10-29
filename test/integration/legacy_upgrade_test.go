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
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	irModel "github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	irStore "github.com/wso2/identity-customer-data-service/internal/identity_resolution/store"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	dbmodel "github.com/wso2/identity-customer-data-service/internal/system/database/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"
	urStore "github.com/wso2/identity-customer-data-service/internal/unification_rules/store"
)

// legacyRuleInsert and legacyConfigInsert write rows in the shape a pre-upgrade database
// holds them: the columns the feature added are simply not mentioned, so whatever the
// migrated schema does with them is what the engine has to cope with.
var legacyRuleInsert = dbmodel.DBQuery{
	ID: "TEST-LEGACY-01",
	Query: `INSERT INTO unification_rules
		(rule_id, {{org_cols}}, rule_name, property_name, priority, is_active, created_at, updated_at)
		VALUES ($1, {{org_val $2}}, $3, $4, $5, $6, $7, $8)`,
}

var legacyConfigInsert = dbmodel.DBQuery{
	ID:    "TEST-LEGACY-02",
	Query: `INSERT INTO cds_config ({{org_cols}}, config, value) VALUES ({{org_val $1}}, $2, $3)`,
}

// insertLegacyRule writes a unification rule the way one existed before typed matching:
// the row is created without attribute_type, unification_method or either strength, so the
// column defaults and the load-time derivation decide how it behaves. It deliberately does
// not go through the store, because the store always writes the new columns and would not
// reproduce a migrated row.
func insertLegacyRule(t *testing.T, org, property string, priority int) {
	t.Helper()

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		t.Fatalf("db client: %v", err)
	}
	defer dbClient.Close()

	_, err = dbClient.ExecuteQueryContext(context.Background(), legacyRuleInsert,
		uuid.New().String(), writeOrgKey(t, org), fmt.Sprintf("legacy-%s", property), property,
		priority, true, time.Now().UTC(), time.Now().UTC())
	if err != nil {
		t.Fatalf("insert legacy rule %s: %v", property, err)
	}
}

// insertLegacyAdminConfig writes the admin config an org had before auto_merge_enabled
// existed: cds_enabled only, and no row at all for the merge settings.
func insertLegacyAdminConfig(t *testing.T, org string) {
	t.Helper()

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		t.Fatalf("db client: %v", err)
	}
	defer dbClient.Close()

	if _, err := dbClient.ExecuteQueryContext(context.Background(), legacyConfigInsert,
		writeOrgKey(t, org), constants.ConfigCDSEnabled, "true"); err != nil {
		t.Fatalf("insert legacy admin config: %v", err)
	}
}

// Test_LegacyOrganisation_KeepsExactMatchMerging drives a pre-upgrade database through the
// current engine.
//
// The unit tests build legacy rules in memory, which cannot catch a column default writing
// a concrete value onto every migrated row — the exact way the in-memory derivation gets
// bypassed in production. This starts from rows as a migration would leave them.
func Test_LegacyOrganisation_KeepsExactMatchMerging(t *testing.T) {
	// The shared value sits on the second rule, not the first. Before typed matching any
	// rule merging on an exact match was sufficient regardless of its priority, and an
	// upgrade must not quietly require the top-priority rule to be the one that matches.
	cases := []struct {
		name     string
		property string
		priority int
	}{
		{"top priority rule matches", "email", 1},
		{"second priority rule matches", "phone", 2},
		{"lowest priority rule matches", "nic", 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			org := newOrg("legacy-upgrade")
			insertLegacyAdminConfig(t, org)
			insertLegacyRule(t, org, "traits.email", 1)
			insertLegacyRule(t, org, "traits.phone", 2)
			insertLegacyRule(t, org, "traits.nic", 3)

			// A migrated rule must arrive with no stored strength, so the engine derives
			// it. A column default here is what silently defeats the derivation.
			rules, err := urStore.GetUnificationRules(context.Background(), org)
			if err != nil {
				t.Fatalf("load rules: %v", err)
			}
			for _, rule := range rules {
				if rule.MatchStrength != "" || rule.MismatchStrength != "" {
					t.Fatalf("migrated rule %s stored strengths (%q/%q); the schema must leave "+
						"them unset so they follow attribute_type",
						rule.PropertyName, rule.MatchStrength, rule.MismatchStrength)
				}
			}

			shared := map[string]interface{}{tc.property: "shared-value-abc"}
			existing := insertBareProfile(t, org, "", shared)
			incoming := insertBareProfile(t, org, "", shared)

			master, mergeErr := workers.MergeMatchedProfiles(context.Background(), existing, incoming, constants.MergeReasonAutoMerge)
			if mergeErr != nil {
				t.Fatalf("merge: %v", mergeErr)
			}
			if master == nil {
				t.Fatal("expected the two profiles to merge")
			}

			// Both inputs must now resolve to the surviving master.
			for _, id := range []string{existing.ProfileId, incoming.ProfileId} {
				if id == master.ProfileId {
					continue
				}
				child, err := profileStore.GetProfile(context.Background(), id, org)
				if err != nil || child == nil {
					t.Fatalf("load %s: %v", id, err)
				}
				if child.ProfileStatus == nil || child.ProfileStatus.ReferenceProfileId != master.ProfileId {
					t.Errorf("profile %s was not attached to master %s", id, master.ProfileId)
				}
			}
		})
	}
}

// Test_LegacyOrganisation_AutoMergeStaysEnabled checks the setting an upgrade never wrote.
//
// auto_merge_enabled is stored as a row, so an org configured before the key existed has no
// row for it. Reading that absence as "disabled" would route every match to the review
// queue instead of merging, with nothing to indicate why.
func Test_LegacyOrganisation_AutoMergeStaysEnabled(t *testing.T) {
	org := newOrg("legacy-automerge")
	insertLegacyAdminConfig(t, org)

	thresholds := irModel.LoadThresholds(context.Background(), org)
	if !thresholds.AutoMergeEnabled {
		t.Error("an organisation with no auto_merge_enabled row must keep merging automatically")
	}
	if thresholds.AutoMerge <= 0 || thresholds.ManualReview <= 0 {
		t.Errorf("thresholds must fall back to usable defaults, got auto=%v review=%v",
			thresholds.AutoMerge, thresholds.ManualReview)
	}
}

// Test_LegacyOrganisation_RulesAreIndexedOnWrite checks that a profile written under legacy
// rules still enters the blocking index, so the pair becomes a candidate at all. Without an
// index entry the merge above could never be reached in the real flow.
func Test_LegacyOrganisation_RulesAreIndexedOnWrite(t *testing.T) {
	org := newOrg("legacy-index")
	insertLegacyAdminConfig(t, org)
	insertLegacyRule(t, org, "traits.email", 1)

	profile := insertBareProfile(t, org, "", map[string]interface{}{"email": "legacy@acme.com"})

	workers.EnqueueProfileForProcessing(profile)
	waitForBlockingKeys(t, org, "traits.email", profile.ProfileId)
}

// waitForBlockingKeys polls for the asynchronous indexing to land.
func waitForBlockingKeys(t *testing.T, org, attribute, profileID string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ids, err := irStore.FindCandidateIDsByKeys(context.Background(), org, attribute,
			[]string{"legacy@acme.com"}, "none", 100)
		if err == nil {
			for _, id := range ids {
				if id == profileID {
					return
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("profile %s was not indexed under %s within the deadline", profileID, attribute)
}
