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

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
)

var (
	createItems = model.DBQuery{ID: "TEST-01", Query: `CREATE TABLE items (name TEXT)`}
	insertItem  = model.DBQuery{ID: "TEST-02", Query: `INSERT INTO items (name) VALUES ($1)`}
	countItems  = model.DBQuery{ID: "TEST-03", Query: `SELECT count(*) AS n FROM items`}
)

// openItemsClient returns a client over a new inbuilt database with one empty
// table.
func openItemsClient(t *testing.T) DBClientInterface {

	t.Helper()
	dbClient := NewSharedDBClient(openSharedPool(t), database.TypeSQLite)
	if _, err := dbClient.ExecuteQueryContext(context.Background(), createItems); err != nil {
		t.Fatal(err)
	}
	return dbClient
}

func countOf(t *testing.T, ctx context.Context, dbClient DBClientInterface) int64 {

	t.Helper()
	rows, err := dbClient.ExecuteQueryContext(ctx, countItems)
	if err != nil {
		t.Fatal(err)
	}
	return rows[0]["n"].(int64)
}

func Test_RunInTransaction_commitsWhenTheWorkSucceeds(t *testing.T) {

	dbClient := openItemsClient(t)

	err := dbClient.RunInTransaction(context.Background(), func(ctx context.Context) error {
		_, err := dbClient.ExecuteQueryContext(ctx, insertItem, "first")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := countOf(t, context.Background(), dbClient); n != 1 {
		t.Fatalf("want 1 row after the commit, got %d", n)
	}
}

func Test_RunInTransaction_rollsBackWhenTheWorkFails(t *testing.T) {

	dbClient := openItemsClient(t)
	failure := errors.New("failure")

	err := dbClient.RunInTransaction(context.Background(), func(ctx context.Context) error {
		if _, err := dbClient.ExecuteQueryContext(ctx, insertItem, "first"); err != nil {
			return err
		}
		if n := countOf(t, ctx, dbClient); n != 1 {
			t.Fatalf("the transaction does not see its own write: got %d rows", n)
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("want the error of the work, got %v", err)
	}
	if n := countOf(t, context.Background(), dbClient); n != 0 {
		t.Fatalf("want no rows after the rollback, got %d", n)
	}
}

func Test_RunInTransaction_rollsBackWhenTheWorkPanics(t *testing.T) {

	dbClient := openItemsClient(t)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the panic did not reach the caller")
			}
		}()
		_ = dbClient.RunInTransaction(context.Background(), func(ctx context.Context) error {
			if _, err := dbClient.ExecuteQueryContext(ctx, insertItem, "first"); err != nil {
				return err
			}
			panic("failure")
		})
	}()

	if n := countOf(t, context.Background(), dbClient); n != 0 {
		t.Fatalf("want no rows after the rollback, got %d", n)
	}
}

// Test_RunInTransaction_joinsTheTransactionOfTheContext checks that an inner
// call writes inside the outer transaction, so the outer call decides whether
// that write stays.
func Test_RunInTransaction_joinsTheTransactionOfTheContext(t *testing.T) {

	dbClient := openItemsClient(t)
	failure := errors.New("failure")

	err := dbClient.RunInTransaction(context.Background(), func(ctx context.Context) error {
		inner := dbClient.RunInTransaction(ctx, func(ctx context.Context) error {
			_, err := dbClient.ExecuteQueryContext(ctx, insertItem, "inner")
			return err
		})
		if inner != nil {
			return inner
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("want the error of the outer work, got %v", err)
	}
	if n := countOf(t, context.Background(), dbClient); n != 0 {
		t.Fatalf("the inner call committed on its own: got %d rows", n)
	}
}

func Test_BeginTxContext_refusesAContextThatCarriesATransaction(t *testing.T) {

	dbClient := openItemsClient(t)

	err := dbClient.RunInTransaction(context.Background(), func(ctx context.Context) error {
		tx, err := dbClient.BeginTxContext(ctx)
		if err == nil {
			_ = tx.Rollback()
		}
		return err
	})
	if !errors.Is(err, ErrNestedTransaction) {
		t.Fatalf("want ErrNestedTransaction, got %v", err)
	}
}
