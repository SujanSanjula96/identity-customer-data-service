/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
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
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/client"
)

// DBConfig represents the local database configuration.
type DBConfig struct {
	dsn        string
	driverName string
}

var (
	testDBOverride     *sql.DB
	testDBTypeOverride string
)

// SetTestDB installs a database handle used by every subsequent GetDBClient
// call, bypassing the configured datasource.
func SetTestDB(db *sql.DB, dbType string) {
	testDBOverride = db
	testDBTypeOverride = dbType
}

// The process holds one pool per datasource, opened on the first use and kept
// open. Every store shares it, so a request reuses a connection instead of a
// new TCP, TLS and authentication round trip. The client treats Close as a
// no-op, so a store that closes its client leaves the pool open.
//
// A mutex rather than a sync.Once guards the handles. A sync.Once would cache
// the failure of the first attempt for the life of the process, so a database
// that is briefly unreachable at start would break the instance for ever. A
// handle is published only after it is verified, so a failed attempt leaves
// nothing behind and the next call builds a fresh pool.
var (
	dbMu           sync.Mutex
	sqliteHandle   *sql.DB
	postgresHandle *sql.DB
	// closed records that CloseDB ran. The handles are cleared at that point,
	// so without this flag a late caller would open a fresh pool that nothing
	// would ever close.
	closed bool
)

// ErrDatabaseClosed is returned to a caller that asks for a pool after CloseDB
// ran. Shutdown closes the pools once, after the HTTP server and the workers
// stop, so a pool opened after that would leak for the rest of the process.
var ErrDatabaseClosed = errors.New("the database is closed: the server has shut down")

// DBProviderInterface defines the interface for getting database clients.
type DBProviderInterface interface {
	GetDBClient() (client.DBClientInterface, error)
	GetDBType() string
}

// DBProvider is the implementation of DBProviderInterface.
type DBProvider struct{}

// NewDBProvider creates a new instance of DBProvider.
func NewDBProvider() DBProviderInterface {

	return &DBProvider{}
}

// GetDBClient returns a database client for the configured datasource. Every
// call shares the one pool the process holds, so the client is cheap to build
// and its Close is a no-op.
func (d *DBProvider) GetDBClient() (client.DBClientInterface, error) {

	// The suite owns the test handle, so Close must leave it open.
	if testDBOverride != nil {
		return client.NewSharedDBClient(testDBOverride, database.ResolveType(testDBTypeOverride)), nil
	}

	// Production DB setup
	dbType := database.ResolveType(config.GetCDSRuntime().Config.DataSource.Type)

	db, err := getDB(dbType)
	if err != nil {
		return nil, err
	}

	return client.NewSharedDBClient(db, dbType), nil
}

// getDB returns the process-wide pool for the given datasource type.
func getDB(dbType string) (*sql.DB, error) {

	if dbType == database.TypeSQLite {
		return getSQLiteDB()
	}
	return getPostgresDB()
}

// getPostgresDB opens the PostgreSQL pool once and returns it on every later
// call. sql.Open builds the pool without a network call, so the first
// connection is made by the first query. EnsureDatabase pings at start, so a
// wrong setting still fails before the server accepts traffic.
func getPostgresDB() (*sql.DB, error) {

	dbMu.Lock()
	defer dbMu.Unlock()

	if closed {
		return nil, ErrDatabaseClosed
	}
	if postgresHandle != nil {
		return postgresHandle, nil
	}

	runtimeConfig := config.GetCDSRuntime().Config

	dbConfig, err := getDBConfig(runtimeConfig)
	if err != nil {
		return nil, err
	}

	// A connector, rather than sql.Open with a driver name. lib/pq does not
	// implement OpenConnector, so sql.Open wraps it in a connector whose
	// Connect discards the context. The pool would then ignore every deadline
	// while it opens a connection.
	//
	// boundedConnector covers what the pq connector still does not: the
	// startup handshake after the dial, which reads the connect_timeout of the
	// DSN and no context at all. With both, the whole attempt ends at whichever
	// comes first, the caller's deadline or the connect timeout.
	connector, err := pq.NewConnector(dbConfig.dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to read the datasource settings: %w", err)
	}
	// The dialer reports each socket to the attempt that opened it, so that an
	// attempt the caller gave up on can be ended rather than left running.
	connector.Dialer(attemptDialer{})

	settings := resolvePostgresPoolSettings(runtimeConfig.DataSource.Postgres)
	db := sql.OpenDB(newBoundedConnector(connector, settings.maxOpenConns))

	applyPostgresPoolSettings(db, runtimeConfig.DataSource.Postgres)

	// Verify before the handle is published. A pool that no caller can reach
	// must not become the handle every later call returns.
	timeout := resolveConnectTimeout(runtimeConfig.DataSource.Postgres)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("failed to reach the database within %s: %w (close error: %v)",
				timeout, err, closeErr)
		}
		return nil, fmt.Errorf("failed to reach the database within %s: %w", timeout, err)
	}

	postgresHandle = db
	return postgresHandle, nil
}

// resolveConnectTimeout returns the bound on one connection attempt.
func resolveConnectTimeout(cfg config.PostgresConfig) time.Duration {

	timeout := time.Duration(cfg.ConnectTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = database.DefaultPostgresConnectTimeout
	}
	return timeout
}

// postgresPoolSettings holds the resolved bounds of the PostgreSQL pool.
type postgresPoolSettings struct {
	maxOpenConns    int
	maxIdleConns    int
	connMaxLifetime time.Duration
	connMaxIdleTime time.Duration
}

// resolvePostgresPoolSettings applies a default to every value the operator
// left empty, and lowers an idle limit that is above the open limit.
//
// ValidateDataSource rejects a negative value and a contradictory pair before
// the server starts, so the corrections below are a safety net rather than the
// place a configuration mistake is handled. The function stays total, because
// a test may build a pool from any configuration.
func resolvePostgresPoolSettings(cfg config.PostgresConfig) postgresPoolSettings {

	settings := postgresPoolSettings{
		maxOpenConns:    cfg.MaxOpenConns,
		maxIdleConns:    cfg.MaxIdleConns,
		connMaxLifetime: time.Duration(cfg.ConnMaxLifetimeSeconds) * time.Second,
		connMaxIdleTime: time.Duration(cfg.ConnMaxIdleTimeSeconds) * time.Second,
	}

	if settings.maxOpenConns <= 0 {
		settings.maxOpenConns = database.DefaultPostgresMaxOpenConns
	}
	if settings.maxIdleConns <= 0 {
		settings.maxIdleConns = database.DefaultPostgresMaxIdleConns
	}
	// An idle limit above the open limit reserves connections the pool can
	// never hold, so lower it.
	if settings.maxIdleConns > settings.maxOpenConns {
		settings.maxIdleConns = settings.maxOpenConns
	}
	if settings.connMaxLifetime <= 0 {
		settings.connMaxLifetime = database.DefaultPostgresConnMaxLifetime
	}
	if settings.connMaxIdleTime <= 0 {
		settings.connMaxIdleTime = database.DefaultPostgresConnMaxIdleTime
	}

	return settings
}

// applyPostgresPoolSettings bounds the pool.
func applyPostgresPoolSettings(db *sql.DB, cfg config.PostgresConfig) {

	settings := resolvePostgresPoolSettings(cfg)

	db.SetMaxOpenConns(settings.maxOpenConns)
	db.SetMaxIdleConns(settings.maxIdleConns)
	db.SetConnMaxLifetime(settings.connMaxLifetime)
	db.SetConnMaxIdleTime(settings.connMaxIdleTime)
}

// CloseDB closes the pools the process holds. Call it at shutdown, after the
// HTTP server and the workers stop.
//
// It is safe to call more than once: the second call finds no handle and
// returns nil. After it runs, a request for a pool returns ErrDatabaseClosed
// rather than a new pool, so a worker that has not stopped yet cannot open
// connections that nothing will close.
func CloseDB() error {

	dbMu.Lock()
	postgres, sqlite := postgresHandle, sqliteHandle
	postgresHandle, sqliteHandle = nil, nil
	closed = true
	dbMu.Unlock()

	var firstErr error
	if postgres != nil {
		firstErr = postgres.Close()
	}
	if sqlite != nil {
		if err := sqlite.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// getSQLiteDB opens the inbuilt database once and initializes its schema. Like
// the PostgreSQL pool, the handle is published only after the database answers
// and the schema is applied, so a failed attempt leaves nothing behind.
func getSQLiteDB() (*sql.DB, error) {

	dbMu.Lock()
	defer dbMu.Unlock()

	if closed {
		return nil, ErrDatabaseClosed
	}
	if sqliteHandle != nil {
		return sqliteHandle, nil
	}

	runtimeConfig := config.GetCDSRuntime()

	dbConfig, err := getDBConfig(runtimeConfig.Config)
	if err != nil {
		return nil, err
	}

	if err := ensureSQLiteDir(runtimeConfig.Config.DataSource.SQLite.Path); err != nil {
		return nil, err
	}

	db, err := sql.Open(dbConfig.driverName, dbConfig.dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open the inbuilt database: %v", err)
	}

	maxOpenConns := runtimeConfig.Config.DataSource.SQLite.MaxOpenConns
	if maxOpenConns <= 0 {
		maxOpenConns = database.DefaultSQLiteMaxOpenConns
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxOpenConns)

	// The inbuilt database is a local file, so the open needs no deadline of
	// its own. The DSN carries busy_timeout, which bounds a wait for the lock.
	if err := db.Ping(); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("failed to ping the inbuilt database: %v (close error: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("failed to ping the inbuilt database: %v", err)
	}

	if err := initializeSQLiteSchema(db); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("%v (close error: %v)", err, closeErr)
		}
		return nil, err
	}

	sqliteHandle = db
	return sqliteHandle, nil
}

// getDBConfig returns the database configuration based on the provided data source.
func getDBConfig(dataSource config.Config) (DBConfig, error) {

	ds := dataSource.DataSource

	switch database.ResolveType(ds.Type) {
	case database.TypeSQLite:
		path, err := resolveSQLitePath(ds.SQLite.Path)
		if err != nil {
			return DBConfig{}, err
		}

		options := ds.SQLite.Options
		if options == "" {
			options = database.DefaultSQLiteOptions
		}
		if !strings.HasPrefix(options, "?") {
			options = "?" + options
		}

		return DBConfig{
			driverName: database.DriverSQLite,
			dsn:        path + options,
		}, nil

	default:
		// PostgreSQL. connect_timeout bounds the startup handshake that follows
		// the dial, which no context can reach.
		connectTimeout := int(resolveConnectTimeout(ds.Postgres).Seconds())
		return DBConfig{
			driverName: ds.Type,
			dsn: fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=%d",
				ds.Hostname, ds.Port, ds.Username, ds.Password, ds.Name, ds.SSLMode, connectTimeout),
		}, nil
	}
}

// resolveSQLitePath returns the absolute path of the inbuilt database file,
// resolving a relative path against CDS_HOME.
func resolveSQLitePath(path string) (string, error) {

	if path == "" {
		path = database.DefaultSQLitePath
	}
	if filepath.IsAbs(path) {
		return path, nil
	}
	return filepath.Join(config.GetCDSRuntime().CDSHome, path), nil
}

// GetDBType returns the configured datasource type.
func (d *DBProvider) GetDBType() string {

	if testDBOverride != nil {
		return database.ResolveType(testDBTypeOverride)
	}
	return database.ResolveType(config.GetCDSRuntime().Config.DataSource.Type)
}
