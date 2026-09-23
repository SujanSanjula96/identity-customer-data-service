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

package database

import (
	"database/sql"
	"strings"
)

// ScanRows reads every row into a map of column name to value, and closes the
// rows. Column names are lowercased, and an inbuilt-database value is coerced
// to the type lib/pq returns for the same column.
//
// The pool and a transaction both read through it, so a store reads the same
// row the same way inside a transaction and outside one.
func ScanRows(rows *sql.Rows, dbType string) ([]map[string]interface{}, error) {

	defer rows.Close()

	isSQLite := dbType == TypeSQLite

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
				value = NormalizeSQLiteValue(value, declaredTypes[i])
			}
			result[strings.ToLower(col)] = value
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}
