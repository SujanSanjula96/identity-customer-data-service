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

// The process holds one pool per datasource, opened on first use and kept open.
// Every store shares it, so a request reuses a connection instead of a new one.
var (
	dbMu           sync.Mutex
	sqliteHandle   *sql.DB
	postgresHandle *sql.DB
	// closed records that CloseDB ran.
	closed bool
)

// ErrDatabaseClosed is returned to a caller that asks for a pool after CloseDB
// ran. A pool opened at that point would leak for the rest of the process.
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

// GetDBClient returns a database client for the configured datasource.
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
// call.
//
// database/sql bounds the pool. The DSN connect_timeout bounds one connection
// attempt: a caller's context may not interrupt every stage of the lib/pq
// startup, TLS and authentication handshake, but the attempt ends at
// connect_timeout.
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

	connector, err := pq.NewConnector(dbConfig.dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to read the datasource settings: %w", err)
	}

	settings, err := resolvePostgresPoolSettings(runtimeConfig.DataSource.Postgres)
	if err != nil {
		return nil, err
	}

	db := sql.OpenDB(connector)
	applyPostgresPoolSettings(db, settings)

	// Verify before the handle is published, so a pool no caller can reach is
	// not cached.
	ctx, cancel := context.WithTimeout(context.Background(), settings.connectTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("failed to reach the database within %s: %w (close error: %v)",
				settings.connectTimeout, err, closeErr)
		}
		return nil, fmt.Errorf("failed to reach the database within %s: %w", settings.connectTimeout, err)
	}

	postgresHandle = db
	return postgresHandle, nil
}

// poolResolution turns configured numbers into the numbers a pool uses, and
// collects every setting CDS cannot use on the way.
type poolResolution struct {
	problems []string
}

// count returns the configured value, or def when the value is zero, which is
// what an omitted setting gives and how an operator asks for the default. A
// negative value is a mistake, so it is recorded instead of replaced.
func (r *poolResolution) count(key string, value, def int) int {

	if value < 0 {
		r.problems = append(r.problems, fmt.Sprintf("%s is %d, which is below zero", key, value))
		return def
	}
	if value == 0 {
		return def
	}
	return value
}

// seconds is count for a setting an operator gives in seconds.
func (r *poolResolution) seconds(key string, value int, def time.Duration) time.Duration {

	if value < 0 {
		r.problems = append(r.problems, fmt.Sprintf("%s is %d, which is below zero", key, value))
		return def
	}
	if value == 0 {
		return def
	}
	return time.Duration(value) * time.Second
}

// err returns one error that names every problem, so that an operator can fix
// a configuration in one pass rather than one mistake per restart.
func (r *poolResolution) err() error {

	if len(r.problems) == 0 {
		return nil
	}
	return fmt.Errorf("invalid datasource settings: %s", strings.Join(r.problems, "; "))
}

// resolveSQLiteMaxOpenConns returns the open limit of the inbuilt pool.
func resolveSQLiteMaxOpenConns(cfg config.SQLiteConfig) (int, error) {

	var resolution poolResolution
	maxOpenConns := resolution.count("datasource.sqlite.max_open_conns",
		cfg.MaxOpenConns, database.DefaultSQLiteMaxOpenConns)

	return maxOpenConns, resolution.err()
}

// postgresPoolSettings holds every PostgreSQL number the provider uses, with a
// default already applied to each one, so that one function decides them and
// the rest of the code reads them.
type postgresPoolSettings struct {
	maxOpenConns    int
	maxIdleConns    int
	connMaxLifetime time.Duration
	connMaxIdleTime time.Duration
	// connectTimeout bounds one connection attempt, so it also bounds how long
	// the pool may spend opening a connection.
	connectTimeout time.Duration
}

// resolvePostgresPoolSettings returns every number the PostgreSQL pool uses.
//
// It is the only place that reads these settings, so the rule cannot differ
// between the check at start and the pool itself. Zero takes the default, a
// negative value is refused, and so is an idle limit above the open limit.
func resolvePostgresPoolSettings(cfg config.PostgresConfig) (postgresPoolSettings, error) {

	var resolution poolResolution

	settings := postgresPoolSettings{
		maxOpenConns: resolution.count("datasource.postgres.max_open_conns",
			cfg.MaxOpenConns, database.DefaultPostgresMaxOpenConns),
		maxIdleConns: resolution.count("datasource.postgres.max_idle_conns",
			cfg.MaxIdleConns, database.DefaultPostgresMaxIdleConns),
		connMaxLifetime: resolution.seconds("datasource.postgres.conn_max_lifetime_seconds",
			cfg.ConnMaxLifetimeSeconds, database.DefaultPostgresConnMaxLifetime),
		connMaxIdleTime: resolution.seconds("datasource.postgres.conn_max_idle_time_seconds",
			cfg.ConnMaxIdleTimeSeconds, database.DefaultPostgresConnMaxIdleTime),
		connectTimeout: resolution.seconds("datasource.postgres.connect_timeout_seconds",
			cfg.ConnectTimeoutSeconds, database.DefaultPostgresConnectTimeout),
	}

	// An idle limit above the open limit reserves connections the pool can
	// never hold, so the two settings contradict each other. The comparison is
	// against the limit the pool really uses: an open limit of zero is the
	// default, not no limit.
	if cfg.MaxIdleConns > settings.maxOpenConns {
		openSource := "the default datasource.postgres.max_open_conns"
		if cfg.MaxOpenConns > 0 {
			openSource = "datasource.postgres.max_open_conns"
		}
		resolution.problems = append(resolution.problems, fmt.Sprintf(
			"datasource.postgres.max_idle_conns is %d, which is above %s of %d",
			cfg.MaxIdleConns, openSource, settings.maxOpenConns))
	}

	if err := resolution.err(); err != nil {
		return postgresPoolSettings{}, err
	}

	return settings, nil
}

func applyPostgresPoolSettings(db *sql.DB, settings postgresPoolSettings) {

	db.SetMaxOpenConns(settings.maxOpenConns)
	db.SetMaxIdleConns(settings.maxIdleConns)
	db.SetConnMaxLifetime(settings.connMaxLifetime)
	db.SetConnMaxIdleTime(settings.connMaxIdleTime)
}

// CloseDB closes the pools the process holds. Call it at shutdown, after the
// HTTP server and the workers stop.
//
// It is safe to call more than once. After it runs, a request for a pool
// returns ErrDatabaseClosed rather than a new pool.
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

// getSQLiteDB opens the inbuilt database once and initializes its schema. The
// handle is published only after the database answers and the schema is
// applied, so a failed attempt leaves nothing behind.
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

	// The settings are resolved before the open, so a refused setting leaves no
	// handle to close.
	maxOpenConns, err := resolveSQLiteMaxOpenConns(runtimeConfig.Config.DataSource.SQLite)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open(dbConfig.driverName, dbConfig.dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open the inbuilt database: %v", err)
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
		settings, err := resolvePostgresPoolSettings(ds.Postgres)
		if err != nil {
			return DBConfig{}, err
		}
		connectTimeout := int(settings.connectTimeout.Seconds())
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
