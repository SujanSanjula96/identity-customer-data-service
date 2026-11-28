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

package database

import (
	"database/sql/driver"
	"errors"
	"net"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/sijms/go-ora/v2/network"
)

// IsTransient reports whether err is a database failure that the same statements can get past when
// they run again a little later: a lock that was not granted in time, a deadlock or serialization
// failure that rolled the transaction back, or a connection that was lost. It reports false for
// every other error, which running the statements again would only repeat.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, mysql.ErrInvalidConn) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	// SQLite: SQLITE_BUSY and SQLITE_LOCKED, with their extended codes, when busy_timeout ran out
	// while another connection held the write lock.
	var sqliteErr interface{ Code() int }
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & 0xff {
		case 5, 6:
			return true
		}
	}

	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch pqErr.Code {
		case "40001", // serialization_failure
			"40P01", // deadlock_detected
			"55P03", // lock_not_available
			"57P01", // admin_shutdown
			"57P03": // cannot_connect_now
			return true
		}
		return pqErr.Code.Class() == "08" // connection_exception
	}

	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		switch mysqlErr.Number {
		case 1205, // ER_LOCK_WAIT_TIMEOUT
			1213: // ER_LOCK_DEADLOCK
			return true
		}
		return false
	}

	var mssqlErr mssql.Error
	if errors.As(err, &mssqlErr) {
		switch mssqlErr.Number {
		case 1205, // chosen as deadlock victim
			1222: // lock request time out
			return true
		}
		return false
	}

	var oraErr *network.OracleError
	if errors.As(err, &oraErr) {
		switch oraErr.ErrCode {
		case 54, // resource busy, acquire with NOWAIT
			60,    // deadlock detected
			8177,  // cannot serialize access for this transaction
			30006: // resource busy, acquire with WAIT timeout expired
			return true
		}
	}
	return false
}
