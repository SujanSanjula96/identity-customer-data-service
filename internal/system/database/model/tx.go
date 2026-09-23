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
type Tx struct {
	internal *sql.Tx
	dbType   string
}

// NewTx wraps a transaction with the dialect of the connection it belongs to.
func NewTx(tx *sql.Tx, dbType string) *Tx {

	return &Tx{
		internal: tx,
		dbType:   dbType,
	}
}

// Commit commits the transaction.
func (t *Tx) Commit() error {

	return t.internal.Commit()
}

// Rollback rolls back the transaction.
func (t *Tx) Rollback() error {

	return t.internal.Rollback()
}

// ExecContext runs a statement that returns no rows, under the caller's
// context. The transaction carries no context of its own, so each statement
// takes the one its caller is working under.
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

// QueryContext runs a statement that returns rows, under the caller's context.
// The caller must close the rows.
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

// QueryRowsContext runs a statement that returns rows, reads every row into a
// map, and closes the rows. It reads a row exactly as the client does outside a
// transaction, so a store can take its reads and its writes into one
// transaction without changing how it reads them.
func (t *Tx) QueryRowsContext(ctx context.Context, query DBQuery, args ...interface{}) (
	[]map[string]interface{}, error) {

	rows, err := t.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	return database.ScanRows(rows, t.dbType)
}

// Exec runs a statement that returns no rows, under context.Background.
//
// Deprecated: use ExecContext. A statement without a context cannot be ended
// with the caller that started the transaction.
func (t *Tx) Exec(query DBQuery, args ...interface{}) (sql.Result, error) {

	return t.ExecContext(context.Background(), query, args...)
}

// Query runs a statement that returns rows, under context.Background. The
// caller must close them.
//
// Deprecated: use QueryContext. A statement without a context cannot be ended
// with the caller that started the transaction.
func (t *Tx) Query(query DBQuery, args ...interface{}) (*sql.Rows, error) {

	return t.QueryContext(context.Background(), query, args...)
}
