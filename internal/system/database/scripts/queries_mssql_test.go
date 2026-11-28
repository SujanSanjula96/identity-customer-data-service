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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

func Test_EveryMSSQLStatementBelongsToARegisteredQuery(t *testing.T) {
	registered := map[string]bool{}
	for _, query := range AllQueries() {
		registered[query.ID] = true
	}
	for id := range mssqlStatements {
		if !registered[id] {
			t.Errorf("%s is listed in mssqlStatements but no registered statement has that id", id)
		}
	}
}

func Test_MSSQLStatementsAreResolvedForMSSQLOnly(t *testing.T) {
	for _, query := range AllQueries() {
		text, listed := mssqlStatements[query.ID]
		if !listed {
			continue
		}
		if query.GetQuery(database.TypeMSSQL) != text {
			t.Errorf("%s: MSSQL does not get its listed text", query.ID)
		}
		// MySQL is left out: a statement that is plain in both gets the same text.
		for _, other := range []string{database.TypePostgres, database.TypeSQLite} {
			if query.GetQuery(other) == text {
				t.Errorf("%s: %s got the MSSQL text", query.ID, other)
			}
		}
	}
}

// The listed statements are written for SQL Server, so none may keep syntax it rejects.
func Test_MSSQLStatementsKeepNoPostgresSyntax(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)(ON CONFLICT|EXCLUDED\.|::|ILIKE|jsonb|RETURNING|\bLIMIT\b|\bnow\(\)|` +
		`\bTRUE\b|\bFALSE\b|RECURSIVE|ON DUPLICATE KEY|AS TEXT)`)
	for id, text := range mssqlStatements {
		if match := forbidden.FindString(text); match != "" {
			t.Errorf("%s: contains %q", id, match)
		}
		if strings.HasPrefix(strings.TrimSpace(text), "MERGE") && !strings.HasSuffix(strings.TrimSpace(text), ";") {
			t.Errorf("%s: a MERGE statement must end with a semicolon", id)
		}
	}
}

func Test_MSSQLStatementsFormatLikeTheirPostgresText(t *testing.T) {
	for _, query := range AllQueries() {
		text, listed := mssqlStatements[query.ID]
		if !listed {
			continue
		}
		if got, want := formatArgumentCount(text), formatArgumentCount(query.Query); got != want {
			t.Errorf("%s: the MSSQL text takes %d arguments, the PostgreSQL text %d", query.ID, got, want)
		}
	}
}

// readMSSQLSchema returns dbscripts/mssql.sql, the schema operators apply, with its whitespace collapsed.
func readMSSQLSchema(t *testing.T) string {
	t.Helper()
	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "dbscripts", "mssql.sql"))
	if err != nil {
		t.Fatalf("read the SQL Server schema: %v", err)
	}
	return regexp.MustCompile(`\s+`).ReplaceAllString(string(schema), " ")
}

// SQL Server rejects a statement whose hint names an index the table does not have, so every index
// a listed statement forces a seek on must exist in the schema, with the columns the hint seeks on as
// the leading columns of its key.
func Test_MSSQLSeekHintsNameIndexesOfTheSchema(t *testing.T) {
	schema := readMSSQLSchema(t)
	space := regexp.MustCompile(`\s+`)
	hint := regexp.MustCompile(`FORCESEEK \((\w+) \(([^)]*)\)\)`)
	for id, text := range mssqlStatements {
		for _, match := range hint.FindAllStringSubmatch(space.ReplaceAllString(text, " "), -1) {
			index, columns := match[1], match[2]
			key := regexp.MustCompile(`(?:CONSTRAINT|INDEX) ` + index + `\b[^(]*\(` +
				regexp.QuoteMeta(columns) + `[,)]`)
			if !key.MatchString(schema) {
				t.Errorf("%s: dbscripts/mssql.sql has no index %s whose key starts with (%s)", id, index, columns)
			}
		}
	}
}

// The insert of blocking keys stores them without looking for them first, so it may only run again
// while the primary key drops the keys that are stored instead of failing the statement.
func Test_MSSQLBlockingKeyInsertSkipsStoredKeysThroughThePrimaryKey(t *testing.T) {
	insert := IRInsertBlockingKeys.GetQuery(database.TypeMSSQL)
	if strings.Contains(insert, "MERGE") || strings.Contains(insert, "EXISTS") {
		t.Errorf("the insert reads blocking_keys before it stores the keys:\n%s", insert)
	}
	pk := regexp.MustCompile(`CONSTRAINT pk_blocking_keys PRIMARY KEY NONCLUSTERED \([^)]*\) ` +
		`WITH \(IGNORE_DUP_KEY = ON\)`)
	if !pk.MatchString(readMSSQLSchema(t)) {
		t.Error("dbscripts/mssql.sql does not create pk_blocking_keys WITH (IGNORE_DUP_KEY = ON)")
	}
}

// The re-index of a profile deletes its keys by a seek that reaches no other profile's rows, or two
// profiles that share a key deadlock each other.
func Test_MSSQLBlockingKeyDeleteSeeksTheProfilesOwnRows(t *testing.T) {
	query := DeleteBlockingKeysSQL.GetQuery(database.TypeMSSQL)
	if !strings.Contains(query, "FORCESEEK (idx_blocking_keys_org_id_profile (org_id, profile_id))") ||
		!strings.Contains(query, "ROWLOCK") {
		t.Errorf("the delete does not seek idx_blocking_keys_org_id_profile with row locks:\n%s", query)
	}
}
