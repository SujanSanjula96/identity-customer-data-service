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
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
)

var testPing = model.DBQuery{ID: "TEST-01", Query: "SELECT 1"}

// openSaturatedPool opens an inbuilt database whose pool holds exactly one
// connection, and takes that connection with an open transaction. Every later
// call must wait, which is the state a bounded pool reaches under load.
func openSaturatedPool(t *testing.T, timeouts Timeouts) DBClientInterface {

	t.Helper()

	path := filepath.Join(t.TempDir(), "cds.db")
	db, err := sql.Open(database.DriverSQLite, path+"?"+database.DefaultSQLiteOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}

	holder, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Rollback() })

	return NewSharedDBClient(db, database.TypeSQLite, timeouts)
}

// Test_ExecuteQueryContext_endsTheWaitOnTheDeadline is the case the bounded
// pool creates: every connection is in use, so the query waits for one. Before
// the client passed a context, that wait had no end.
func Test_ExecuteQueryContext_endsTheWaitOnTheDeadline(t *testing.T) {

	dbClient := openSaturatedPool(t, Timeouts{})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := dbClient.ExecuteQueryContext(ctx, testPing)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the wait took %v, so the deadline did not end it", elapsed)
	}
}

// Test_ExecuteQueryContext_endsTheWaitOnCancel covers the caller that goes
// away, which is what an abandoned HTTP request does.
func Test_ExecuteQueryContext_endsTheWaitOnCancel(t *testing.T) {

	dbClient := openSaturatedPool(t, Timeouts{})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := dbClient.ExecuteQueryContext(ctx, testPing)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// Test_ExecuteQueryContext_endsTheWaitOnTheDefaultDeadline covers the caller
// whose context carries no deadline, which is what an HTTP request context is.
// The client must still bound the wait.
func Test_ExecuteQueryContext_endsTheWaitOnTheDefaultDeadline(t *testing.T) {

	dbClient := openSaturatedPool(t, Timeouts{Query: 200 * time.Millisecond})

	start := time.Now()
	_, err := dbClient.ExecuteQueryContext(context.Background(), testPing)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the wait took %v, so the default deadline did not end it", elapsed)
	}
}

// Test_BeginTxContext_endsTheWaitOnTheDeadline covers the start of a
// transaction, which also waits for a connection.
func Test_BeginTxContext_endsTheWaitOnTheDeadline(t *testing.T) {

	dbClient := openSaturatedPool(t, Timeouts{})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if _, err := dbClient.BeginTxContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded, got %v", err)
	}
}

// Test_TxStatements_respectCancellation covers a statement that runs inside a
// transaction whose context has ended.
func Test_TxStatements_respectCancellation(t *testing.T) {

	path := filepath.Join(t.TempDir(), "cds.db")
	db, err := sql.Open(database.DriverSQLite, path+"?"+database.DefaultSQLiteOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	dbClient := NewSharedDBClient(db, database.TypeSQLite, Timeouts{})

	ctx, cancel := context.WithCancel(context.Background())
	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	// The driver needs a moment to observe the cancel and roll back.
	time.Sleep(100 * time.Millisecond)

	if _, err := tx.Query(testPing); err == nil {
		t.Fatal("want an error after the transaction's context ended, got nil")
	}
}

// Test_AbandonedTx_releasesItsConnection is the case that a bounded pool turns
// into an outage. No store uses defer Rollback, so a transaction that no code
// ends would hold its connection for the life of the process. The deadline
// returns it.
func Test_AbandonedTx_releasesItsConnection(t *testing.T) {

	path := filepath.Join(t.TempDir(), "cds.db")
	db, err := sql.Open(database.DriverSQLite, path+"?"+database.DefaultSQLiteOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}

	dbClient := NewSharedDBClient(db, database.TypeSQLite, Timeouts{Tx: 200 * time.Millisecond})

	// Start a transaction and abandon it. Nothing commits or rolls it back.
	if _, err := dbClient.BeginTxContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := dbClient.ExecuteQueryContext(ctx, testPing); err != nil {
		t.Fatalf("the abandoned transaction kept its connection: %v", err)
	}
}

// Test_ReadinessCancel_leavesNoBlockedGoroutine checks that a caller which
// gives up does not leave a goroutine blocked on the pool.
func Test_ReadinessCancel_leavesNoBlockedGoroutine(t *testing.T) {

	dbClient := openSaturatedPool(t, Timeouts{})

	before := runtime.NumGoroutine()

	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, _ = dbClient.ExecuteQueryContext(ctx, testPing)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the probe goroutine never returned")
	}

	// The pool starts helper goroutines of its own, so allow a small margin.
	waitForGoroutines(t, before+2)
}

// waitForGoroutines waits for the goroutine count to fall to a limit. The
// scheduler needs a moment after a goroutine returns.
func waitForGoroutines(t *testing.T, limit int) {

	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for {
		count := runtime.NumGoroutine()
		if count <= limit {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines are still running, want at most %d", count, limit)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func Test_Timeouts_resolve(t *testing.T) {

	tests := []struct {
		name  string
		given Timeouts
		want  Timeouts
	}{
		{
			name:  "empty values take the defaults",
			given: Timeouts{},
			want:  Timeouts{Query: database.DefaultQueryTimeout, Tx: database.DefaultTxTimeout},
		},
		{
			name:  "set values are kept",
			given: Timeouts{Query: time.Second, Tx: 2 * time.Second},
			want:  Timeouts{Query: time.Second, Tx: 2 * time.Second},
		},
		{
			name:  "negative values take the defaults",
			given: Timeouts{Query: -time.Second, Tx: -time.Second},
			want:  Timeouts{Query: database.DefaultQueryTimeout, Tx: database.DefaultTxTimeout},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.given.resolve(); got != test.want {
				t.Fatalf("got %+v, want %+v", got, test.want)
			}
		})
	}
}

// Test_withDeadline_keepsTheCallersDeadline checks that the client does not
// shorten a deadline the caller set. A background job may need longer than the
// default.
func Test_withDeadline_keepsTheCallersDeadline(t *testing.T) {

	want := time.Now().Add(time.Hour)
	parent, cancelParent := context.WithDeadline(context.Background(), want)
	defer cancelParent()

	ctx, cancel := withDeadline(parent, time.Second)
	if cancel != nil {
		t.Fatal("want no cancel function when the caller owns the context")
	}
	got, ok := ctx.Deadline()
	if !ok || !got.Equal(want) {
		t.Fatalf("got deadline %v, want %v", got, want)
	}
}

// Test_withDeadline_addsOneWhenTheCallerHasNone checks the safety net.
func Test_withDeadline_addsOneWhenTheCallerHasNone(t *testing.T) {

	ctx, cancel := withDeadline(context.Background(), time.Second)
	if cancel == nil {
		t.Fatal("want a cancel function when the client owns the context")
	}
	defer cancel()

	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("want a deadline on the returned context")
	}
}
