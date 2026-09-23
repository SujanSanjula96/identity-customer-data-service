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
	"fmt"

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
	// DBType is for the few statements a store builds at runtime, whose bind
	// arguments differ per datasource. It is not for selecting a statement.
	DBType() string
	Close() error
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
// context covers the whole call.
func (client *DBClient) ExecuteQueryContext(ctx context.Context, query model.DBQuery, args ...interface{}) (
	[]map[string]interface{}, error) {

	if client.dbType == database.TypeSQLite {
		args = database.NormalizeSQLiteArgs(args)
	}

	rows, err := client.db.QueryContext(ctx, query.GetQuery(client.dbType), args...)
	if err != nil {
		return nil, fmt.Errorf("query %s failed: %w", query.ID, err)
	}

	return database.ScanRows(rows, client.dbType)
}

// BeginTxContext starts a new database transaction under the caller's context.
//
// A transaction holds its connection until it ends. When the context ends
// first, database/sql rolls the transaction back and returns that connection.
func (client *DBClient) BeginTxContext(ctx context.Context) (*model.Tx, error) {

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

// DBType returns the datasource type this client is connected to.
func (client *DBClient) DBType() string {

	return client.dbType
}

// Close is a no-op. The pool belongs to the process, which closes it at
// shutdown through provider.CloseDB.
func (client *DBClient) Close() error {

	return nil
}
