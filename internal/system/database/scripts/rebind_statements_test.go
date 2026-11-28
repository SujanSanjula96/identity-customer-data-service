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
	"strconv"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

var (
	numberedPlaceholder = regexp.MustCompile(`\$(\d+)`)
	namedPlaceholder    = regexp.MustCompile(`@p\d+`)
)

// Test_EveryStatementRebinds runs the placeholder rewrite over every registered
// statement for the datasources that need it. A statement with a placeholder the
// rewrite cannot read, or one that a caller could never satisfy, fails here
// without a database.
func Test_EveryStatementRebinds(t *testing.T) {
	for name, query := range AllQueries() {
		for _, dbType := range []string{database.TypeMySQL, database.TypeMSSQL} {
			t.Run(name+"/"+dbType, func(t *testing.T) {
				text := query.GetQuery(dbType)

				matches := numberedPlaceholder.FindAllStringSubmatch(text, -1)
				highest := 0
				for _, match := range matches {
					number, err := strconv.Atoi(match[1])
					if err != nil {
						t.Fatalf("unreadable placeholder %q: %v", match[0], err)
					}
					if number > highest {
						highest = number
					}
				}

				args := make([]interface{}, highest)
				for i := range args {
					args[i] = i + 1
				}

				rewritten, boundArgs, err := database.Rebind(dbType, text, args)
				if err != nil {
					t.Fatalf("%s does not rebind for %s: %v", query.ID, dbType, err)
				}

				switch dbType {
				case database.TypeMySQL:
					if len(boundArgs) != len(matches) {
						t.Errorf("%s: %d arguments for %d placeholders", query.ID, len(boundArgs), len(matches))
					}
					if numberedPlaceholder.MatchString(rewritten) {
						t.Errorf("%s: a numbered placeholder is left: %s", query.ID, rewritten)
					}
				case database.TypeMSSQL:
					added := len(namedPlaceholder.FindAllString(rewritten, -1)) -
						len(namedPlaceholder.FindAllString(text, -1))
					if added != len(matches) {
						t.Errorf("%s: %d named parameters for %d placeholders", query.ID, added, len(matches))
					}
					if len(boundArgs) != len(args) {
						t.Errorf("%s: the arguments changed", query.ID)
					}
				}
			})
		}
	}
}
