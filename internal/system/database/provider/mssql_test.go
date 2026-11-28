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

func mssqlDataSource() config.DataSourceConfig {
	return config.DataSourceConfig{
		Type:     "mssql",
		Hostname: "db.example.com",
		Port:     1434,
		Name:     "cdsdb",
		Username: "cdsuser",
		Password: "p@ss/word",
		SSLMode:  "require",
	}
}

func Test_getDBConfig_mssql(t *testing.T) {
	dbConfig, err := getDBConfig(config.Config{DataSource: mssqlDataSource()})
	if err != nil {
		t.Fatal(err)
	}
	if dbConfig.driverName != database.DriverMSSQL {
		t.Errorf("expected the sqlserver driver, got %q", dbConfig.driverName)
	}

	parsed, err := url.Parse(dbConfig.dsn)
	if err != nil {
		t.Fatalf("the DSN %q is not a URL: %v", dbConfig.dsn, err)
	}
	password, _ := parsed.User.Password()
	if parsed.Scheme != "sqlserver" || parsed.Host != "db.example.com:1434" ||
		parsed.User.Username() != "cdsuser" || password != "p@ss/word" {
		t.Errorf("unexpected DSN %q", dbConfig.dsn)
	}
	query := parsed.Query()
	for key, want := range map[string]string{"database": "cdsdb", "encrypt": "true",
		"TrustServerCertificate": "true", "connection timeout": "10"} {
		if query.Get(key) != want {
			t.Errorf("%s is %q in the DSN, expected %q", key, query.Get(key), want)
		}
	}
}

func Test_MSSQLDSN_defaults(t *testing.T) {
	ds := mssqlDataSource()
	ds.Port = 0
	ds.SSLMode = ""

	dsn, err := MSSQLDSN(ds, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dsn, "db.example.com:1433") || !strings.Contains(dsn, "encrypt=disable") {
		t.Errorf("unexpected DSN %q", dsn)
	}
}

func Test_mssqlTLS(t *testing.T) {
	cases := map[string][2]string{
		"": {"disable", "true"}, "disable": {"disable", "true"}, "require": {"true", "true"},
		"verify-ca": {"true", "false"}, "VERIFY-FULL": {"true", "false"},
	}
	for sslMode, want := range cases {
		encrypt, trust, err := mssqlTLS(sslMode)
		if err != nil || encrypt != want[0] || trust != want[1] {
			t.Errorf("mssqlTLS(%q) = %q, %q, %v; expected %v", sslMode, encrypt, trust, err, want)
		}
	}
	if _, _, err := mssqlTLS(unsupportedSSLMode); err == nil {
		t.Error("expected an error for an sslmode SQL Server cannot honor")
	}
}

func Test_resolveMSSQLPoolSettings(t *testing.T) {
	settings, err := resolveMSSQLPoolSettings(config.MSSQLConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if settings.maxOpenConns != database.DefaultMSSQLMaxOpenConns {
		t.Errorf("the defaults were not applied: %+v", settings)
	}
	_, err = resolveMSSQLPoolSettings(config.MSSQLConfig{MaxOpenConns: 5, MaxIdleConns: 9})
	if err == nil || !strings.Contains(err.Error(), "datasource.mssql.max_idle_conns") {
		t.Errorf("expected an error naming the mssql setting, got %v", err)
	}
}

func Test_ValidateDataSource_mssql(t *testing.T) {
	if err := ValidateDataSource(mssqlDataSource()); err != nil {
		t.Fatalf("a complete mssql configuration was refused: %v", err)
	}

	ds := mssqlDataSource()
	ds.Name = ""
	if err := ValidateDataSource(ds); err == nil || !strings.Contains(err.Error(), "datasource.name") {
		t.Errorf("expected the missing name to be named, got %v", err)
	}

	ds = mssqlDataSource()
	ds.SSLMode = unsupportedSSLMode
	if err := ValidateDataSource(ds); err == nil {
		t.Error("expected an unsupported sslmode to be refused")
	}

	ds = mssqlDataSource()
	ds.MSSQL.ConnectTimeoutSeconds = -1
	if err := ValidateDataSource(ds); err == nil {
		t.Error("expected a negative pool setting to be refused")
	}
}
