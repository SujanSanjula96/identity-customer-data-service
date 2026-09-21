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

package model

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	_ "modernc.org/sqlite"
)

var (
	createItems = DBQuery{ID: "TST-TX-01", Query: `CREATE TABLE items (id INTEGER PRIMARY KEY)`}
	insertItem  = DBQuery{ID: "TST-TX-02", Query: `INSERT INTO items (id) VALUES (?)`}
	countItems  = DBQuery{ID: "TST-TX-03", Query: `SELECT COUNT(*) FROM items`}
)

// openTestPool opens an inbuilt database that holds at most maxOpen
// connections, and creates the one table the tests write to.
func openTestPool(t *testing.T, maxOpen int) *sql.DB {

	t.Helper()

	path := filepath.Join(t.TempDir(), "cds.db")
	db, err := sql.Open(database.DriverSQLite, path+"?"+database.DefaultSQLiteOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(createItems.Query); err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(maxOpen)
	return db
}

// countRows reads the number of rows the table holds.
func countRows(t *testing.T, db *sql.DB) int {

	t.Helper()

	var count int
	if err := db.QueryRow(countItems.Query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// Test_ExecContext_usesTheSuppliedContext checks that a statement inside a
// transaction ends with the context the caller passes, and not with some
// context the transaction kept.
func Test_ExecContext_usesTheSuppliedContext(t *testing.T) {

	db := openTestPool(t, 2)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	statementCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = NewTx(tx, database.TypeSQLite).ExecContext(statementCtx, insertItem, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// Test_QueryContext_usesTheSuppliedContext is the same check for a statement
// that returns rows.
func Test_QueryContext_usesTheSuppliedContext(t *testing.T) {

	db := openTestPool(t, 2)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	statementCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = NewTx(tx, database.TypeSQLite).QueryContext(statementCtx, countItems)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// Test_Rollback_afterCommit_reportsErrTxDone pins the behaviour that the
// transaction helper relies on to tell a completed transaction from a failed
// rollback.
func Test_Rollback_afterCommit_reportsErrTxDone(t *testing.T) {

	db := openTestPool(t, 2)
	internal, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tx := NewTx(internal, database.TypeSQLite)

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("want sql.ErrTxDone after a commit, got %v", err)
	}
}

// Test_Commit_returnsItsError covers the commit that fails. The caller must
// see that failure, because the data was not written.
func Test_Commit_returnsItsError(t *testing.T) {

	db := openTestPool(t, 2)

	txCtx, cancel := context.WithCancel(context.Background())
	internal, err := db.BeginTx(txCtx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tx := NewTx(internal, database.TypeSQLite)

	if _, err := tx.ExecContext(context.Background(), insertItem, 1); err != nil {
		t.Fatal(err)
	}

	// database/sql rolls the transaction back when its context ends, so the
	// commit that follows has nothing left to commit.
	cancel()
	waitForTxToEnd(t, tx)

	if err := tx.Commit(); err == nil {
		t.Fatal("want an error from the commit, got nil")
	}
	if got := countRows(t, db); got != 0 {
		t.Fatalf("the failed commit wrote %d rows, want 0", got)
	}
}

// waitForTxToEnd waits for database/sql to roll a transaction back after its
// context ended. That happens in a goroutine of its own.
func waitForTxToEnd(t *testing.T, tx *Tx) {

	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := tx.ExecContext(context.Background(), insertItem, 2); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the transaction is still open after its context ended")
}
