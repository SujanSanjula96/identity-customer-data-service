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
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// errFakeServerDown is what the fake driver returns when its server does not answer.
var errFakeServerDown = errors.New("the fake server is down")

// fakeServer is a database/sql driver whose connections do nothing. It stands in for a MySQL or
// SQL Server that answers, or, with down set, one that does not, so the code that opens a pool
// runs without a server.
type fakeServer struct{ down bool }

func (f fakeServer) Open(string) (driver.Conn, error) {
	if f.down {
		return nil, errFakeServerDown
	}
	return fakeConn{}, nil
}

type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not supported") }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("not supported") }

const (
	fakeUpDriver   = "cds-fake-server-up"
	fakeDownDriver = "cds-fake-server-down"
)

func init() {
	sql.Register(fakeUpDriver, fakeServer{})
	sql.Register(fakeDownDriver, fakeServer{down: true})
}

// unsupportedSSLMode is a PostgreSQL sslmode that neither MySQL nor SQL Server can honor.
const unsupportedSSLMode = "prefer"

// serverDatasources are the datasources that open a pool through openServerPool.
var serverDatasources = []string{database.TypeMySQL, database.TypeMSSQL}

// unreachableConfig points the runtime at a server nothing listens on, and bounds a connection
// attempt to one second.
func unreachableConfig(dbType string) config.Config {
	ds := mysqlDataSource()
	if dbType == database.TypeMSSQL {
		ds = mssqlDataSource()
	}
	ds.Hostname = "127.0.0.1"
	ds.Port = 1
	ds.SSLMode = "disable"
	ds.MySQL.ConnectTimeoutSeconds = 1
	ds.MSSQL.ConnectTimeoutSeconds = 1
	return config.Config{DataSource: ds}
}

// publishedHandle returns the pool the process holds for dbType.
func publishedHandle(dbType string) *sql.DB {
	dbMu.Lock()
	defer dbMu.Unlock()
	if dbType == database.TypeMSSQL {
		return mssqlHandle
	}
	return mysqlHandle
}

// seedServerHandle publishes a pool of the fake server as the process-wide handle of dbType, which
// getDB would only publish after a server answered.
func seedServerHandle(t *testing.T, dbType string) *sql.DB {
	t.Helper()

	db, err := sql.Open(fakeUpDriver, "")
	if err != nil {
		t.Fatal(err)
	}
	dbMu.Lock()
	if dbType == database.TypeMSSQL {
		mssqlHandle = db
	} else {
		mysqlHandle = db
	}
	dbMu.Unlock()
	return db
}

func Test_openServerPool_returnsAPoolThatAnswers(t *testing.T) {
	db, err := openServerPool(DBConfig{driverName: fakeUpDriver, dsn: "x"},
		serverPoolSettings{maxOpenConns: 7, maxIdleConns: 2, connMaxLifetime: time.Minute,
			connMaxIdleTime: time.Minute, connectTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if got := db.Stats().MaxOpenConnections; got != 7 {
		t.Errorf("the pool limit is %d, expected the configured 7", got)
	}
}

func Test_openServerPool_closesAPoolThatDoesNotAnswer(t *testing.T) {
	db, err := openServerPool(DBConfig{driverName: fakeDownDriver, dsn: "x"},
		serverPoolSettings{maxOpenConns: 1, maxIdleConns: 1, connectTimeout: time.Second})
	if db != nil {
		t.Error("expected no pool")
	}
	if !errors.Is(err, errFakeServerDown) || !strings.Contains(err.Error(), "failed to reach the database within") {
		t.Fatalf("expected the failure to name the timeout and wrap the cause, got %v", err)
	}
}

func Test_openServerPool_refusesAnUnknownDriver(t *testing.T) {
	_, err := openServerPool(DBConfig{driverName: "no-such-driver", dsn: "x"}, serverPoolSettings{})
	if err == nil || !strings.Contains(err.Error(), "failed to read the datasource settings") {
		t.Fatalf("expected the settings to be refused, got %v", err)
	}
}

func Test_getDB_failsWhenTheServerIsUnreachable(t *testing.T) {
	for _, dbType := range serverDatasources {
		t.Run(dbType, func(t *testing.T) {
			config.OverrideCDSRuntime(unreachableConfig(dbType))
			isolatePools(t)

			db, err := getDB(dbType)
			if err == nil || !strings.Contains(err.Error(), "failed to reach the database within") {
				t.Fatalf("expected the unreachable server to be reported, got %v", err)
			}
			if db != nil || publishedHandle(dbType) != nil {
				t.Error("a pool that never answered must not be published")
			}
		})
	}
}

func Test_getDB_refusesSettingsItCannotUse(t *testing.T) {
	for _, dbType := range serverDatasources {
		t.Run(dbType+" pool setting", func(t *testing.T) {
			cfg := unreachableConfig(dbType)
			cfg.DataSource.MySQL.MaxOpenConns = -1
			cfg.DataSource.MSSQL.MaxOpenConns = -1
			config.OverrideCDSRuntime(cfg)
			isolatePools(t)

			want := "datasource." + dbType + ".max_open_conns"
			if _, err := getDB(dbType); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("expected the pool setting to be named, got %v", err)
			}
		})

		t.Run(dbType+" sslmode", func(t *testing.T) {
			cfg := unreachableConfig(dbType)
			cfg.DataSource.SSLMode = unsupportedSSLMode
			config.OverrideCDSRuntime(cfg)
			isolatePools(t)

			if _, err := getDB(dbType); err == nil || !strings.Contains(err.Error(), "sslmode") {
				t.Fatalf("expected the sslmode to be refused, got %v", err)
			}
		})
	}
}

func Test_getDB_refusesAfterShutdownAndReusesThePublishedPool(t *testing.T) {
	for _, dbType := range serverDatasources {
		t.Run(dbType, func(t *testing.T) {
			config.OverrideCDSRuntime(unreachableConfig(dbType))
			isolatePools(t)

			seeded := seedServerHandle(t, dbType)
			got, err := getDB(dbType)
			if err != nil || got != seeded {
				t.Fatalf("expected the published pool to be reused, got %v, %v", got, err)
			}

			if err := CloseDB(); err != nil {
				t.Fatal(err)
			}
			if _, err := getDB(dbType); !errors.Is(err, ErrDatabaseClosed) {
				t.Fatalf("expected ErrDatabaseClosed, got %v", err)
			}
		})
	}
}

func Test_CloseDB_closesEveryServerPool(t *testing.T) {
	isolatePools(t)
	pools := []*sql.DB{seedServerHandle(t, database.TypeMySQL), seedServerHandle(t, database.TypeMSSQL)}

	if err := CloseDB(); err != nil {
		t.Fatal(err)
	}
	for index, pool := range pools {
		if err := pool.PingContext(context.Background()); err == nil {
			t.Errorf("pool %d is still open after CloseDB", index)
		}
	}
	if publishedHandle(database.TypeMySQL) != nil || publishedHandle(database.TypeMSSQL) != nil {
		t.Error("CloseDB must clear the published handles")
	}
}

func Test_EnsureDatabase_reportsAnUnreachableServer(t *testing.T) {
	for _, dbType := range serverDatasources {
		t.Run(dbType, func(t *testing.T) {
			config.OverrideCDSRuntime(unreachableConfig(dbType))
			isolatePools(t)

			if err := EnsureDatabase(); err == nil {
				t.Fatal("expected the unreachable server to fail the start")
			}
		})
	}
}

func Test_getDBConfig_refusesSettingsItCannotUse(t *testing.T) {
	for _, dbType := range serverDatasources {
		cfg := unreachableConfig(dbType)
		cfg.DataSource.SSLMode = unsupportedSSLMode
		if _, err := getDBConfig(cfg); err == nil {
			t.Errorf("%s: expected an sslmode error", dbType)
		}

		cfg = unreachableConfig(dbType)
		cfg.DataSource.MySQL.MaxIdleConns = -3
		cfg.DataSource.MSSQL.MaxIdleConns = -3
		if _, err := getDBConfig(cfg); err == nil {
			t.Errorf("%s: expected a pool setting error", dbType)
		}
	}
}
