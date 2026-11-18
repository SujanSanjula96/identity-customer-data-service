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
