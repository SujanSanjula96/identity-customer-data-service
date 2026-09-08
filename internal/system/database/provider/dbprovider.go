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
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

// sqliteHandle is the single pooled handle for the inbuilt database, opened
// once and kept open. The client treats Close as a no-op, so the file's locks
// are held for the process rather than re-acquired on every query.
var (
	sqliteHandle *sql.DB
	sqliteOnce   sync.Once
	sqliteErr    error
)

// postgresHandle is the single pooled handle for PostgreSQL, opened on the
// first use and kept open for the life of the process. Every store shares it,
// so a request reuses a connection instead of a new TCP, TLS and
// authentication round trip. The client treats Close as a no-op, so a store
// that closes its client leaves the pool open.
//
// A mutex rather than a sync.Once guards it, so that a database which is
// unreachable at the first call does not cache its error for the life of the
// process. The next call tries again.
var (
	postgresMu     sync.Mutex
	postgresHandle *sql.DB
)

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

	postgresMu.Lock()
	defer postgresMu.Unlock()

	if postgresHandle != nil {
		return postgresHandle, nil
	}

	runtimeConfig := config.GetCDSRuntime().Config

	dbConfig, err := getDBConfig(runtimeConfig)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open(dbConfig.driverName, dbConfig.dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %v", err)
	}

	applyPostgresPoolSettings(db, runtimeConfig.DataSource.Postgres)

	postgresHandle = db
	return postgresHandle, nil
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

// CloseDB closes the pools the process holds. Call it once, at shutdown, after
// the HTTP server and the workers stop.
func CloseDB() error {

	postgresMu.Lock()
	postgres := postgresHandle
	postgresHandle = nil
	postgresMu.Unlock()

	var firstErr error
	if postgres != nil {
		firstErr = postgres.Close()
	}
	if sqliteHandle != nil {
		if err := sqliteHandle.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// getSQLiteDB opens the inbuilt database once and initializes its schema.
func getSQLiteDB() (*sql.DB, error) {

	sqliteOnce.Do(func() {
		runtimeConfig := config.GetCDSRuntime()

		dbConfig, err := getDBConfig(runtimeConfig.Config)
		if err != nil {
			sqliteErr = err
			return
		}

		if err := ensureSQLiteDir(runtimeConfig.Config.DataSource.SQLite.Path); err != nil {
			sqliteErr = err
			return
		}

		db, err := sql.Open(dbConfig.driverName, dbConfig.dsn)
		if err != nil {
			sqliteErr = fmt.Errorf("failed to open the inbuilt database: %v", err)
			return
		}

		maxOpenConns := runtimeConfig.Config.DataSource.SQLite.MaxOpenConns
		if maxOpenConns <= 0 {
			maxOpenConns = database.DefaultSQLiteMaxOpenConns
		}
		db.SetMaxOpenConns(maxOpenConns)
		db.SetMaxIdleConns(maxOpenConns)

		if err := db.Ping(); err != nil {
			_ = db.Close()
			sqliteErr = fmt.Errorf("failed to ping the inbuilt database: %v", err)
			return
		}

		if err := initializeSQLiteSchema(db); err != nil {
			_ = db.Close()
			sqliteErr = err
			return
		}

		sqliteHandle = db
	})

	return sqliteHandle, sqliteErr
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
		// PostgreSQL.
		return DBConfig{
			driverName: ds.Type,
			dsn: fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
				ds.Hostname, ds.Port, ds.Username, ds.Password, ds.Name, ds.SSLMode),
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
