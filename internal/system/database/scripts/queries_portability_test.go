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

package scripts

import (
	"regexp"
	"sort"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// A statement is written once, in PostgreSQL wording, and runs on every other datasource
// unless it has an override. That fallback hides a mistake: PostgreSQL syntax that MySQL or
// SQL Server rejects fails only when the integration suite reaches the statement. This test
// finds the mistake from the statement's text, so a new statement cannot reach MySQL or SQL
// Server without the override it needs.

var postgresOnlySyntax = []struct {
	name    string
	pattern *regexp.Regexp
	reject  []string
}{
	{"ON CONFLICT", regexp.MustCompile(`(?i)\bON\s+CONFLICT\b`), []string{database.TypeMySQL, database.TypeMSSQL}},
	{"EXCLUDED.column", regexp.MustCompile(`(?i)\bEXCLUDED\.`), []string{database.TypeMySQL, database.TypeMSSQL}},
	{"a :: cast", regexp.MustCompile(`::`), []string{database.TypeMySQL, database.TypeMSSQL}},
	{"CAST(... AS TEXT)", regexp.MustCompile(`(?i)\bAS\s+TEXT\b`), []string{database.TypeMySQL, database.TypeMSSQL}},
	{"ILIKE", regexp.MustCompile(`(?i)\bILIKE\b`), []string{database.TypeMySQL, database.TypeMSSQL}},
	// MySQL's own JSON_EXTRACT and the like are not PostgreSQL syntax, so only the
	// PostgreSQL names are matched.
	{"jsonb or jsonpath", regexp.MustCompile(`(?i)\bjsonb(_[a-z_]+)?\b|\bjsonpath\b|` +
		`\bjson_(array_elements|each|build_object|build_array|typeof|path_query|agg|object_agg)[a-z_]*\b`),
		[]string{database.TypeMySQL, database.TypeMSSQL}},
	{"a JSON operator (@>, @?, ->, ->>)", regexp.MustCompile(`@>|@\?|->>?`),
		[]string{database.TypeMySQL, database.TypeMSSQL}},
	{"RETURNING", regexp.MustCompile(`(?i)\bRETURNING\b`), []string{database.TypeMySQL, database.TypeMSSQL}},
	{"LATERAL", regexp.MustCompile(`(?i)\bLATERAL\b`), []string{database.TypeMySQL, database.TypeMSSQL}},
	{"ANY, unnest or generate_series",
		regexp.MustCompile(`(?i)\b(ANY\s*\(|unnest\b|generate_series\b|string_agg\b|array_[a-z_]+\b)`),
		[]string{database.TypeMySQL, database.TypeMSSQL}},
	{"|| concatenation", regexp.MustCompile(`\|\|`), []string{database.TypeMySQL}},
	{"LIMIT", regexp.MustCompile(`(?i)\bLIMIT\b`), []string{database.TypeMSSQL}},
	{"TRUE or FALSE", regexp.MustCompile(`(?i)\b(TRUE|FALSE)\b`), []string{database.TypeMSSQL}},
	{"now()", regexp.MustCompile(`(?i)\bnow\s*\(\s*\)`), []string{database.TypeMSSQL}},
	{"WITH RECURSIVE", regexp.MustCompile(`(?i)\bRECURSIVE\b`), []string{database.TypeMSSQL}},
}

// portabilityExceptions lists the statements whose PostgreSQL text keeps syntax a datasource
// rejects without an override listed for it, and why that is correct. A new entry needs a
// reason a reviewer can check.
var portabilityExceptions = map[string]map[string]string{
	"CDS-PRF-20": {
		database.TypeMSSQL: "the SQL Server text depends on the number of keys, so " +
			"BuildFindOldestReferenceProfileIDByAttributeValuesQuery builds it for the call",
	},
}

var stringLiteral = regexp.MustCompile(`'(?:[^']|'')*'`)

// unportableSyntax returns the PostgreSQL-only syntax in text that dbType rejects. String
// literals are not read, since text inside one is data.
func unportableSyntax(dbType, text string) []string {
	code := stringLiteral.ReplaceAllString(text, "''")
	var found []string
	for _, syntax := range postgresOnlySyntax {
		for _, rejecting := range syntax.reject {
			if rejecting == dbType && syntax.pattern.MatchString(code) {
				found = append(found, syntax.name)
			}
		}
	}
	return found
}

// Every statement has a text that runs on MySQL and on SQL Server, either its own PostgreSQL
// text or an override.
func Test_EveryStatementIsPortable(t *testing.T) {
	for name, query := range AllQueries() {
		for _, dbType := range []string{database.TypeMySQL, database.TypeMSSQL} {
			problems := unportableSyntax(dbType, query.GetQuery(dbType))
			if len(problems) == 0 {
				continue
			}
			if reason, allowed := portabilityExceptions[query.ID][dbType]; allowed && reason != "" {
				continue
			}
			sort.Strings(problems)
			t.Errorf("%s (%s) runs on %s with syntax it rejects: %v.\n"+
				"Add a %s text for it in queries_%s.go.", query.ID, name, dbType, problems, dbType, dbType)
		}
	}
}

// An exception that no longer matches anything would hide a statement that needs an override.
func Test_PortabilityExceptionsStillApply(t *testing.T) {
	byID := map[string]string{}
	for _, query := range AllQueries() {
		byID[query.ID] = query.Query
	}
	for id, databases := range portabilityExceptions {
		text, exists := byID[id]
		if !exists {
			t.Errorf("%s has a portability exception but no registered statement has that id", id)
			continue
		}
		for dbType := range databases {
			if len(unportableSyntax(dbType, text)) == 0 {
				t.Errorf("%s: the exception for %s is not needed, because the statement has no syntax %s rejects",
					id, dbType, dbType)
			}
		}
	}
}

// The check has to see each kind of syntax it names, or it passes while checking nothing.
func Test_UnportableSyntaxIsFound(t *testing.T) {
	cases := []struct {
		dbType, text, want string
	}{
		{database.TypeMySQL, "INSERT INTO t (a) VALUES ($1) ON CONFLICT (a) DO NOTHING", "ON CONFLICT"},
		{database.TypeMSSQL, "UPDATE t SET a = EXCLUDED.a", "EXCLUDED.column"},
		{database.TypeMySQL, "SELECT $1::text", "a :: cast"},
		{database.TypeMSSQL, "SELECT CAST(a AS TEXT) FROM t", "CAST(... AS TEXT)"},
		{database.TypeMySQL, "SELECT a FROM t WHERE b ILIKE $1", "ILIKE"},
		{database.TypeMSSQL, "SELECT jsonb_array_elements(a) FROM t", "jsonb or jsonpath"},
		{database.TypeMySQL, "SELECT a FROM t WHERE b @> $1", "a JSON operator (@>, @?, ->, ->>)"},
		{database.TypeMySQL, "INSERT INTO t (a) VALUES ($1) RETURNING a", "RETURNING"},
		{database.TypeMSSQL, "SELECT a FROM t LIMIT 1", "LIMIT"},
		{database.TypeMSSQL, "SELECT a FROM t WHERE b = TRUE", "TRUE or FALSE"},
		{database.TypeMSSQL, "UPDATE t SET a = now()", "now()"},
		{database.TypeMSSQL, "WITH RECURSIVE c AS (SELECT 1) SELECT * FROM c", "WITH RECURSIVE"},
		{database.TypeMySQL, "SELECT a || b FROM t", "|| concatenation"},
	}
	for _, tc := range cases {
		found := unportableSyntax(tc.dbType, tc.text)
		if len(found) == 0 || !contains(found, tc.want) {
			t.Errorf("%s: %q was not found in %q, got %v", tc.dbType, tc.want, tc.text, found)
		}
	}

	clean := []struct{ dbType, text string }{
		{database.TypeMySQL, "SELECT a FROM t WHERE b = TRUE LIMIT 1"},
		{database.TypeMSSQL, "SELECT a || b FROM t"},
		{database.TypeMSSQL, "SELECT a FROM t WHERE b = 'LIMIT ON CONFLICT now()'"},
		{database.TypeMSSQL, "SELECT TOP (1) a FROM t WHERE b = 1"},
		{database.TypePostgres, "SELECT a FROM t LIMIT 1 ON CONFLICT"},
	}
	for _, tc := range clean {
		if found := unportableSyntax(tc.dbType, tc.text); len(found) != 0 {
			t.Errorf("%s: %q is portable but was reported as %v", tc.dbType, tc.text, found)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
