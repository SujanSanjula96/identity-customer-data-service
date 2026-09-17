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
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// sqliteDataSource points the runtime at an inbuilt database in a temporary
// directory. The path is absolute, so nothing is written into the repository.
func sqliteDataSource(t *testing.T) config.Config {

	t.Helper()

	return config.Config{
		DataSource: config.DataSourceConfig{
			Type:   database.TypeSQLite,
			SQLite: config.SQLiteConfig{Path: filepath.Join(t.TempDir(), "cds.db")},
		},
	}
}

// Test_CloseDB_isSafeToRepeat pins the shutdown contract. The server calls
// CloseDB once, but a second call must not fail or panic.
func Test_CloseDB_isSafeToRepeat(t *testing.T) {

	config.OverrideCDSRuntime(sqliteDataSource(t))
	isolatePools(t)

	if _, err := getSQLiteDB(); err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= 3; attempt++ {
		if err := CloseDB(); err != nil {
			t.Fatalf("call %d to CloseDB failed: %v", attempt, err)
		}
	}
}

// Test_getPostgresDB_refusesAfterShutdown is the defect this test was written
// for. CloseDB clears the handle, so without a shutdown state the next call
// would open a whole new pool that nothing would ever close.
func Test_getPostgresDB_refusesAfterShutdown(t *testing.T) {

	config.OverrideCDSRuntime(postgresDataSource("postgres"))
	seedPostgresHandle(t)

	if err := CloseDB(); err != nil {
		t.Fatal(err)
	}

	db, err := getPostgresDB()
	if !errors.Is(err, ErrDatabaseClosed) {
		t.Fatalf("expected ErrDatabaseClosed, got %v", err)
	}
	if db != nil {
		t.Error("expected no pool after shutdown")
	}

	dbMu.Lock()
	published := postgresHandle
	dbMu.Unlock()

	if published != nil {
		t.Error("expected the handle to stay empty after shutdown")
	}
}

// Test_getSQLiteDB_refusesAfterShutdown checks the same rule for the inbuilt
// datasource.
func Test_getSQLiteDB_refusesAfterShutdown(t *testing.T) {

	config.OverrideCDSRuntime(sqliteDataSource(t))
	isolatePools(t)

	if _, err := getSQLiteDB(); err != nil {
		t.Fatal(err)
	}
	if err := CloseDB(); err != nil {
		t.Fatal(err)
	}

	if _, err := getSQLiteDB(); !errors.Is(err, ErrDatabaseClosed) {
		t.Fatalf("expected ErrDatabaseClosed, got %v", err)
	}
}

// Test_GetDBClient_refusesAfterShutdown checks that the rule reaches the
// caller a store uses. A worker that has not stopped yet asks for a client,
// not for a pool.
func Test_GetDBClient_refusesAfterShutdown(t *testing.T) {

	config.OverrideCDSRuntime(sqliteDataSource(t))
	isolatePools(t)

	if _, err := NewDBProvider().GetDBClient(); err != nil {
		t.Fatal(err)
	}
	if err := CloseDB(); err != nil {
		t.Fatal(err)
	}

	dbClient, err := NewDBProvider().GetDBClient()
	if !errors.Is(err, ErrDatabaseClosed) {
		t.Fatalf("expected ErrDatabaseClosed, got %v", err)
	}
	if dbClient != nil {
		t.Error("expected no client after shutdown")
	}
}

// Test_getSQLiteDB_neverReturnsAClosedHandle checks that a pool the process
// closed is not handed out again. The handle was cached behind a sync.Once
// before, which had no way to forget a closed pool.
func Test_getSQLiteDB_neverReturnsAClosedHandle(t *testing.T) {

	config.OverrideCDSRuntime(sqliteDataSource(t))
	isolatePools(t)

	first, err := getSQLiteDB()
	if err != nil {
		t.Fatal(err)
	}
	if err := CloseDB(); err != nil {
		t.Fatal(err)
	}

	// A test restarts the process where the server would not, so clear the
	// shutdown state as a restart does.
	dbMu.Lock()
	closed = false
	dbMu.Unlock()

	second, err := getSQLiteDB()
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("expected a fresh pool rather than the one that was closed")
	}
	if err := second.Ping(); err != nil {
		t.Errorf("expected the fresh pool to answer: %v", err)
	}
}

// Test_CloseDB_racesWithInitialization runs shutdown against callers that are
// still asking for a pool. Run it with -race.
//
// Every caller must get a pool it can use or ErrDatabaseClosed. No caller may
// get a pool that shutdown has already closed, and no pool may outlive
// CloseDB.
func Test_CloseDB_racesWithInitialization(t *testing.T) {

	config.OverrideCDSRuntime(sqliteDataSource(t))
	isolatePools(t)

	const callers = 16

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)

	results := make([]*sql.DB, callers)
	failures := make([]error, callers)

	for i := 0; i < callers; i++ {
		done.Add(1)
		go func(index int) {
			defer done.Done()
			start.Wait()
			results[index], failures[index] = getSQLiteDB()
		}(i)
	}

	done.Add(1)
	go func() {
		defer done.Done()
		start.Wait()
		if err := CloseDB(); err != nil {
			t.Error(err)
		}
	}()

	start.Done()
	done.Wait()

	for i := 0; i < callers; i++ {
		switch {
		case failures[i] == nil && results[i] == nil:
			t.Errorf("caller %d got neither a pool nor an error", i)
		case failures[i] != nil && !errors.Is(failures[i], ErrDatabaseClosed):
			t.Errorf("caller %d got an unexpected error: %v", i, failures[i])
		}
	}

	// Whatever the order was, shutdown leaves nothing behind.
	dbMu.Lock()
	published := sqliteHandle
	dbMu.Unlock()

	if published != nil {
		t.Error("expected no handle to outlive CloseDB")
	}
}
