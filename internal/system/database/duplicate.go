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
	"errors"

	mssql "github.com/microsoft/go-mssqldb"
)

// IsDuplicateKey reports whether err is SQL Server rejecting a row because a primary key or unique
// index already holds its key: error 2627 for a constraint, 2601 for a unique index.
//
// An insert that skips existing rows with NOT EXISTS gets it when another statement inserts the same
// row between the check and the insert. The row is then stored, and running the insert again skips
// it. The other datasources skip such a row within the statement, so they do not raise it there.
func IsDuplicateKey(err error) bool {
	var mssqlErr mssql.Error
	if errors.As(err, &mssqlErr) {
		return mssqlErr.Number == 2627 || mssqlErr.Number == 2601
	}
	return false
}
