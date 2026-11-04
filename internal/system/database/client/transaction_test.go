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
	"errors"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// Test_BeginTxContext_endsTheWaitOnTheDeadline covers the start of a
// transaction, which waits for a connection in the same way a query does.
func Test_BeginTxContext_endsTheWaitOnTheDeadline(t *testing.T) {

	dbClient := openSaturatedPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := dbClient.BeginTxContext(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the wait took %v, so the deadline did not end it", elapsed)
	}
}

// Test_BeginTxContext_endsTheWaitOnCancel covers the caller that goes away
// while the pool is full.
func Test_BeginTxContext_endsTheWaitOnCancel(t *testing.T) {

	dbClient := openSaturatedPool(t)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := dbClient.BeginTxContext(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the wait took %v, so the cancel did not end it", elapsed)
	}
}

// Test_BeginTxContext_refusesACancelledCaller checks that a caller which has
// already gone away never receives a transaction, and therefore never takes a
// connection it would have to give back.
func Test_BeginTxContext_refusesACancelledCaller(t *testing.T) {

	db := openSharedPool(t)
	dbClient := NewSharedDBClient(db, database.TypeSQLite)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tx, err := dbClient.BeginTxContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if tx != nil {
		t.Fatal("want no transaction for a cancelled caller")
	}
}
