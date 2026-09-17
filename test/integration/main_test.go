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

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"
	"github.com/wso2/identity-customer-data-service/test/integration/utils"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

// suiteDB is the datasource the suite is running against, for the tests that
// need the handle rather than a client over it.
var (
	suiteDB     *sql.DB
	suiteDBType string
	// suitePostgres is the running container, for the tests that build their
	// own pool from the runtime configuration rather than from suiteDB.
	suitePostgres *setup.TestPostgres
)

// TestMain runs the suite against the datasource named by CDS_TEST_DB:
// "postgres" (the default) or "sqlite".
func TestMain(m *testing.M) {
	ctx := context.Background()

	dbType := os.Getenv("CDS_TEST_DB")
	if dbType == "" {
		dbType = database.TypePostgres
	}

	conf := config.Config{
		Log: config.LogConfig{
			LogLevel: "DEBUG",
		},
		DataSource: config.DataSourceConfig{
			Type: dbType,
		},
	}
	config.OverrideCDSRuntime(conf)
	_ = log.Init("DEBUG")

	var db *sql.DB
	var teardown func()

	switch dbType {
	case database.TypeSQLite:
		sqliteDB, err := setup.SetupTestSQLite()
		if err != nil {
			fmt.Println("Failed to start test DB:", err)
			os.Exit(1)
		}
		db = sqliteDB.DB
		teardown = sqliteDB.Terminate

	case database.TypePostgres:
		pg, err := setup.SetupTestPostgres(ctx)
		if err != nil {
			fmt.Println("Failed to start test DB:", err)
			os.Exit(1)
		}
		db = pg.DB
		suitePostgres = pg
		teardown = func() {
			// Terminate container manually after tests complete
			_ = pg.Container.Terminate(ctx)

			// Remove the docker image used for tests
			cmd := exec.Command("docker", "rm", "-f", "cds-test-postgres")
			_, _ = cmd.CombinedOutput()
		}

		if err := utils.CreateTablesFromFile(pg.DB, utils.GetSchemaPath()); err != nil {
			fmt.Println("Failed to create tables from schema:", err)
			os.Exit(1)
		}

	default:
		fmt.Printf("Unsupported CDS_TEST_DB value: %s (want %q or %q)\n",
			dbType, database.TypePostgres, database.TypeSQLite)
		os.Exit(1)
	}

	provider.SetTestDB(db, dbType)
	suiteDB, suiteDBType = db, dbType

	if err := workers.StartProfileWorker(); err != nil { // Start the real enrichment queue worker
		fmt.Println("Failed to start profile worker:", err)
		os.Exit(1)
	}

	// Initialize Schema Sync worker
	if err := workers.StartSchemaSyncWorker(); err != nil {
		fmt.Println("Failed to start schema sync worker:", err)
		os.Exit(1)
	}

	// Run tests
	code := m.Run()

	teardown()

	os.Exit(code)
}
