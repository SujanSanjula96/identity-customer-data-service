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
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	goora "github.com/sijms/go-ora/v2"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
)

// defaultOraclePort
const defaultOraclePort = 1521

// oracleTLS maps datasource.sslmode to the driver's SSL and SSL VERIFY options.
func oracleTLS(sslMode string) (ssl, verify string, err error) {
	switch strings.ToLower(strings.TrimSpace(sslMode)) {
	case "", sslModeDisable:
		return optionFalse, optionFalse, nil
	case sslModeRequire:
		// Encrypted, but the certificate is not verified.
		return optionTrue, optionFalse, nil
	case sslModeVerifyCA, sslModeVerifyFull:
		return optionTrue, optionTrue, nil
	default:
		return "", "", fmt.Errorf("datasource.sslmode %q is not supported for oracle: use disable, require, "+
			"verify-ca or verify-full", sslMode)
	}
}

// OracleDSN builds the connection string of the Oracle datasource.
func OracleDSN(ds config.DataSourceConfig, connectTimeout time.Duration) (string, error) {
	ssl, verify, err := oracleTLS(ds.SSLMode)
	if err != nil {
		return "", err
	}

	port := ds.Port
	if port == 0 {
		port = defaultOraclePort
	}

	// LOB FETCH=STREAM reads a CLOB or BLOB in full. The driver's default, INLINE,
	// cuts every LOB off at 32 KB without an error.
	options := map[string]string{
		"SSL":                ssl,
		"SSL VERIFY":         verify,
		"CONNECTION TIMEOUT": strconv.Itoa(int(connectTimeout.Seconds())),
		"LOB FETCH":          "STREAM",
	}

	return goora.BuildUrl(ds.Hostname, port, ds.Name, ds.Username, ds.Password, options), nil
}

// getOracleDB opens the Oracle pool once and returns it on every later call.
func getOracleDB() (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()

	if closed {
		return nil, ErrDatabaseClosed
	}
	if oracleHandle != nil {
		return oracleHandle, nil
	}

	runtimeConfig := config.GetCDSRuntime().Config

	dbConfig, err := getDBConfig(runtimeConfig)
	if err != nil {
		return nil, err
	}

	settings, err := resolveOraclePoolSettings(runtimeConfig.DataSource.Oracle)
	if err != nil {
		return nil, err
	}

	db, err := openServerPool(dbConfig, settings)
	if err != nil {
		return nil, err
	}

	oracleHandle = db
	return oracleHandle, nil
}
