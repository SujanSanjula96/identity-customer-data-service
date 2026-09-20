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
	ExecuteQuery(query model.DBQuery, args ...interface{}) ([]map[string]interface{}, error)
	// ExecuteQueryContext runs a query under the caller's context, and under no
	// deadline of its own. The caller ends the wait for a free connection, and
	// the query itself, by ending that context.
	ExecuteQueryContext(ctx context.Context, query model.DBQuery, args ...interface{}) (
		[]map[string]interface{}, error)
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

// ExecuteQuery executes a query and returns the result as a slice of maps. It
// runs under context.Background, so the caller that needs a deadline calls
// ExecuteQueryContext instead.
func (client *DBClient) ExecuteQuery(query model.DBQuery, args ...interface{}) (
	[]map[string]interface{}, error) {

	return client.ExecuteQueryContext(context.Background(), query, args...)
}

// ExecuteQueryContext executes a query under the caller's context and returns
// the result as a slice of maps. Every row is read before it returns, so the
// context covers the whole call.
func (client *DBClient) ExecuteQueryContext(ctx context.Context, query model.DBQuery, args ...interface{}) (
	[]map[string]interface{}, error) {

	isSQLite := client.dbType == database.TypeSQLite
	if isSQLite {
		args = database.NormalizeSQLiteArgs(args)
	}

	sqlText := query.GetQuery(client.dbType)

	rows, err := client.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("query %s failed: %w", query.ID, err)
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

// BeginTx starts a new database transaction.
func (client *DBClient) BeginTx() (*model.Tx, error) {

	tx, err := client.db.Begin()
	if err != nil {
		return nil, err
	}
	return model.NewTx(tx, client.dbType), nil
}

// DBType returns the datasource type this client is connected to.
func (client *DBClient) DBType() string {

	return client.dbType
}

// Close releases the client. The connection pool belongs to the process, not
// to the client, so this is a no-op. The process closes the pool at shutdown
// through provider.CloseDB.
func (client *DBClient) Close() error {

	return nil
}
