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
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

func oracleDataSource() config.DataSourceConfig {
	return config.DataSourceConfig{
		Type:     "oracle",
		Hostname: "db.example.com",
		Port:     1522,
		Name:     "FREEPDB1",
		Username: "cdsuser",
		Password: "p@ss/word",
		SSLMode:  "require",
	}
}

func Test_getDBConfig_oracle(t *testing.T) {
	dbConfig, err := getDBConfig(config.Config{DataSource: oracleDataSource()})
	if err != nil {
		t.Fatal(err)
	}
	if dbConfig.driverName != database.DriverOracle {
		t.Errorf("expected the oracle driver, got %q", dbConfig.driverName)
	}

	parsed, err := url.Parse(dbConfig.dsn)
	if err != nil {
		t.Fatalf("the DSN %q is not a URL: %v", dbConfig.dsn, err)
	}
	password, _ := parsed.User.Password()
	if parsed.Scheme != "oracle" || parsed.Host != "db.example.com:1522" || parsed.Path != "/FREEPDB1" ||
		parsed.User.Username() != "cdsuser" || password != "p@ss/word" {
		t.Errorf("unexpected DSN %q", dbConfig.dsn)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"SSL": "true", "SSL VERIFY": "false", "CONNECTION TIMEOUT": "10", "LOB FETCH": "STREAM",
	} {
		if query.Get(key) != want {
			t.Errorf("%s is %q in the DSN, expected %q", key, query.Get(key), want)
		}
	}
}

func Test_OracleDSN_defaults(t *testing.T) {
	ds := oracleDataSource()
	ds.Port = 0
	ds.SSLMode = ""

	dsn, err := OracleDSN(ds, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dsn, "db.example.com:1521") || !strings.Contains(dsn, "SSL=false") {
		t.Errorf("unexpected DSN %q", dsn)
	}
}

func Test_oracleTLS(t *testing.T) {
	cases := map[string][2]string{
		"": {"false", "false"}, "disable": {"false", "false"}, "require": {"true", "false"},
		"verify-ca": {"true", "true"}, "VERIFY-FULL": {"true", "true"},
	}
	for sslMode, want := range cases {
		ssl, verify, err := oracleTLS(sslMode)
		if err != nil || ssl != want[0] || verify != want[1] {
			t.Errorf("oracleTLS(%q) = %q, %q, %v; expected %v", sslMode, ssl, verify, err, want)
		}
	}
	if _, _, err := oracleTLS(unsupportedSSLMode); err == nil {
		t.Error("expected an error for an sslmode Oracle cannot honor")
	}
}

func Test_resolveOraclePoolSettings(t *testing.T) {
	settings, err := resolveOraclePoolSettings(config.OracleConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if settings.maxOpenConns != database.DefaultOracleMaxOpenConns {
		t.Errorf("the defaults were not applied: %+v", settings)
	}
	_, err = resolveOraclePoolSettings(config.OracleConfig{MaxOpenConns: 5, MaxIdleConns: 9})
	if err == nil || !strings.Contains(err.Error(), "datasource.oracle.max_idle_conns") {
		t.Errorf("expected an error naming the oracle setting, got %v", err)
	}
}

func Test_ValidateDataSource_oracle(t *testing.T) {
	if err := ValidateDataSource(oracleDataSource()); err != nil {
		t.Fatalf("a complete oracle configuration was refused: %v", err)
	}

	ds := oracleDataSource()
	ds.Name = ""
	if err := ValidateDataSource(ds); err == nil || !strings.Contains(err.Error(), "datasource.name") {
		t.Errorf("expected the missing name to be named, got %v", err)
	}

	ds = oracleDataSource()
	ds.SSLMode = unsupportedSSLMode
	if err := ValidateDataSource(ds); err == nil {
		t.Error("expected an unsupported sslmode to be refused")
	}

	ds = oracleDataSource()
	ds.Oracle.ConnectTimeoutSeconds = -1
	if err := ValidateDataSource(ds); err == nil {
		t.Error("expected a negative pool setting to be refused")
	}
}
