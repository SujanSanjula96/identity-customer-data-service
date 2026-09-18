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

// Package database holds constants shared by the database provider, client and
// query layers.
package database

import (
	"strings"
	"time"
)

// Supported values of the `datasource.type` configuration.
const (
	// TypePostgres is the PostgreSQL datasource.
	TypePostgres = "postgres"
	// TypeSQLite is the inbuilt, file-backed datasource, and the default. It
	// needs no external database server and is intended for development, demos
	// and single-instance deployments.
	TypeSQLite = "sqlite"
)

// DefaultType is the datasource used when `datasource.type` is not configured,
// so that a server with no database settings starts on the inbuilt database.
const DefaultType = TypeSQLite

// SupportedTypes lists every value `datasource.type` accepts.
var SupportedTypes = []string{TypePostgres, TypeSQLite}

// ResolveType normalizes a configured `datasource.type`. An empty value means
// DefaultType. The returned value may still be unsupported, which
// IsSupportedType reports on.
func ResolveType(dbType string) string {

	normalized := strings.ToLower(strings.TrimSpace(dbType))
	if normalized == "" {
		return DefaultType
	}
	return normalized
}

// IsSupportedType reports whether dbType is a datasource CDS can run on.
func IsSupportedType(dbType string) bool {

	for _, supported := range SupportedTypes {
		if dbType == supported {
			return true
		}
	}
	return false
}

// Driver names registered with database/sql by the imported driver packages.
const (
	DriverPostgres = "postgres"
	DriverSQLite   = "sqlite"
)

// SQLite defaults, applied when the corresponding configuration values are
// left empty.
const (
	// DefaultSQLitePath is the inbuilt database location, resolved relative to
	// CDS_HOME when it is not an absolute path.
	DefaultSQLitePath = "repository/database/cds.db"

	// DefaultSQLiteOptions is the DSN query string appended to the database
	// file path. Every option is required:
	//   - foreign_keys(1)    enforces the schema's ON DELETE CASCADE.
	//   - journal_mode(WAL)  allows readers alongside a writer.
	//   - busy_timeout(5000) waits for the write lock instead of failing.
	//   - _txlock=immediate  takes the write lock when a transaction begins.
	//   - _time_format and _timezone store timestamps as sortable UTC text,
	//                        which keyset pagination orders on.
	//   - _texttotime=true   scans timestamp columns back into time.Time.
	DefaultSQLiteOptions = "_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" +
		"&_txlock=immediate&_time_format=sqlite&_timezone=UTC&_texttotime=true"

	// DefaultSQLiteMaxOpenConns bounds the connection pool. SQLite serialises
	// writers, so a small pool avoids lock contention.
	DefaultSQLiteMaxOpenConns = 4
)

// PostgreSQL connection pool defaults, applied when the corresponding
// configuration values are left empty. One instance holds one pool for the
// life of the process, so DefaultPostgresMaxOpenConns bounds the connections
// that instance takes from the server. The deployment-wide demand is
//
//	max_open_conns * maximum instance count + operational headroom
//	    <= the server's max_connections
//
// The maximum instance count includes the extra instance a rolling update
// starts before it stops an old one.
const (
	// DefaultPostgresMaxOpenConns bounds the connections one instance holds,
	// in use and idle together.
	DefaultPostgresMaxOpenConns = 25

	// DefaultPostgresMaxIdleConns is how many unused connections stay open.
	// It matches the open limit, so a burst does not close and reopen
	// connections.
	DefaultPostgresMaxIdleConns = 25

	// DefaultPostgresConnMaxLifetime retires a connection at this age, even a
	// healthy one, so that a failover or a DNS change takes effect.
	DefaultPostgresConnMaxLifetime = 30 * time.Minute

	// DefaultPostgresConnMaxIdleTime closes a connection that stays unused for
	// this long, so an idle instance releases what it does not need.
	DefaultPostgresConnMaxIdleTime = 5 * time.Minute

	// DefaultPostgresConnectTimeout bounds one connection attempt, from the TCP
	// dial to the end of the PostgreSQL startup handshake. It reaches the
	// driver as the connect_timeout parameter of the DSN, and it is also the
	// deadline of the check that runs when the pool opens.
	//
	// Three things are needed, because each covers a different failure.
	//
	// lib/pq does not implement OpenConnector, so database/sql wraps it in a
	// connector that discards the context. A call against a host that drops
	// packets then waits for the operating system. The pool is therefore built
	// from a pq.Connector, which reads the context while it dials.
	//
	// The pq connector still reads no context once the dial succeeds, so a
	// server that accepts and then answers nothing holds the caller for the
	// whole of this value. connect_timeout is what ends that, and it is in the
	// DSN for exactly that reason.
	//
	// A caller with a shorter deadline than this value must not wait for it.
	// provider.boundedConnector ends the attempt at whichever comes first.
	DefaultPostgresConnectTimeout = 10 * time.Second
)

// Timeout defaults, applied when the corresponding configuration values are
// left empty.
//
// A bounded pool makes a database call wait when every connection is in use.
// Go's short forms, db.Query and db.Begin, pass context.Background(), which has
// no deadline, so that wait would never end.
//
// These are maximums rather than fallbacks. The client applies one to every
// call, so a caller with no deadline gets it and a caller asking for longer is
// held to it. A caller with an earlier deadline keeps its own.
const (
	// DefaultQueryTimeout bounds one statement, from the wait for a free
	// connection to the last row.
	DefaultQueryTimeout = 30 * time.Second

	// DefaultTxTimeout bounds a whole transaction. A transaction holds its
	// connection until it ends, so an abandoned one would hold that connection
	// for the life of the process. At this age the driver rolls it back and
	// returns the connection to the pool.
	DefaultTxTimeout = 30 * time.Second

	// DefaultReadinessTimeout bounds the readiness probe's query. It is short,
	// because a probe that cannot answer quickly has already answered: the
	// instance is not ready.
	DefaultReadinessTimeout = 2 * time.Second
)
