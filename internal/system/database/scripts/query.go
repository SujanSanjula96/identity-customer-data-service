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

// Package scripts declares every SQL statement CDS runs, and the dialect-specific
// fragments the stores assemble at runtime.
package scripts

import (
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
)

// newQuery declares a statement.
//
// id follows the convention `CDS-<DOMAIN>-<NN>`, where DOMAIN groups the tables
// the statement touches:
//
//	CDS-APP  applications
//	CDS-SCH  profile_schema
//	CDS-UNR  unification_rules, profile_unification_*
//	CDS-PRF  profiles, profile_reference, application_data
//	CDS-CON  consent_categories, consent_category_attributes, profile_consents
//	CDS-CKI  profile_cookies
//	CDS-CFG  cds_config
//	CDS-SVC  org_service_credentials
//	CDS-IDR  blocking_keys, review_tasks, rejection_pairs
//	CDS-SYS  statements bound to no table
//
// An id is permanent: give a new statement the next unused number in its
// domain instead of renumbering the existing ones.
//
// base is the PostgreSQL statement, and is used for every other datasource
// unless an override is supplied. Most statements need no override; pass one
// only where the dialects differ. The SQLite text is passed here, and the text
// for each server datasource is listed by statement id in queries_mysql.go and
// queries_mssql.go.
func newQuery(id, base string, sqliteOverride ...string) model.DBQuery {
	query := model.DBQuery{
		ID:    id,
		Query: base,
	}
	if len(sqliteOverride) > 0 {
		query.Overrides = map[string]string{database.TypeSQLite: sqliteOverride[0]}
	}
	for dbType, statements := range serverStatements {
		if text, ok := statements[id]; ok {
			if query.Overrides == nil {
				query.Overrides = map[string]string{}
			}
			query.Overrides[dbType] = text
		}
	}
	return query
}

// serverStatements maps each server datasource to the statements written for it.
var serverStatements = map[string]map[string]string{
	database.TypeMySQL:  mysqlStatements,
	database.TypeMSSQL:  mssqlStatements,
	database.TypeOracle: oracleStatements,
}
