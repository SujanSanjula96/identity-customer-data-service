/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
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
	"fmt"
	"strings"

	_ "github.com/lib/pq"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
	_ "modernc.org/sqlite"
)

// DBClientInterface defines the interface for database operations.
//
// Statements are passed as a model.DBQuery, so the client is the only place that
// selects a dialect and the stores stay datasource-agnostic.
type DBClientInterface interface {
	// Deprecated: use ExecuteQueryContext. A query without a context waits for
	// a free connection without a limit, and cannot be ended by its caller.
	ExecuteQuery(query model.DBQuery, args ...interface{}) ([]map[string]interface{}, error)
	// ExecuteQueryContext runs a query under the caller's context, and under no
	// deadline of its own. The caller ends the wait for a free connection, and
	// the query itself, by ending that context.
	ExecuteQueryContext(ctx context.Context, query model.DBQuery, args ...interface{}) (
		[]map[string]interface{}, error)
	// BeginTxContext starts a transaction under the caller's context. When that
	// context ends, database/sql rolls the transaction back and returns its
	// connection to the pool.
	BeginTxContext(ctx context.Context) (*model.Tx, error)
	// Deprecated: use BeginTxContext. A transaction without a context holds
	// its connection until some code ends it.
	BeginTx() (*model.Tx, error)
	// RunInTransaction runs fn in one transaction, and every query that runs
	// under the context fn receives joins that transaction.
	RunInTransaction(ctx context.Context, fn func(ctx context.Context) error) error
	// DBType is for the few statements a store builds at runtime, whose bind
	// arguments differ per datasource. It is not for selecting a statement.
	DBType() string
	Close() error
}

// ErrNestedTransaction is returned by BeginTxContext for a context that already
// carries a transaction. The new transaction would wait for the locks the
// first one holds, and the first one cannot end until the caller returns.
var ErrNestedTransaction = errors.New("a transaction cannot begin inside another transaction")

// txKey is the context key of the transaction that RunInTransaction started.
type txKey struct{}

// contextTx is a transaction together with the pool it belongs to.
type contextTx struct {
	db *sql.DB
	tx *model.Tx
}

// DBClient is the implementation of DBClientInterface.
type DBClient struct {
	db *sql.DB
	// dbType is the datasource type this client is connected to.
	dbType string
}

// NewSharedDBClient creates a client over the connection pool the process
// owns. Close is a no-op, so the pool outlives the client.
func NewSharedDBClient(db *sql.DB, dbType string) DBClientInterface {

	return &DBClient{
		db:     db,
		dbType: dbType,
	}
}

// ExecuteQuery executes a query under context.Background.
//
// Deprecated: use ExecuteQueryContext. A query without a context waits for a
// free connection without a limit, and cannot be ended by its caller.
func (client *DBClient) ExecuteQuery(query model.DBQuery, args ...interface{}) (
	[]map[string]interface{}, error) {

	return client.ExecuteQueryContext(context.Background(), query, args...)
}

// ExecuteQueryContext executes a query under the caller's context and returns
// the result as a slice of maps. Every row is read before it returns, so the
// context covers the whole call. When the context carries a transaction of
// this pool, the query runs inside it.
func (client *DBClient) ExecuteQueryContext(ctx context.Context, query model.DBQuery, args ...interface{}) (
	[]map[string]interface{}, error) {

	isSQLite := client.dbType == database.TypeSQLite

	var rows *sql.Rows
	var err error
	if tx := client.transactionOf(ctx); tx != nil {
		rows, err = tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
	} else {
		if isSQLite {
			args = database.NormalizeSQLiteArgs(args)
		}
		rows, err = client.db.QueryContext(ctx, query.GetQuery(client.dbType), args...)
		if err != nil {
			return nil, fmt.Errorf("query %s failed: %w", query.ID, err)
		}
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var declaredTypes []string
	if isSQLite {
		columnTypes, err := rows.ColumnTypes()
		if err != nil {
			return nil, err
		}
		declaredTypes = make([]string, len(columnTypes))
		for i, columnType := range columnTypes {
			declaredTypes[i] = columnType.DatabaseTypeName()
		}
	}

	var results []map[string]interface{}
	for rows.Next() {
		row := make([]interface{}, len(columns))
		rowPointers := make([]interface{}, len(columns))
		for i := range row {
			rowPointers[i] = &row[i]
		}

		if err := rows.Scan(rowPointers...); err != nil {
			return nil, err
		}

		result := map[string]interface{}{}
		for i, col := range columns {
			value := row[i]
			if isSQLite {
				value = normalizeSQLiteValue(value, declaredTypes[i])
			}
			// Normalize column names to lowercase for consistency.
			result[strings.ToLower(col)] = value
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

// BeginTxContext starts a new database transaction under the caller's context.
//
// A transaction holds its connection until it ends. When the context ends
// first, database/sql rolls the transaction back and returns that connection.
func (client *DBClient) BeginTxContext(ctx context.Context) (*model.Tx, error) {

	if client.transactionOf(ctx) != nil {
		return nil, ErrNestedTransaction
	}

	tx, err := client.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return model.NewTx(tx, client.dbType), nil
}

// BeginTx starts a new database transaction under context.Background.
//
// Deprecated: use BeginTxContext. A transaction without a context holds its
// connection until some code ends it.
func (client *DBClient) BeginTx() (*model.Tx, error) {

	return client.BeginTxContext(context.Background())
}

// RunInTransaction runs fn in one transaction. It commits when fn returns nil
// and rolls back when fn returns an error or panics, so either every write fn
// makes is in the database or none of them is.
//
// The context fn receives carries the transaction, and every query that runs
// under it joins the transaction, whichever store runs it. When ctx already
// carries a transaction of this pool, fn joins that one, and the outer call
// decides the outcome.
//
// fn must not start work on the pool under a context that does not carry the
// transaction. On SQLite that work waits for the write lock the transaction
// holds, and on PostgreSQL it can wait for a row the transaction has locked.
func (client *DBClient) RunInTransaction(ctx context.Context, fn func(ctx context.Context) error) (err error) {

	if client.transactionOf(ctx) != nil {
		return fn(ctx)
	}

	tx, err := client.BeginTxContext(ctx)
	if err != nil {
		return err
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil && err != nil {
			err = errors.Join(err, fmt.Errorf("rollback failed: %w", rollbackErr))
		}
	}()

	if err = fn(context.WithValue(ctx, txKey{}, &contextTx{db: client.db, tx: tx})); err != nil {
		return err
	}

	committed = true
	return tx.Commit()
}

// transactionOf returns the transaction of this pool that ctx carries, or nil.
func (client *DBClient) transactionOf(ctx context.Context) *model.Tx {

	current, ok := ctx.Value(txKey{}).(*contextTx)
	if !ok || current.db != client.db {
		return nil
	}
	return current.tx
}

// DBType returns the datasource type this client is connected to.
func (client *DBClient) DBType() string {

	return client.dbType
}

// Close is a no-op. The pool belongs to the process, which closes it at
// shutdown through provider.CloseDB.
func (client *DBClient) Close() error {

	return nil
}
