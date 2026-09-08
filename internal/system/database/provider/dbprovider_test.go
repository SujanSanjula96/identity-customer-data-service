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
	"path/filepath"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// postgresDataSource is the configuration of an existing PostgreSQL
// deployment, with no sqlite block.
func postgresDataSource(dbType string) config.Config {

	return config.Config{
		DataSource: config.DataSourceConfig{
			Type:     dbType,
			Hostname: "localhost",
			Port:     5432,
			Username: "cdsuser",
			Password: "cdspwd",
			Name:     "cdsdb",
			SSLMode:  "disable",
		},
	}
}

// Test_getDBConfig_postgres pins the PostgreSQL DSN, which the inbuilt database
// must not change.
func Test_getDBConfig_postgres(t *testing.T) {

	expectedDSN := "host=localhost port=5432 user=cdsuser password=cdspwd dbname=cdsdb sslmode=disable"

	t.Run("configured as postgres", func(t *testing.T) {
		dbConfig, err := getDBConfig(postgresDataSource("postgres"))
		if err != nil {
			t.Fatal(err)
		}
		if dbConfig.dsn != expectedDSN {
			t.Errorf("expected %q, got %q", expectedDSN, dbConfig.dsn)
		}
		if dbConfig.driverName != "postgres" {
			t.Errorf("expected the postgres driver, got %q", dbConfig.driverName)
		}
	})

	t.Run("an unrecognized type keeps using its own driver name", func(t *testing.T) {
		dbConfig, err := getDBConfig(postgresDataSource("pgx"))
		if err != nil {
			t.Fatal(err)
		}
		if dbConfig.dsn != expectedDSN {
			t.Errorf("expected %q, got %q", expectedDSN, dbConfig.dsn)
		}
		if dbConfig.driverName != "pgx" {
			t.Errorf("expected the configured driver name, got %q", dbConfig.driverName)
		}
	})
}

func Test_getDBConfig_sqlite(t *testing.T) {

	config.OverrideCDSRuntime(config.Config{})
	home := config.GetCDSRuntime().CDSHome

	testCases := []struct {
		name        string
		sqlite      config.SQLiteConfig
		expectedDSN string
	}{
		{
			name:        "no sqlite block configured",
			sqlite:      config.SQLiteConfig{},
			expectedDSN: filepath.Join(home, database.DefaultSQLitePath) + "?" + database.DefaultSQLiteOptions,
		},
		{
			name:        "a relative path resolves against CDS_HOME",
			sqlite:      config.SQLiteConfig{Path: "data/cds.db"},
			expectedDSN: filepath.Join(home, "data/cds.db") + "?" + database.DefaultSQLiteOptions,
		},
		{
			name:        "an absolute path is used as given",
			sqlite:      config.SQLiteConfig{Path: "/var/lib/cds/cds.db"},
			expectedDSN: "/var/lib/cds/cds.db?" + database.DefaultSQLiteOptions,
		},
		{
			name:        "configured options replace the defaults",
			sqlite:      config.SQLiteConfig{Path: "/tmp/cds.db", Options: "_pragma=busy_timeout(1000)"},
			expectedDSN: "/tmp/cds.db?_pragma=busy_timeout(1000)",
		},
		{
			name:        "configured options may include the separator",
			sqlite:      config.SQLiteConfig{Path: "/tmp/cds.db", Options: "?_pragma=busy_timeout(1000)"},
			expectedDSN: "/tmp/cds.db?_pragma=busy_timeout(1000)",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dbConfig, err := getDBConfig(config.Config{
				DataSource: config.DataSourceConfig{Type: "sqlite", SQLite: testCase.sqlite},
			})
			if err != nil {
				t.Fatal(err)
			}
			if dbConfig.driverName != database.DriverSQLite {
				t.Errorf("expected the sqlite driver, got %q", dbConfig.driverName)
			}
			if dbConfig.dsn != testCase.expectedDSN {
				t.Errorf("expected %q, got %q", testCase.expectedDSN, dbConfig.dsn)
			}
		})
	}
}

// Test_unsetTypeUsesTheInbuiltDatabase pins the default: a configuration with
// no type runs on the inbuilt database, even when PostgreSQL settings are
// present. The startup check is what refuses that combination.
func Test_unsetTypeUsesTheInbuiltDatabase(t *testing.T) {

	config.OverrideCDSRuntime(config.Config{})

	dbConfig, err := getDBConfig(postgresDataSource(""))
	if err != nil {
		t.Fatal(err)
	}
	if dbConfig.driverName != database.DriverSQLite {
		t.Errorf("expected the sqlite driver, got %q", dbConfig.driverName)
	}
}

func Test_ensureSQLiteDir(t *testing.T) {

	config.OverrideCDSRuntime(config.Config{})

	path := filepath.Join(t.TempDir(), "nested", "deeper", "cds.db")
	if err := ensureSQLiteDir(path); err != nil {
		t.Fatal(err)
	}

	// Creating a directory that already exists must not fail either.
	if err := ensureSQLiteDir(path); err != nil {
		t.Fatalf("expected a second call to be a no-op: %v", err)
	}
}

func Test_ValidateDataSource(t *testing.T) {

	postgres := postgresDataSource("postgres").DataSource

	// withoutSetting returns the PostgreSQL configuration with one setting
	// cleared.
	withoutSetting := func(clear func(*config.DataSourceConfig)) config.DataSourceConfig {
		ds := postgres
		clear(&ds)
		return ds
	}

	t.Run("accepted", func(t *testing.T) {
		accepted := map[string]config.DataSourceConfig{
			"a complete PostgreSQL configuration": postgres,
			"the inbuilt database, which needs no connection settings": {
				Type: "sqlite",
			},
			"no datasource configuration at all": {},
			// An unset type means the inbuilt database whatever else the
			// configuration carries, so that the default has one meaning.
			"PostgreSQL settings with no type": withoutSetting(
				func(ds *config.DataSourceConfig) { ds.Type = "" }),
			"the inbuilt database alongside PostgreSQL settings, which the " +
				"Helm chart always renders": func() config.DataSourceConfig {
				ds := postgres
				ds.Type = "sqlite"
				return ds
			}(),
		}

		for name, ds := range accepted {
			if err := ValidateDataSource(ds); err != nil {
				t.Errorf("expected %s to be accepted: %v", name, err)
			}
		}
	})

	t.Run("rejected", func(t *testing.T) {
		rejected := map[string]config.DataSourceConfig{
			"an unsupported type": {Type: "mysql"},
			"a missing hostname":  withoutSetting(func(ds *config.DataSourceConfig) { ds.Hostname = "" }),
			"a missing username":  withoutSetting(func(ds *config.DataSourceConfig) { ds.Username = "" }),
			"a missing password":  withoutSetting(func(ds *config.DataSourceConfig) { ds.Password = "" }),
			"a missing name":      withoutSetting(func(ds *config.DataSourceConfig) { ds.Name = "" }),
		}

		for name, ds := range rejected {
			if err := ValidateDataSource(ds); err == nil {
				t.Errorf("expected %s to be rejected", name)
			}
		}
	})
}

// Test_getPostgresDB_reusesOnePool pins the fix for the connection churn: the
// process opens one PostgreSQL pool and hands it to every caller. sql.Open
// builds the pool without a network call, so this needs no database server.
func Test_getPostgresDB_reusesOnePool(t *testing.T) {

	config.OverrideCDSRuntime(postgresDataSource("postgres"))
	t.Cleanup(func() { _ = CloseDB() })

	if err := CloseDB(); err != nil {
		t.Fatal(err)
	}

	first, err := getPostgresDB()
	if err != nil {
		t.Fatal(err)
	}
	second, err := getPostgresDB()
	if err != nil {
		t.Fatal(err)
	}

	if first != second {
		t.Error("expected every call to return the same pool")
	}

	if got := first.Stats().MaxOpenConnections; got != database.DefaultPostgresMaxOpenConns {
		t.Errorf("expected the pool to be bounded at %d, got %d",
			database.DefaultPostgresMaxOpenConns, got)
	}

	// A client must not close the shared pool, so that the stores can keep
	// their `defer dbClient.Close()`.
	dbClient, err := NewDBProvider().GetDBClient()
	if err != nil {
		t.Fatal(err)
	}
	if err := dbClient.Close(); err != nil {
		t.Fatal(err)
	}

	third, err := getPostgresDB()
	if err != nil {
		t.Fatal(err)
	}
	if third != first {
		t.Error("expected closing a client to leave the shared pool in place")
	}
}

// Test_CloseDB_releasesThePool checks that shutdown drops the handle, so a
// later call builds a new pool rather than one that is closed.
func Test_CloseDB_releasesThePool(t *testing.T) {

	config.OverrideCDSRuntime(postgresDataSource("postgres"))
	t.Cleanup(func() { _ = CloseDB() })

	if err := CloseDB(); err != nil {
		t.Fatal(err)
	}

	first, err := getPostgresDB()
	if err != nil {
		t.Fatal(err)
	}
	if err := CloseDB(); err != nil {
		t.Fatal(err)
	}

	second, err := getPostgresDB()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Error("expected a new pool after CloseDB")
	}
}

func Test_resolvePostgresPoolSettings(t *testing.T) {

	testCases := []struct {
		name     string
		cfg      config.PostgresConfig
		expected postgresPoolSettings
	}{
		{
			name: "an empty configuration uses every default",
			cfg:  config.PostgresConfig{},
			expected: postgresPoolSettings{
				maxOpenConns:    database.DefaultPostgresMaxOpenConns,
				maxIdleConns:    database.DefaultPostgresMaxIdleConns,
				connMaxLifetime: database.DefaultPostgresConnMaxLifetime,
				connMaxIdleTime: database.DefaultPostgresConnMaxIdleTime,
			},
		},
		{
			name: "configured values replace the defaults",
			cfg: config.PostgresConfig{
				MaxOpenConns:           10,
				MaxIdleConns:           4,
				ConnMaxLifetimeSeconds: 60,
				ConnMaxIdleTimeSeconds: 30,
			},
			expected: postgresPoolSettings{
				maxOpenConns:    10,
				maxIdleConns:    4,
				connMaxLifetime: 60 * time.Second,
				connMaxIdleTime: 30 * time.Second,
			},
		},
		{
			name: "an idle limit above the open limit is lowered to it",
			cfg: config.PostgresConfig{
				MaxOpenConns: 5,
				MaxIdleConns: 50,
			},
			expected: postgresPoolSettings{
				maxOpenConns:    5,
				maxIdleConns:    5,
				connMaxLifetime: database.DefaultPostgresConnMaxLifetime,
				connMaxIdleTime: database.DefaultPostgresConnMaxIdleTime,
			},
		},
		{
			name: "a negative value falls back to its default",
			cfg: config.PostgresConfig{
				MaxOpenConns:           -1,
				MaxIdleConns:           -1,
				ConnMaxLifetimeSeconds: -1,
				ConnMaxIdleTimeSeconds: -1,
			},
			expected: postgresPoolSettings{
				maxOpenConns:    database.DefaultPostgresMaxOpenConns,
				maxIdleConns:    database.DefaultPostgresMaxIdleConns,
				connMaxLifetime: database.DefaultPostgresConnMaxLifetime,
				connMaxIdleTime: database.DefaultPostgresConnMaxIdleTime,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := resolvePostgresPoolSettings(testCase.cfg); got != testCase.expected {
				t.Errorf("expected %+v, got %+v", testCase.expected, got)
			}
		})
	}
}
