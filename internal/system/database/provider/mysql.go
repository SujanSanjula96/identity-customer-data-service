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

package provider

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
)

// defaultMySQLPort is the port MySQL listens on, used when datasource.port is
// not set.
const defaultMySQLPort = 3306

// mysqlCollation is the connection collation. It is binary, like the schema's
// columns, so a value compares the same way in a statement and in a column.
const mysqlCollation = "utf8mb4_bin"

// mysqlTLS maps datasource.sslmode to the driver's tls option. The values are
// the PostgreSQL sslmode names, so one setting reads the same on both servers.
func mysqlTLS(sslMode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(sslMode)) {
	case "", sslModeDisable:
		return optionFalse, nil
	case sslModeRequire:
		// Encrypted, but the certificate is not verified.
		return "skip-verify", nil
	case sslModeVerifyCA, sslModeVerifyFull:
		return optionTrue, nil
	default:
		return "", fmt.Errorf("datasource.sslmode %q is not supported for mysql: use disable, require, "+
			"verify-ca or verify-full", sslMode)
	}
}

// MySQLDSN builds the connection string of the MySQL datasource. The integration
// suite builds its test pool with it too, so the suite runs on the settings
// production uses.
//
// Times are read as UTC and the session runs in UTC, so now() and the stored
// DATETIME values agree. clientFoundRows makes an UPDATE report the rows it
// matched, as PostgreSQL does, and not only the rows whose values changed.
func MySQLDSN(ds config.DataSourceConfig, connectTimeout time.Duration) (string, error) {
	tlsMode, err := mysqlTLS(ds.SSLMode)
	if err != nil {
		return "", err
	}

	port := ds.Port
	if port == 0 {
		port = defaultMySQLPort
	}

	driverConfig := mysqldriver.NewConfig()
	driverConfig.User = ds.Username
	driverConfig.Passwd = ds.Password
	driverConfig.Net = "tcp"
	driverConfig.Addr = net.JoinHostPort(ds.Hostname, strconv.Itoa(port))
	driverConfig.DBName = ds.Name
	driverConfig.Collation = mysqlCollation
	driverConfig.ParseTime = true
	driverConfig.Loc = time.UTC
	driverConfig.ClientFoundRows = true
	driverConfig.Timeout = connectTimeout
	driverConfig.TLSConfig = tlsMode
	driverConfig.Params = map[string]string{"time_zone": "'+00:00'"}

	return driverConfig.FormatDSN(), nil
}

// getMySQLDB opens the MySQL pool once and returns it on every later call.
//
// The DSN timeout bounds one connection attempt, and the check that runs when
// the pool opens is bounded by the same number, so an unreachable server fails
// the start within a known time.
func getMySQLDB() (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()

	if closed {
		return nil, ErrDatabaseClosed
	}
	if mysqlHandle != nil {
		return mysqlHandle, nil
	}

	runtimeConfig := config.GetCDSRuntime().Config

	dbConfig, err := getDBConfig(runtimeConfig)
	if err != nil {
		return nil, err
	}

	settings, err := resolveMySQLPoolSettings(runtimeConfig.DataSource.MySQL)
	if err != nil {
		return nil, err
	}

	db, err := openServerPool(dbConfig, settings)
	if err != nil {
		return nil, err
	}

	mysqlHandle = db
	return mysqlHandle, nil
}

// openServerPool opens a pool, applies its settings and verifies that the server
// answers within the connect timeout. The caller publishes the handle only on
// success, so a pool no caller can reach is not cached.
func openServerPool(dbConfig DBConfig, settings serverPoolSettings) (*sql.DB, error) {
	db, err := sql.Open(dbConfig.driverName, dbConfig.dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to read the datasource settings: %w", err)
	}
	applyPoolSettings(db, settings)

	ctx, cancel := context.WithTimeout(context.Background(), settings.connectTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("failed to reach the database within %s: %w (close error: %w)",
				settings.connectTimeout, err, closeErr)
		}
		return nil, fmt.Errorf("failed to reach the database within %s: %w", settings.connectTimeout, err)
	}

	return db, nil
}
