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
)

// openSaturatedPool opens an inbuilt database whose pool holds exactly one
// connection, and takes that connection with an open transaction. Every later
// call must wait, which is the state a bounded pool reaches under load.
func openSaturatedPool(t *testing.T) DBClientInterface {

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

	return NewSharedDBClient(db, database.TypeSQLite)
}

// Test_ExecuteQueryContext_endsTheWaitOnTheDeadline is the case the bounded
// pool creates: every connection is in use, so the query waits for one. The
// readiness check depends on this, because it runs its query under the context
// of the probe request and must end when that request does.
func Test_ExecuteQueryContext_endsTheWaitOnTheDeadline(t *testing.T) {

	dbClient := openSaturatedPool(t)

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
// away, which is what an abandoned probe request does.
func Test_ExecuteQueryContext_endsTheWaitOnCancel(t *testing.T) {

	dbClient := openSaturatedPool(t)

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

// Test_ReadinessCancel_leavesNoBlockedGoroutine checks that a caller which
// gives up does not leave a goroutine blocked on the pool. The probe runs every
// ten seconds, so a goroutine left behind on each call would accumulate.
func Test_ReadinessCancel_leavesNoBlockedGoroutine(t *testing.T) {

	dbClient := openSaturatedPool(t)

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
