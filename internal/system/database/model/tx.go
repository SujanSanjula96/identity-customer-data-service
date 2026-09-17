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
	"fmt"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// Tx is a transaction that carries the dialect of the connection it started on,
// so a store runs statements inside a transaction the same way it runs them
// outside one.
//
// A transaction holds its connection from the first statement until Commit or
// Rollback. It therefore carries the context it began on, which bounds how long
// it can hold that connection. When that context ends, database/sql rolls the
// transaction back and returns the connection to the pool. A transaction that
// no code ever commits or rolls back cannot hold a connection for ever.
type Tx struct {
	internal *sql.Tx
	dbType   string
	// ctx is the context the transaction began on. Every statement that the
	// caller runs without its own context uses it.
	ctx context.Context
	// cancel releases ctx. It is nil when the caller owns the context.
	cancel context.CancelFunc
}

// NewTx wraps a transaction with the dialect of the connection it belongs to.
//
// ctx is the context the transaction began on. cancel releases it, and may be
// nil when the caller owns the context. Commit and Rollback call it.
func NewTx(ctx context.Context, cancel context.CancelFunc, tx *sql.Tx, dbType string) *Tx {

	return &Tx{
		internal: tx,
		dbType:   dbType,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Commit commits the transaction.
func (t *Tx) Commit() error {

	// The cancel runs after Commit, because it would otherwise roll the
	// transaction back before Commit reaches the server.
	if t.cancel != nil {
		defer t.cancel()
	}
	return t.internal.Commit()
}

// Rollback rolls the transaction back.
func (t *Tx) Rollback() error {

	if t.cancel != nil {
		defer t.cancel()
	}
	return t.internal.Rollback()
}

// Exec runs a statement that returns no rows, under the transaction's context.
func (t *Tx) Exec(query DBQuery, args ...interface{}) (sql.Result, error) {

	return t.ExecContext(t.ctx, query, args...)
}

// ExecContext runs a statement that returns no rows, under the given context.
func (t *Tx) ExecContext(ctx context.Context, query DBQuery, args ...interface{}) (sql.Result, error) {

	if t.dbType == database.TypeSQLite {
		args = database.NormalizeSQLiteArgs(args)
	}

	result, err := t.internal.ExecContext(ctx, query.GetQuery(t.dbType), args...)
	if err != nil {
		return nil, fmt.Errorf("query %s failed: %w", query.ID, err)
	}
	return result, nil
}

// Query runs a statement that returns rows, under the transaction's context.
// The caller must close them.
func (t *Tx) Query(query DBQuery, args ...interface{}) (*sql.Rows, error) {

	return t.QueryContext(t.ctx, query, args...)
}

// QueryContext runs a statement that returns rows, under the given context. The
// caller must close them.
func (t *Tx) QueryContext(ctx context.Context, query DBQuery, args ...interface{}) (*sql.Rows, error) {

	if t.dbType == database.TypeSQLite {
		args = database.NormalizeSQLiteArgs(args)
	}

	rows, err := t.internal.QueryContext(ctx, query.GetQuery(t.dbType), args...)
	if err != nil {
		return nil, fmt.Errorf("query %s failed: %w", query.ID, err)
	}
	return rows, nil
}
