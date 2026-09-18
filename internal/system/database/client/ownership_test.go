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

package client

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
)

// testPing is a statement every datasource accepts.
var testPing = model.DBQuery{ID: "TEST-01", Query: "SELECT 1"}

// openSharedPool opens an inbuilt database and returns the pool. The test owns
// it, as the process owns the pool in production.
func openSharedPool(t *testing.T) *sql.DB {

	t.Helper()

	path := filepath.Join(t.TempDir(), "cds.db")
	db, err := sql.Open(database.DriverSQLite, path+"?"+database.DefaultSQLiteOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	return db
}

// Test_Close_leavesTheSharedPoolOpen is the ownership rule.
//
// Every store runs `defer dbClient.Close()`. The pool belongs to the process,
// so that Close must release nothing. A store that closed the pool would stop
// every other store in the instance.
func Test_Close_leavesTheSharedPoolOpen(t *testing.T) {

	db := openSharedPool(t)
	first := NewSharedDBClient(db, database.TypeSQLite)
	second := NewSharedDBClient(db, database.TypeSQLite)

	// A store may close its client more than once, so repeat it.
	for attempt := 1; attempt <= 3; attempt++ {
		if err := first.Close(); err != nil {
			t.Fatalf("call %d to Close failed: %v", attempt, err)
		}
	}

	// The other client must still answer. This is the check the pointer
	// comparison alone does not make.
	if _, err := second.ExecuteQuery(testPing); err != nil {
		t.Errorf("expected the second client to keep working: %v", err)
	}

	// The closed client answers too, because Close released nothing.
	if _, err := first.ExecuteQuery(testPing); err != nil {
		t.Errorf("expected the closed client to keep working: %v", err)
	}

	// And the pool itself is untouched.
	if err := db.Ping(); err != nil {
		t.Errorf("expected the pool to stay open: %v", err)
	}
}

// Test_Close_leavesTheSharedPoolOpenUnderConcurrency runs the same rule while
// other clients are at work. Run it with -race.
func Test_Close_leavesTheSharedPoolOpenUnderConcurrency(t *testing.T) {

	db := openSharedPool(t)

	const clients = 16

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)

	failures := make([]error, clients)

	for i := 0; i < clients; i++ {
		done.Add(1)
		go func(index int) {
			defer done.Done()
			dbClient := NewSharedDBClient(db, database.TypeSQLite)
			// Every store does exactly this.
			defer func() { _ = dbClient.Close() }()

			start.Wait()
			_, failures[index] = dbClient.ExecuteQuery(testPing)
		}(i)
	}

	start.Done()
	done.Wait()

	for i, err := range failures {
		if err != nil {
			t.Errorf("client %d failed: %v", i, err)
		}
	}

	// Every client closed. The pool must still answer.
	if err := db.Ping(); err != nil {
		t.Errorf("expected the pool to outlive every client: %v", err)
	}
}
