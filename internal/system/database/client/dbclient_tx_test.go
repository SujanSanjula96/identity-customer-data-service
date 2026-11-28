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

package client

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
)

var (
	testCreateItems = model.DBQuery{ID: "TEST-TX-1", Query: `CREATE TABLE items (name TEXT PRIMARY KEY)`}
	testInsertItem  = model.DBQuery{ID: "TEST-TX-2", Query: `INSERT INTO items (name) VALUES ($1)`}
	testCountItems  = model.DBQuery{ID: "TEST-TX-3", Query: `SELECT COUNT(*) AS n FROM items`}
)

// openSingleConnectionPool opens an inbuilt database whose pool holds one connection, so a
// statement that does not run in the open transaction waits for that connection and times out.
func openSingleConnectionPool(t *testing.T) DBClientInterface {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cds.db")
	db, err := sql.Open(database.DriverSQLite, path+"?"+database.DefaultSQLiteOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	dbClient := NewSharedDBClient(db, database.TypeSQLite)
	if _, err := dbClient.ExecuteQueryContext(context.Background(), testCreateItems); err != nil {
		t.Fatal(err)
	}
	return dbClient
}

// countItems counts the rows outside any transaction.
func countItems(t *testing.T, dbClient DBClientInterface) int64 {
	t.Helper()

	rows, err := dbClient.ExecuteQueryContext(context.Background(), testCountItems)
	if err != nil {
		t.Fatal(err)
	}
	return rows[0]["n"].(int64)
}

// Test_ExecuteQueryContext_runsInTheTransactionOfTheContext checks that a store which takes its
// own client still runs in the caller's transaction. The pool has one connection, held by the
// transaction, so a statement outside it would wait until the deadline.
func Test_ExecuteQueryContext_runsInTheTransactionOfTheContext(t *testing.T) {
	dbClient := openSingleConnectionPool(t)

	tx, err := dbClient.BeginTxContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(model.ContextWithTx(context.Background(), tx), 2*time.Second)
	defer cancel()

	if _, err := dbClient.ExecuteQueryContext(ctx, testInsertItem, "a"); err != nil {
		t.Fatalf("the insert did not run in the transaction: %v", err)
	}
	rows, err := dbClient.ExecuteQueryContext(ctx, testCountItems)
	if err != nil {
		t.Fatalf("the count did not run in the transaction: %v", err)
	}
	if got := rows[0]["n"].(int64); got != 1 {
		t.Errorf("the transaction sees %d rows, want 1", got)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := countItems(t, dbClient); got != 0 {
		t.Errorf("%d rows remain after the rollback, want 0", got)
	}
}

// Test_BeginTxContext_joinsTheTransactionOfTheContext checks that a store which begins its own
// transaction inside another one leaves the commit to the outer transaction.
func Test_BeginTxContext_joinsTheTransactionOfTheContext(t *testing.T) {
	dbClient := openSingleConnectionPool(t)

	outer, err := dbClient.BeginTxContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx := model.ContextWithTx(context.Background(), outer)

	inner, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inner.ExecContext(ctx, testInsertItem, "a"); err != nil {
		t.Fatal(err)
	}
	if err := inner.Commit(); err != nil {
		t.Fatal(err)
	}

	// The inner commit did not commit, so rolling back the outer one discards the row.
	if err := outer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := countItems(t, dbClient); got != 0 {
		t.Errorf("%d rows remain after the outer rollback, want 0", got)
	}
}

// Test_Commit_rollsBackWhenAJoinedPartRolledBack checks that a part which rolled back its share
// stops the whole transaction from committing what the other parts wrote.
func Test_Commit_rollsBackWhenAJoinedPartRolledBack(t *testing.T) {
	dbClient := openSingleConnectionPool(t)

	outer, err := dbClient.BeginTxContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx := model.ContextWithTx(context.Background(), outer)
	if _, err := dbClient.ExecuteQueryContext(ctx, testInsertItem, "a"); err != nil {
		t.Fatal(err)
	}

	inner, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := inner.Rollback(); err != nil {
		t.Fatal(err)
	}

	if err := outer.Commit(); !errors.Is(err, model.ErrTxRolledBack) {
		t.Fatalf("want model.ErrTxRolledBack, got %v", err)
	}
	if got := countItems(t, dbClient); got != 0 {
		t.Errorf("%d rows remain after the joined rollback, want 0", got)
	}
}
