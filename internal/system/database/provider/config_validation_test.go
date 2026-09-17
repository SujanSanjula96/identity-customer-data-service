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
	"strings"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// Test_ValidateDataSource_rejectsInvalidNumbers checks that a configuration
// mistake fails the start, and that the message names the key the operator has
// to edit.
func Test_ValidateDataSource_rejectsInvalidNumbers(t *testing.T) {

	// withPostgres returns a complete PostgreSQL configuration with the pool
	// block replaced.
	withPostgres := func(pool config.PostgresConfig) config.DataSourceConfig {
		ds := postgresDataSource("postgres").DataSource
		ds.Postgres = pool
		return ds
	}

	testCases := []struct {
		name       string
		dataSource config.DataSourceConfig
		expectKey  string
	}{
		{
			name:       "a negative open limit",
			dataSource: withPostgres(config.PostgresConfig{MaxOpenConns: -1}),
			expectKey:  "datasource.postgres.max_open_conns",
		},
		{
			name:       "a negative idle limit",
			dataSource: withPostgres(config.PostgresConfig{MaxIdleConns: -5}),
			expectKey:  "datasource.postgres.max_idle_conns",
		},
		{
			name:       "a negative connection lifetime",
			dataSource: withPostgres(config.PostgresConfig{ConnMaxLifetimeSeconds: -1}),
			expectKey:  "datasource.postgres.conn_max_lifetime_seconds",
		},
		{
			name:       "a negative idle time",
			dataSource: withPostgres(config.PostgresConfig{ConnMaxIdleTimeSeconds: -1}),
			expectKey:  "datasource.postgres.conn_max_idle_time_seconds",
		},
		{
			name:       "a negative connect timeout",
			dataSource: withPostgres(config.PostgresConfig{ConnectTimeoutSeconds: -1}),
			expectKey:  "datasource.postgres.connect_timeout_seconds",
		},
		{
			name: "an idle limit above the open limit",
			dataSource: withPostgres(config.PostgresConfig{
				MaxOpenConns: 5,
				MaxIdleConns: 10,
			}),
			expectKey: "datasource.postgres.max_idle_conns",
		},
		{
			name: "a query timeout below zero",
			dataSource: func() config.DataSourceConfig {
				ds := postgresDataSource("postgres").DataSource
				ds.QueryTimeoutSeconds = -1
				return ds
			}(),
			expectKey: "datasource.query_timeout_seconds",
		},
		{
			name: "a transaction timeout below zero on the inbuilt database",
			dataSource: config.DataSourceConfig{
				Type:             database.TypeSQLite,
				TxTimeoutSeconds: -1,
			},
			expectKey: "datasource.tx_timeout_seconds",
		},
		{
			name: "a negative open limit on the inbuilt database",
			dataSource: config.DataSourceConfig{
				Type:   database.TypeSQLite,
				SQLite: config.SQLiteConfig{MaxOpenConns: -1},
			},
			expectKey: "datasource.sqlite.max_open_conns",
		},
		{
			name: "a lifetime that would overflow a duration",
			dataSource: withPostgres(config.PostgresConfig{
				ConnMaxLifetimeSeconds: maxConfigurableSeconds + 1,
			}),
			expectKey: "datasource.postgres.conn_max_lifetime_seconds",
		},
		{
			name: "a query timeout that would overflow a duration",
			dataSource: func() config.DataSourceConfig {
				ds := postgresDataSource("postgres").DataSource
				ds.QueryTimeoutSeconds = 1 << 62
				return ds
			}(),
			expectKey: "datasource.query_timeout_seconds",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateDataSource(testCase.dataSource)
			if err == nil {
				t.Fatal("expected the configuration to be rejected")
			}
			if !strings.Contains(err.Error(), testCase.expectKey) {
				t.Errorf("expected the error to name %q, got %v", testCase.expectKey, err)
			}
		})
	}
}

// Test_ValidateDataSource_reportsEveryProblem checks that an operator can fix
// a configuration in one pass rather than one mistake per restart.
func Test_ValidateDataSource_reportsEveryProblem(t *testing.T) {

	ds := postgresDataSource("postgres").DataSource
	ds.QueryTimeoutSeconds = -1
	ds.Postgres = config.PostgresConfig{MaxOpenConns: -1, ConnectTimeoutSeconds: -1}

	err := ValidateDataSource(ds)
	if err == nil {
		t.Fatal("expected the configuration to be rejected")
	}

	for _, key := range []string{
		"datasource.query_timeout_seconds",
		"datasource.postgres.max_open_conns",
		"datasource.postgres.connect_timeout_seconds",
	} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("expected the error to name %q, got %v", key, err)
		}
	}
}

// Test_ValidateDataSource_acceptsValidNumbers pins what stays accepted. Zero
// keeps its meaning, and the PostgreSQL block is ignored on the inbuilt
// database, which is what the Helm chart depends on.
func Test_ValidateDataSource_acceptsValidNumbers(t *testing.T) {

	accepted := map[string]config.DataSourceConfig{
		"every pool setting left empty": postgresDataSource("postgres").DataSource,
		"a complete pool configuration": func() config.DataSourceConfig {
			ds := postgresDataSource("postgres").DataSource
			ds.QueryTimeoutSeconds = 30
			ds.TxTimeoutSeconds = 30
			ds.Postgres = config.PostgresConfig{
				MaxOpenConns:           25,
				MaxIdleConns:           25,
				ConnMaxLifetimeSeconds: 1800,
				ConnMaxIdleTimeSeconds: 300,
				ConnectTimeoutSeconds:  10,
			}
			return ds
		}(),
		"an idle limit below the open limit": func() config.DataSourceConfig {
			ds := postgresDataSource("postgres").DataSource
			ds.Postgres = config.PostgresConfig{MaxOpenConns: 25, MaxIdleConns: 5}
			return ds
		}(),
		// The open limit is not configured, so the idle limit is checked
		// against the default rather than against zero.
		"an idle limit with no open limit": func() config.DataSourceConfig {
			ds := postgresDataSource("postgres").DataSource
			ds.Postgres = config.PostgresConfig{MaxIdleConns: 10}
			return ds
		}(),
		"invalid PostgreSQL settings on the inbuilt database": {
			Type:     database.TypeSQLite,
			Postgres: config.PostgresConfig{MaxOpenConns: -1, MaxIdleConns: -1},
		},
	}

	for name, ds := range accepted {
		if err := ValidateDataSource(ds); err != nil {
			t.Errorf("expected %s to be accepted: %v", name, err)
		}
	}
}
