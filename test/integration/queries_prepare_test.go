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
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

// Test_QueriesPrepare prepares every declared statement against the datasource
// the suite runs on, which is PostgreSQL by default. The unit tests cover the
// SQLite text.
func Test_QueriesPrepare(t *testing.T) {

	for name, query := range scripts.AllQueries() {
		t.Run(name, func(t *testing.T) {
			statement := setup.CompleteStatement(name, query.GetQuery(suiteDBType))
			stmt, err := suiteDB.Prepare(statement)
			if err != nil {
				t.Fatalf("failed to prepare the query: %v\n%s", err, statement)
			}
			_ = stmt.Close()
		})
	}
}
