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
	"strconv"
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// A listed id that matches no statement would never reach MySQL, and nothing
// else would notice.
func Test_EveryMySQLStatementBelongsToARegisteredQuery(t *testing.T) {
	registered := map[string]bool{}
	for _, query := range AllQueries() {
		registered[query.ID] = true
	}
	for id := range mysqlStatements {
		if !registered[id] {
			t.Errorf("%s is listed in mysqlStatements but no registered statement has that id", id)
		}
	}
}

func Test_MySQLStatementsAreResolvedForMySQLOnly(t *testing.T) {
	for _, query := range AllQueries() {
		text, listed := mysqlStatements[query.ID]
		if !listed {
			continue
		}
		if query.GetQuery(database.TypeMySQL) != text {
			t.Errorf("%s: MySQL does not get its listed text", query.ID)
		}
		if query.GetQuery(database.TypePostgres) == text {
			t.Errorf("%s: PostgreSQL got the MySQL text", query.ID)
		}
	}
}

// The listed statements are written for MySQL, so none may keep syntax MySQL rejects.
func Test_MySQLStatementsKeepNoPostgresSyntax(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)(ON CONFLICT|EXCLUDED\.|::|ILIKE|jsonb|RETURNING|\bnow\(\))`)
	for id, text := range mysqlStatements {
		if match := forbidden.FindString(text); match != "" {
			t.Errorf("%s: contains %q", id, match)
		}
		if strings.Contains(text, ";") {
			t.Errorf("%s: a trailing semicolon is not part of a prepared statement", id)
		}
	}
}

// formatArgumentCount is how many arguments fmt.Sprintf needs for the text: the highest
// numbered verb, or else the number of verbs.
func formatArgumentCount(text string) int {
	verbs := regexp.MustCompile(`%(?:\[(\d+)\])?[sd]`).FindAllStringSubmatch(text, -1)
	highest, plain := 0, 0
	for _, verb := range verbs {
		if verb[1] == "" {
			plain++
			continue
		}
		index, _ := strconv.Atoi(verb[1])
		if index > highest {
			highest = index
		}
	}
	if highest > plain {
		return highest
	}
	return plain
}

// A statement completed at runtime is formatted with the arguments of its PostgreSQL
// text, so the MySQL text must take the same number of arguments.
func Test_MySQLStatementsFormatLikeTheirPostgresText(t *testing.T) {
	for _, query := range AllQueries() {
		text, listed := mysqlStatements[query.ID]
		if !listed {
			continue
		}
		if got, want := formatArgumentCount(text), formatArgumentCount(query.Query); got != want {
			t.Errorf("%s: the MySQL text takes %d arguments, the PostgreSQL text %d", query.ID, got, want)
		}
	}
}
