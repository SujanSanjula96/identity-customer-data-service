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

package provider

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	// Registers the sqlserver driver with database/sql.
	_ "github.com/microsoft/go-mssqldb"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
)

// defaultMSSQLPort is the port SQL Server listens on, used when datasource.port
// is not set.
const defaultMSSQLPort = 1433

// mssqlTLS maps datasource.sslmode to the driver's encrypt and
// TrustServerCertificate options. The values are the PostgreSQL sslmode names,
// so one setting reads the same on every server.
func mssqlTLS(sslMode string) (encrypt, trustServerCertificate string, err error) {
	switch strings.ToLower(strings.TrimSpace(sslMode)) {
	case "", sslModeDisable:
		return sslModeDisable, optionTrue, nil
	case sslModeRequire:
		// Encrypted, but the certificate is not verified.
		return optionTrue, optionTrue, nil
	case sslModeVerifyCA, sslModeVerifyFull:
		return optionTrue, optionFalse, nil
	default:
		return "", "", fmt.Errorf("datasource.sslmode %q is not supported for mssql: use disable, require, "+
			"verify-ca or verify-full", sslMode)
	}
}

// MSSQLDSN builds the connection string of the MSSQL datasource. The integration
// suite builds its test pool with it too, so the suite runs on the settings
// production uses.
func MSSQLDSN(ds config.DataSourceConfig, connectTimeout time.Duration) (string, error) {
	encrypt, trust, err := mssqlTLS(ds.SSLMode)
	if err != nil {
		return "", err
	}

	port := ds.Port
	if port == 0 {
		port = defaultMSSQLPort
	}

	query := url.Values{}
	query.Set("database", ds.Name)
	query.Set("encrypt", encrypt)
	query.Set("TrustServerCertificate", trust)
	query.Set("connection timeout", strconv.Itoa(int(connectTimeout.Seconds())))

	dsn := url.URL{
		Scheme:   "sqlserver",
		User:     url.UserPassword(ds.Username, ds.Password),
		Host:     net.JoinHostPort(ds.Hostname, strconv.Itoa(port)),
		RawQuery: query.Encode(),
	}
	return dsn.String(), nil
}

// getMSSQLDB opens the MSSQL pool once and returns it on every later call.
func getMSSQLDB() (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()

	if closed {
		return nil, ErrDatabaseClosed
	}
	if mssqlHandle != nil {
		return mssqlHandle, nil
	}

	runtimeConfig := config.GetCDSRuntime().Config

	dbConfig, err := getDBConfig(runtimeConfig)
	if err != nil {
		return nil, err
	}

	settings, err := resolveMSSQLPoolSettings(runtimeConfig.DataSource.MSSQL)
	if err != nil {
		return nil, err
	}

	db, err := openServerPool(dbConfig, settings)
	if err != nil {
		return nil, err
	}

	mssqlHandle = db
	return mssqlHandle, nil
}
