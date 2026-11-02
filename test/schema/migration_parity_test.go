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

package schema

import (
	"regexp"
	"strings"
	"testing"
)

const migrationScript = "../../dbscripts/migrations/001-identity-resolution-postgres.sql"

// identityResolutionTables are the tables a database created before this feature does not
// have. A fresh install gets them from dbscripts/postgres.sql; an existing one gets them
// from the migration, and the two have to agree or an upgraded environment runs a schema no
// install ever produces.
var identityResolutionTables = []string{"blocking_keys", "review_tasks", "rejection_pairs"}

// matchingColumns are the columns identity resolution adds to an existing table.
var matchingColumns = []string{"attribute_type", "unification_method", "match_strength", "mismatch_strength"}

// TestMigrationCreatesEveryNewTable fails when the install schema gains a resolution table
// the migration does not, which would leave upgraded environments missing it.
func TestMigrationCreatesEveryNewTable(t *testing.T) {
	migration := read(t, migrationScript)
	install := read(t, installSchema)

	inMigration := map[string]bool{}
	for _, name := range tables(migration) {
		inMigration[name] = true
	}

	for _, name := range identityResolutionTables {
		if !inMigration[name] {
			t.Errorf("%s is missing from the migration script", name)
		}
		if !strings.Contains(strings.ToLower(install), strings.ToLower(name)) {
			t.Errorf("%s is named by this test but absent from the install schema", name)
		}
	}
}

// TestMigrationAddsEveryMatchingColumn pins the columns the scorer reads onto the upgrade
// path, not just onto a fresh install.
func TestMigrationAddsEveryMatchingColumn(t *testing.T) {
	migration := read(t, migrationScript)

	for _, column := range matchingColumns {
		pattern := regexp.MustCompile(`(?i)ADD\s+COLUMN\s+IF\s+NOT\s+EXISTS\s+` + column + `\b`)
		if !pattern.MatchString(migration) {
			t.Errorf("the migration does not add %s to unification_rules", column)
		}
	}
}

// TestMigrationLeavesTheStrengthsWithoutADefault guards the trap this feature already fell
// into once.
//
// A DEFAULT on either strength column stamps a concrete value onto every pre-existing rule.
// The engine cannot tell that apart from an operator's deliberate override, so it stops
// deriving the strength from attribute_type — and an upgraded organisation quietly starts
// merging a different set of profiles. The columns must arrive NULL.
func TestMigrationLeavesTheStrengthsWithoutADefault(t *testing.T) {
	migration := read(t, migrationScript)

	for _, column := range []string{"match_strength", "mismatch_strength"} {
		stanza := regexp.MustCompile(`(?i)ADD\s+COLUMN\s+IF\s+NOT\s+EXISTS\s+` + column + `[^;]*`)
		match := stanza.FindString(migration)
		if match == "" {
			t.Fatalf("no ADD COLUMN found for %s", column)
		}
		if regexp.MustCompile(`(?i)\bDEFAULT\b`).MatchString(match) {
			t.Errorf("%s is added WITH A DEFAULT: %q", column, strings.TrimSpace(match))
		}
		if regexp.MustCompile(`(?i)\bNOT\s+NULL\b`).MatchString(match) {
			t.Errorf("%s is added NOT NULL, which forces a value onto every existing rule: %q",
				column, strings.TrimSpace(match))
		}
	}
}

// TestMigrationIsAdditive keeps the script safe to run against a live database. Anything
// that drops or rewrites is a data-loss risk on an environment already carrying profiles.
func TestMigrationIsAdditive(t *testing.T) {
	migration := strings.ToUpper(read(t, migrationScript))

	for _, forbidden := range []string{"DROP ", "TRUNCATE ", "DELETE FROM", "ALTER COLUMN"} {
		if strings.Contains(migration, forbidden) {
			t.Errorf("the migration contains %q; it must be purely additive", strings.TrimSpace(forbidden))
		}
	}
}
