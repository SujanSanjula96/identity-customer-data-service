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
	"encoding/json"
	"time"
)

// NormalizeArgs converts the arguments of a statement to the Go types the driver
// of dbType binds as the column types the schema declares. SQLite takes its own
// conversion, MySQL and MSSQL take byte slices as text and times in UTC, and the
// other datasources take the arguments as they are.
func NormalizeArgs(dbType string, args []interface{}) []interface{} {
	switch dbType {
	case TypeSQLite:
		return NormalizeSQLiteArgs(args)
	case TypeMySQL, TypeMSSQL:
		return normalizeServerArgs(args)
	default:
		return args
	}
}

func normalizeServerArgs(args []interface{}) []interface{} {
	if len(args) == 0 {
		return args
	}

	normalized := make([]interface{}, len(args))
	for i, arg := range args {
		switch typed := arg.(type) {
		case []byte:
			normalized[i] = string(typed)
		case json.RawMessage:
			normalized[i] = string(typed)
		case time.Time:
			normalized[i] = typed.UTC()
		default:
			normalized[i] = arg
		}
	}

	return normalized
}

// NormalizeSQLiteArgs prepares query arguments for the inbuilt database. The
// stores pass marshaled JSON as []byte; binding it as text rather than as a
// BLOB keeps the JSON functions working and the column readable.
//
// The client and the transaction wrapper both apply it, so a JSON column is
// stored the same way inside and outside a transaction.
func NormalizeSQLiteArgs(args []interface{}) []interface{} {
	if len(args) == 0 {
		return args
	}

	normalized := make([]interface{}, len(args))
	for i, arg := range args {
		if raw, ok := arg.([]byte); ok {
			normalized[i] = string(raw)
			continue
		}
		normalized[i] = arg
	}

	return normalized
}
