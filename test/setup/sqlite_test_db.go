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

package setup

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wso2/identity-customer-data-service/dbscripts"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	_ "modernc.org/sqlite"
)

// SQLiteTestDB holds a temporary inbuilt database used by the test suites.
type SQLiteTestDB struct {
	DB   *sql.DB
	Path string

	cleanup func()
}

// Terminate closes the database and removes its files.
func (s *SQLiteTestDB) Terminate() {
	if s == nil {
		return
	}
	if s.DB != nil {
		_ = s.DB.Close()
	}
	if s.cleanup != nil {
		s.cleanup()
	}
}

// SetupTestSQLite creates a temporary inbuilt database with the schema
// applied. The database is file-backed so that every pooled connection sees it.
func SetupTestSQLite() (*SQLiteTestDB, error) {

	dir, err := os.MkdirTemp("", "cds-sqlite-test")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir for the test database: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	path := filepath.Join(dir, "cds-test.db")
	db, err := sql.Open(database.DriverSQLite, path+"?"+database.DefaultSQLiteOptions)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to open the test database: %w", err)
	}

	// Bound the pool the way the server does.
	db.SetMaxOpenConns(database.DefaultSQLiteMaxOpenConns)
	db.SetMaxIdleConns(database.DefaultSQLiteMaxOpenConns)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		cleanup()
		return nil, fmt.Errorf("failed to ping the test database: %w", err)
	}

	if _, err := db.Exec(dbscripts.SQLiteSchema); err != nil {
		_ = db.Close()
		cleanup()
		return nil, fmt.Errorf("failed to apply the test database schema: %w", err)
	}

	return &SQLiteTestDB{DB: db, Path: path, cleanup: cleanup}, nil
}
