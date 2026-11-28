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
	"strings"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

func mysqlDataSource() config.DataSourceConfig {
	return config.DataSourceConfig{
		Type:     "mysql",
		Hostname: "db.example.com",
		Port:     3307,
		Name:     "cdsdb",
		Username: "cdsuser",
		Password: "p@ss/word",
		SSLMode:  "require",
	}
}

func Test_getDBConfig_mysql(t *testing.T) {
	dbConfig, err := getDBConfig(config.Config{DataSource: mysqlDataSource()})
	if err != nil {
		t.Fatal(err)
	}
	if dbConfig.driverName != database.DriverMySQL {
		t.Errorf("expected the mysql driver, got %q", dbConfig.driverName)
	}

	expectedParts := []string{
		"cdsuser:p@ss/word@tcp(db.example.com:3307)/cdsdb?",
		"clientFoundRows=true",
		"collation=utf8mb4_bin",
		"parseTime=true",
		"timeout=10s",
		"tls=skip-verify",
		"time_zone=%27%2B00%3A00%27",
	}
	for _, part := range expectedParts {
		if !strings.Contains(dbConfig.dsn, part) {
			t.Errorf("the DSN %q does not contain %q", dbConfig.dsn, part)
		}
	}
}

func Test_mysqlDSN_defaults(t *testing.T) {
	ds := mysqlDataSource()
	ds.Port = 0
	ds.SSLMode = ""

	dsn, err := MySQLDSN(ds, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"(db.example.com:3306)", "timeout=5s"} {
		if !strings.Contains(dsn, part) {
			t.Errorf("the DSN %q does not contain %q", dsn, part)
		}
	}
	if !strings.Contains(dsn, "tls=false") {
		t.Errorf("an empty sslmode must leave TLS off: %s", dsn)
	}
}

func Test_mysqlTLS(t *testing.T) {
	cases := map[string]string{
		"": "false", "disable": "false", "DISABLE": "false", "require": "skip-verify",
		"verify-ca": "true", "verify-full": "true",
	}
	for sslMode, want := range cases {
		got, err := mysqlTLS(sslMode)
		if err != nil || got != want {
			t.Errorf("mysqlTLS(%q) = %q, %v; expected %q", sslMode, got, err, want)
		}
	}
	if _, err := mysqlTLS(unsupportedSSLMode); err == nil {
		t.Error("expected an error for an sslmode MySQL cannot honor")
	}
}

func Test_resolveMySQLPoolSettings(t *testing.T) {
	settings, err := resolveMySQLPoolSettings(config.MySQLConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if settings.maxOpenConns != database.DefaultMySQLMaxOpenConns ||
		settings.connectTimeout != database.DefaultMySQLConnectTimeout {
		t.Errorf("the defaults were not applied: %+v", settings)
	}

	_, err = resolveMySQLPoolSettings(config.MySQLConfig{MaxOpenConns: 5, MaxIdleConns: 9})
	if err == nil || !strings.Contains(err.Error(), "datasource.mysql.max_idle_conns") {
		t.Errorf("expected an error naming the mysql setting, got %v", err)
	}
}

func Test_ValidateDataSource_mysql(t *testing.T) {
	if err := ValidateDataSource(mysqlDataSource()); err != nil {
		t.Fatalf("a complete mysql configuration was refused: %v", err)
	}

	ds := mysqlDataSource()
	ds.Hostname = ""
	if err := ValidateDataSource(ds); err == nil || !strings.Contains(err.Error(), "datasource.hostname") {
		t.Errorf("expected the missing hostname to be named, got %v", err)
	}

	ds = mysqlDataSource()
	ds.SSLMode = unsupportedSSLMode
	if err := ValidateDataSource(ds); err == nil {
		t.Error("expected an unsupported sslmode to be refused")
	}

	ds = mysqlDataSource()
	ds.MySQL.MaxOpenConns = -1
	if err := ValidateDataSource(ds); err == nil {
		t.Error("expected a negative pool setting to be refused")
	}
}

func Test_getDB_refusesAnUnsupportedType(t *testing.T) {
	if _, err := getDB("db2"); err == nil {
		t.Fatal("expected an error")
	}
}
