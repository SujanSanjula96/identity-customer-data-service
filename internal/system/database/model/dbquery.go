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

// Package model holds the types the database client and the query layer share:
// a statement in every supported dialect, and a transaction that knows which
// dialect it runs against.
package model

import (
	"fmt"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// DBQuery is a statement in every supported dialect.
type DBQuery struct {
	// ID identifies the statement in error messages.
	ID string
	// Query is the PostgreSQL statement, and the fallback for any dialect
	// without an override.
	Query string
	// SQLiteQuery overrides Query for the inbuilt datasource. Empty means the
	// PostgreSQL statement works as written.
	SQLiteQuery string
}

// GetQuery returns the statement for dbType. A populated DBQuery never resolves
// to an empty statement.
func (q DBQuery) GetQuery(dbType string) string {

	if dbType == database.TypeSQLite && q.SQLiteQuery != "" {
		return q.SQLiteQuery
	}
	return q.Query
}

// Format applies fmt.Sprintf to every dialect variant, for statements completed
// at runtime such as a VALUES or IN list.
//
// An absent override stays absent, so the statement keeps falling back to the
// PostgreSQL text.
func (q DBQuery) Format(args ...interface{}) DBQuery {

	formatted := DBQuery{
		ID:    q.ID,
		Query: fmt.Sprintf(q.Query, args...),
	}
	if q.SQLiteQuery != "" {
		formatted.SQLiteQuery = fmt.Sprintf(q.SQLiteQuery, args...)
	}
	return formatted
}

// Append concatenates a fragment onto every dialect variant, for statements
// built by concatenation rather than Sprintf. It preserves the fallback the
// same way Format does.
func (q DBQuery) Append(suffix string) DBQuery {

	appended := DBQuery{
		ID:    q.ID,
		Query: q.Query + suffix,
	}
	if q.SQLiteQuery != "" {
		appended.SQLiteQuery = q.SQLiteQuery + suffix
	}
	return appended
}

// WithSQL returns a copy of q whose text is sqlText for every dialect, for the
// few statements a store assembles from dialect-specific fragments. The dialect
// is already applied by then, so there is nothing left to select.
func (q DBQuery) WithSQL(sqlText string) DBQuery {

	return DBQuery{
		ID:    q.ID,
		Query: sqlText,
	}
}
