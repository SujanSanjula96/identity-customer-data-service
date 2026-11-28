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

package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// Tx is a transaction that carries the dialect of the connection it started on,
// so a store runs statements inside a transaction the same way it runs them
// outside one.
type Tx struct {
	internal *sql.Tx
	dbType   string

	// outer is the transaction this one joined, or nil when this one began the work. A joined
	// transaction leaves the commit to the code that began the work.
	outer *Tx
	// rollbackOnly records that a joined part rolled back, so the work as a whole must not
	// commit.
	rollbackOnly atomic.Bool
}

// ErrTxRolledBack says that a part of the work rolled back its share of a transaction it had
// joined, so the whole transaction was rolled back instead of committed.
var ErrTxRolledBack = errors.New("a part of the transaction rolled back, so the transaction was rolled back")

// txContextKey is the context key of the transaction a caller runs its statements in.
type txContextKey struct{}

// ContextWithTx returns a context whose statements run in tx. A store that runs a statement, or
// begins a transaction, under that context joins tx instead of taking a connection of its own,
// so work spread over several stores commits or rolls back as one.
func ContextWithTx(ctx context.Context, tx *Tx) context.Context {
	return context.WithValue(ctx, txContextKey{}, tx)
}

// TxFromContext returns the transaction the context carries, or nil.
func TxFromContext(ctx context.Context) *Tx {
	tx, _ := ctx.Value(txContextKey{}).(*Tx)
	return tx
}

// Join returns a transaction that runs its statements in t and leaves the commit to t: its
// Commit does nothing, and its Rollback makes t roll back instead of commit.
func (t *Tx) Join() *Tx {
	outer := t
	for outer.outer != nil {
		outer = outer.outer
	}
	return &Tx{internal: outer.internal, dbType: outer.dbType, outer: outer}
}

// DBType returns the dialect of the connection the transaction belongs to.
func (tx *Tx) DBType() string {
	return tx.dbType
}

// NewTx wraps a transaction with the dialect of the connection it belongs to.
func NewTx(tx *sql.Tx, dbType string) *Tx {
	return &Tx{
		internal: tx,
		dbType:   dbType,
	}
}

// Commit commits the transaction. A joined transaction leaves the commit to the one it joined.
// When a joined part rolled back, the transaction rolls back and Commit returns ErrTxRolledBack.
func (t *Tx) Commit() error {
	if t.outer != nil {
		return nil
	}
	if t.rollbackOnly.Load() {
		return errors.Join(ErrTxRolledBack, t.internal.Rollback())
	}
	return t.internal.Commit()
}

// Rollback rolls back the transaction. A joined transaction marks the one it joined, which then
// rolls back instead of committing.
func (t *Tx) Rollback() error {
	if t.outer != nil {
		t.outer.rollbackOnly.Store(true)
		return nil
	}
	return t.internal.Rollback()
}

// ExecContext runs a statement that returns no rows, under the caller's
// context. The transaction carries no context of its own, so each statement
// takes the one its caller is working under.
func (t *Tx) ExecContext(ctx context.Context, query DBQuery, args ...interface{}) (sql.Result, error) {
	sqlText, args, err := database.BindStatement(t.dbType, query.GetQuery(t.dbType), args)
	if err != nil {
		return nil, fmt.Errorf("query %s failed: %w", query.ID, err)
	}

	result, err := t.internal.ExecContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("query %s failed: %w", query.ID, err)
	}
	return result, nil
}

// QueryContext runs a statement that returns rows, under the caller's context.
// The caller must close the rows.
func (t *Tx) QueryContext(ctx context.Context, query DBQuery, args ...interface{}) (*sql.Rows, error) {
	sqlText, args, err := database.BindStatement(t.dbType, query.GetQuery(t.dbType), args)
	if err != nil {
		return nil, fmt.Errorf("query %s failed: %w", query.ID, err)
	}

	rows, err := t.internal.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("query %s failed: %w", query.ID, err)
	}
	return rows, nil
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
