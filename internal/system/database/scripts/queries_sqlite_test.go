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

package scripts_test

import (
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

// TestQueryIDsAreUniqueAndPopulated guards the property the IDs exist for: an
// empty or reused ID names the wrong statement in a failure, or none.
func TestQueryIDsAreUniqueAndPopulated(t *testing.T) {

	owners := map[string]string{}
	for name, query := range scripts.AllQueries() {
		if query.ID == "" {
			t.Errorf("%s has no ID", name)
			continue
		}
		if owner, taken := owners[query.ID]; taken {
			t.Errorf("%s and %s share the ID %q", owner, name, query.ID)
			continue
		}
		owners[query.ID] = name
	}
}

// TestQueriesResolveForEverySupportedType guards against a statement that
// resolves to the empty string, which a store would send to the database.
func TestQueriesResolveForEverySupportedType(t *testing.T) {

	for name, query := range scripts.AllQueries() {
		for _, dbType := range database.SupportedTypes {
			if query.GetQuery(dbType) == "" {
				t.Errorf("%s resolves to an empty statement for %q", name, dbType)
			}
		}
	}
}

// TestSQLiteQueriesPrepare prepares every SQLite statement against the inbuilt
// schema, covering both dbscripts/sqlite.sql and the SQLite variants in
// queries.go. The integration suite prepares the PostgreSQL text.
func TestSQLiteQueriesPrepare(t *testing.T) {

	testDB, err := setup.SetupTestSQLite()
	if err != nil {
		t.Fatalf("failed to set up the inbuilt test database: %v", err)
	}
	defer testDB.Terminate()

	for name, query := range scripts.AllQueries() {
		t.Run(name, func(t *testing.T) {
			statement := setup.CompleteStatement(name, query.GetQuery(database.TypeSQLite))
			stmt, err := testDB.DB.Prepare(statement)
			if err != nil {
				t.Fatalf("failed to prepare the query: %v\n%s", err, statement)
			}
			_ = stmt.Close()
		})
	}
}
