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
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
)

// errCallback stands for the failure a store's own statement produces.
var errCallback = errors.New("the callback failed")

// errRollback stands for a rollback that fails for a real reason, which is
// the case a caller could not see before.
var errRollback = errors.New("the rollback failed")

// Test_WithTransaction_commitsOnSuccess checks the successful path: the
// transaction commits once, and nothing rolls back.
func Test_WithTransaction_commitsOnSuccess(t *testing.T) {

	recorder := &txRecorder{}
	dbClient := openRecordedDB(t, recorder)

	called := false
	err := WithTransaction(context.Background(), dbClient, func(tx *model.Tx) error {
		called = true
		return nil
	})

	if err != nil {
		t.Fatalf("want no error, got %v", err)
	}
	if !called {
		t.Fatal("the callback never ran")
	}
	assertCounts(t, recorder, 1, 0)
}

// Test_WithTransaction_rollsBackOnError checks that a callback failure rolls
// the transaction back, commits nothing, and returns the callback's own error
// unchanged.
func Test_WithTransaction_rollsBackOnError(t *testing.T) {

	recorder := &txRecorder{}
	dbClient := openRecordedDB(t, recorder)

	err := WithTransaction(context.Background(), dbClient, func(tx *model.Tx) error {
		return errCallback
	})

	if !errors.Is(err, errCallback) {
		t.Fatalf("want the callback error, got %v", err)
	}
	assertCounts(t, recorder, 0, 1)
}

// Test_WithTransaction_joinsARollbackFailure is the reason the helper returns
// errors instead of logging them. When both the callback and the rollback
// fail, the caller must be able to see each one.
func Test_WithTransaction_joinsARollbackFailure(t *testing.T) {

	recorder := &txRecorder{rollbackErr: errRollback}
	dbClient := openRecordedDB(t, recorder)

	err := WithTransaction(context.Background(), dbClient, func(tx *model.Tx) error {
		return errCallback
	})

	if !errors.Is(err, errCallback) {
		t.Fatalf("the rollback failure hid the callback error: %v", err)
	}
	if !errors.Is(err, errRollback) {
		t.Fatalf("the rollback failure was not reported: %v", err)
	}
	assertCounts(t, recorder, 0, 1)
}

// Test_WithTransaction_keepsAServerError checks that errors.As still finds
// the error type a store returns, through the join.
func Test_WithTransaction_keepsAServerError(t *testing.T) {

	recorder := &txRecorder{rollbackErr: errRollback}
	dbClient := openRecordedDB(t, recorder)

	want := &storeError{}
	err := WithTransaction(context.Background(), dbClient, func(tx *model.Tx) error {
		return want
	})

	var got *storeError
	if !errors.As(err, &got) {
		t.Fatalf("the store's own error type was lost: %v", err)
	}
	if got != want {
		t.Fatal("errors.As found a different error")
	}
}

// storeError stands for the error type a store returns, such as a ServerError.
type storeError struct{}

func (e *storeError) Error() string { return "the store failed" }

// Test_WithTransaction_ignoresErrTxDone covers the callback that completed the
// transaction itself and then failed. The transaction is already done, so
// there is nothing to report beyond the callback's error.
func Test_WithTransaction_ignoresErrTxDone(t *testing.T) {

	recorder := &txRecorder{}
	dbClient := openRecordedDB(t, recorder)

	err := WithTransaction(context.Background(), dbClient, func(tx *model.Tx) error {
		if commitErr := tx.Commit(); commitErr != nil {
			t.Fatal(commitErr)
		}
		return errCallback
	})

	if !errors.Is(err, errCallback) {
		t.Fatalf("want the callback error alone, got %v", err)
	}
	if errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("sql.ErrTxDone was reported as a failure: %v", err)
	}
	// database/sql answers the rollback itself once the transaction is done,
	// so the datasource sees one commit and no rollback.
	assertCounts(t, recorder, 1, 0)
}

// Test_WithTransaction_doesNotRunTheCallbackWhenBeginFails checks that a
// transaction which never started runs nothing and reports why.
func Test_WithTransaction_doesNotRunTheCallbackWhenBeginFails(t *testing.T) {

	beginErr := errors.New("the datasource refused the transaction")
	recorder := &txRecorder{beginErr: beginErr}
	dbClient := openRecordedDB(t, recorder)

	called := false
	err := WithTransaction(context.Background(), dbClient, func(tx *model.Tx) error {
		called = true
		return nil
	})

	if called {
		t.Fatal("the callback ran although the transaction never started")
	}
	if !errors.Is(err, beginErr) {
		t.Fatalf("want the begin error, got %v", err)
	}
	assertCounts(t, recorder, 0, 0)
}

// Test_WithTransaction_returnsACommitFailure covers the commit that fails.
// The caller must see it, because the data was not written.
func Test_WithTransaction_returnsACommitFailure(t *testing.T) {

	commitErr := errors.New("the commit failed")
	recorder := &txRecorder{commitErr: commitErr}
	dbClient := openRecordedDB(t, recorder)

	err := WithTransaction(context.Background(), dbClient, func(tx *model.Tx) error {
		return nil
	})

	if !errors.Is(err, commitErr) {
		t.Fatalf("want the commit error, got %v", err)
	}
	assertCounts(t, recorder, 1, 0)
}

// Test_WithTransaction_rollsBackAndRepanics covers a defect inside the
// callback. The transaction must be released, and the panic must reach the
// caller unchanged rather than become an ordinary error.
func Test_WithTransaction_rollsBackAndRepanics(t *testing.T) {

	recorder := &txRecorder{}
	dbClient := openRecordedDB(t, recorder)

	want := "the callback panicked"

	func() {
		defer func() {
			got := recover()
			if got == nil {
				t.Fatal("the panic did not reach the caller")
			}
			if got != want {
				t.Fatalf("got panic value %v, want %v", got, want)
			}
		}()

		_ = WithTransaction(context.Background(), dbClient, func(tx *model.Tx) error {
			panic(want)
		})
	}()

	assertCounts(t, recorder, 0, 1)
}

// Test_WithTransaction_releasesTheOnlyConnection is the case a bounded pool
// turns into an outage. A failed transaction must give its connection back.
func Test_WithTransaction_releasesTheOnlyConnection(t *testing.T) {

	db := openSharedPool(t)
	db.SetMaxOpenConns(1)
	dbClient := NewSharedDBClient(db, database.TypeSQLite)

	err := WithTransaction(context.Background(), dbClient, func(tx *model.Tx) error {
		return errCallback
	})
	if !errors.Is(err, errCallback) {
		t.Fatalf("want the callback error, got %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := dbClient.ExecuteQueryContext(ctx, testPing); err != nil {
		t.Fatalf("the failed transaction kept the only connection: %v", err)
	}
}

// Test_WithTransaction_commitsTheWork checks against a real datasource that
// the successful path leaves the statements in place.
func Test_WithTransaction_commitsTheWork(t *testing.T) {

	db := openSharedPool(t)
	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	dbClient := NewSharedDBClient(db, database.TypeSQLite)

	insert := model.DBQuery{ID: "TST-WTX-01", Query: `INSERT INTO items (id) VALUES (?)`}
	err := WithTransaction(context.Background(), dbClient, func(tx *model.Tx) error {
		_, execErr := tx.ExecContext(context.Background(), insert, 1)
		return execErr
	})
	if err != nil {
		t.Fatalf("want no error, got %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("got %d rows, want 1", count)
	}
}

// Test_WithTransaction_undoesTheWorkOnError checks against a real datasource
// that a failed transaction leaves nothing behind.
func Test_WithTransaction_undoesTheWorkOnError(t *testing.T) {

	db := openSharedPool(t)
	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	dbClient := NewSharedDBClient(db, database.TypeSQLite)

	insert := model.DBQuery{ID: "TST-WTX-02", Query: `INSERT INTO items (id) VALUES (?)`}
	err := WithTransaction(context.Background(), dbClient, func(tx *model.Tx) error {
		if _, execErr := tx.ExecContext(context.Background(), insert, 1); execErr != nil {
			return execErr
		}
		return errCallback
	})
	if !errors.Is(err, errCallback) {
		t.Fatalf("want the callback error, got %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("the rollback left %d rows behind, want 0", count)
	}
}

// assertCounts checks how the datasource saw the transaction end. No path may
// both commit and roll back.
func assertCounts(t *testing.T, recorder *txRecorder, commits, rollbacks int64) {

	t.Helper()

	if got := recorder.commits.Load(); got != commits {
		t.Errorf("got %d commits, want %d", got, commits)
	}
	if got := recorder.rollbacks.Load(); got != rollbacks {
		t.Errorf("got %d rollbacks, want %d", got, rollbacks)
	}
}
