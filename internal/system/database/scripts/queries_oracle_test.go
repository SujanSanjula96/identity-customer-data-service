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

package scripts

import (
	"regexp"
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

func Test_EveryOracleStatementBelongsToARegisteredQuery(t *testing.T) {
	registered := map[string]bool{}
	for _, query := range AllQueries() {
		registered[query.ID] = true
	}
	for id := range oracleStatements {
		if !registered[id] {
			t.Errorf("%s is listed in oracleStatements but no registered statement has that id", id)
		}
	}
}

func Test_OracleStatementsAreResolvedForOracleOnly(t *testing.T) {
	for _, query := range AllQueries() {
		text, listed := oracleStatements[query.ID]
		if !listed {
			continue
		}
		if query.GetQuery(database.TypeOracle) != text {
			t.Errorf("%s: Oracle does not get its listed text", query.ID)
		}
		// MySQL and MSSQL are left out: a statement that is plain in several gets the same text.
		for _, other := range []string{database.TypePostgres, database.TypeSQLite} {
			if query.GetQuery(other) == text {
				t.Errorf("%s: %s got the Oracle text", query.ID, other)
			}
		}
	}
}

// The listed statements are written for Oracle 21c, so none may keep syntax it rejects, and none may end with
// the semicolon the driver refuses.
func Test_OracleStatementsKeepNoSyntaxOracle21cRejects(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)(ON CONFLICT|EXCLUDED\.|::|ILIKE|jsonb|RETURNING|\bLIMIT\b|\bnow\(\)|` +
		`\bTRUE\b|\bFALSE\b|RECURSIVE|ON DUPLICATE KEY|AS TEXT|SYSUTCDATETIME|\bTOP\s*\(|OPENJSON|WITH \(HOLDLOCK\))`)
	for id, text := range oracleStatements {
		if match := forbidden.FindString(text); match != "" {
			t.Errorf("%s: contains %q", id, match)
		}
		if strings.HasSuffix(strings.TrimSpace(text), ";") {
			t.Errorf("%s: an Oracle statement must not end with a semicolon", id)
		}
	}
}

func Test_OracleStatementsFormatLikeTheirPostgresText(t *testing.T) {
	for _, query := range AllQueries() {
		text, listed := oracleStatements[query.ID]
		if !listed {
			continue
		}
		if got, want := formatArgumentCount(text), formatArgumentCount(query.Query); got != want {
			t.Errorf("%s: the Oracle text takes %d arguments, the PostgreSQL text %d", query.ID, got, want)
		}
	}
}

// The statements a store completes with one row take their arguments by fixed number, so the numbers must agree
// with the order of the columns the store binds.
func Test_OracleSchemaUpsertNamesEveryColumnOfItsRow(t *testing.T) {
	text := oracleStatements["CDS-SCH-05"]
	for number, column := range map[string]string{"3": "attribute_name", "4": "value_type",
		"5": "merge_strategy", "6": "mutability", "7": "application_identifier", "8": "multi_valued",
		"9": "canonical_values", "10": "sub_attributes", "11": "scim_dialect", "13": "display_name"} {
		if !regexp.MustCompile(column + `\s*= \$` + number + `\b`).MatchString(text) {
			t.Errorf("CDS-SCH-05 does not set %s from $%s", column, number)
		}
	}
}
