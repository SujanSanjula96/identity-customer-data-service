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

// Package schema guards the two copies of the database schema against drift.
//
// Integration tests build their database from test/setup/schema.sql, an external install
// runs dbscripts/postgres.sql, and the inbuilt datasource runs dbscripts/sqlite.sql.
// Nothing connects the three, so a change applied to one and not the others passes CI and
// breaks on install — which is how a file with unresolved merge-conflict markers reached
// the branch with every test green.
package schema

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	installSchema = "../../dbscripts/postgres.sql"
	sqliteSchema  = "../../dbscripts/sqlite.sql"
	testSchema    = "../setup/schema.sql"
)

var (
	conflictMarker = regexp.MustCompile(`(?m)^(<{7}|={7}|>{7})`)
	createTable    = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-zA-Z_][a-zA-Z0-9_]*)`)
)

func read(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func tables(sql string) []string {
	found := map[string]bool{}
	for _, match := range createTable.FindAllStringSubmatch(sql, -1) {
		found[strings.ToLower(match[1])] = true
	}

	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestSchemasHaveNoConflictMarkers catches an unresolved merge before it reaches an
// install. A marker makes the file unexecutable, and because integration tests read the
// other copy, nothing else in the suite notices.
func TestSchemasHaveNoConflictMarkers(t *testing.T) {
	for _, path := range []string{installSchema, sqliteSchema, testSchema} {
		content := read(t, path)
		for _, line := range conflictMarker.FindAllString(content, -1) {
			t.Errorf("%s contains an unresolved conflict marker: %q", path, line)
		}
	}
}

// TestInstallSchemaDefinesEveryTestedTable ensures a table the tests rely on also exists
// for real installations. The reverse is allowed: the install script may carry tables the
// integration suite does not exercise.
func TestInstallSchemaDefinesEveryTestedTable(t *testing.T) {
	install := tables(read(t, installSchema))
	tested := tables(read(t, testSchema))

	installed := make(map[string]bool, len(install))
	for _, name := range install {
		installed[name] = true
	}

	for _, name := range tested {
		if !installed[name] {
			t.Errorf("table %q exists in %s but not in %s — installs would be missing it",
				name, testSchema, installSchema)
		}
	}
}

// TestIdentityResolutionTablesArePresent names the tables the resolution engine cannot run
// without, so removing one fails loudly rather than at first merge attempt.
func TestIdentityResolutionTablesArePresent(t *testing.T) {
	required := []string{"blocking_keys", "review_tasks", "rejection_pairs"}

	for _, path := range []string{installSchema, sqliteSchema, testSchema} {
		present := make(map[string]bool)
		for _, name := range tables(read(t, path)) {
			present[name] = true
		}
		for _, name := range required {
			if !present[name] {
				t.Errorf("%s is missing required table %q", path, name)
			}
		}
	}
}

// TestUnificationRuleMatchingColumnsExist pins the columns the scorer reads. A rule loaded
// without them silently falls back to defaults for every tenant.
func TestUnificationRuleMatchingColumnsExist(t *testing.T) {
	required := []string{"attribute_type", "unification_method", "match_strength", "mismatch_strength"}

	for _, path := range []string{installSchema, sqliteSchema, testSchema} {
		content := read(t, path)
		start := strings.Index(strings.ToLower(content), "create table if not exists unification_rules")
		if start < 0 {
			if start = strings.Index(strings.ToLower(content), "create table unification_rules"); start < 0 {
				t.Fatalf("%s does not define unification_rules", path)
			}
		}
		end := strings.Index(content[start:], ");")
		if end < 0 {
			t.Fatalf("%s: unterminated unification_rules definition", path)
		}
		definition := strings.ToLower(content[start : start+end])

		for _, column := range required {
			if !strings.Contains(definition, column) {
				t.Errorf("%s: unification_rules is missing column %q", path, column)
			}
		}
	}
}
