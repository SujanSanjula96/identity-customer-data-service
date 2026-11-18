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
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
)

// MSSQLContainerName is the name of the test container, so a teardown can
// remove it.
const MSSQLContainerName = "cds-test-mssql"

const (
	// defaultMSSQLImage is the SQL Server the suite runs on. CDS_TEST_MSSQL_IMAGE
	// replaces it, for a machine that cannot run it, such as an arm64 host whose
	// container engine cannot emulate amd64 well enough for SQL Server.
	defaultMSSQLImage = "mcr.microsoft.com/mssql/server:2022-latest"

	mssqlTestUser     = "sa"
	mssqlTestPassword = "Cds_Test_Pass1!"
	mssqlTestDatabase = "testdb"
)

// TestMSSQL is a running SQL Server container and the settings needed to reach
// it.
type TestMSSQL struct {
	Container testcontainers.Container
	DB        *sql.DB
	Host      string
	Port      int
	Username  string
	Password  string
	Database  string
}

// SetupTestMSSQL starts a SQL Server container, creates the test database with
// the binary collation the schema expects, and opens a pool on it with the
// settings the production provider uses.
func SetupTestMSSQL(ctx context.Context) (*TestMSSQL, error) {
	image, platform := defaultMSSQLImage, "linux/amd64"
	if override := os.Getenv("CDS_TEST_MSSQL_IMAGE"); override != "" {
		// The platform of an image the caller chose is the caller's to set.
		image, platform = override, os.Getenv("CDS_TEST_MSSQL_PLATFORM")
	}

	req := testcontainers.ContainerRequest{
		Image: image,
		Name:  MSSQLContainerName,
		// The default image is built for amd64 only.
		ImagePlatform: platform,
		ExposedPorts:  []string{"1433/tcp"},
		Env: map[string]string{
			"ACCEPT_EULA":       "Y",
			"MSSQL_SA_PASSWORD": mssqlTestPassword,
		},
		WaitingFor: wait.ForAll(
			wait.ForLog("SQL Server is now ready for client connections"),
			wait.ForListeningPort("1433/tcp"),
		).WithDeadline(5 * time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}
	port, err := container.MappedPort(ctx, "1433")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}

	instance := &TestMSSQL{
		Container: container,
		Host:      host,
		Port:      int(port.Num()),
		Username:  mssqlTestUser,
		Password:  mssqlTestPassword,
		Database:  mssqlTestDatabase,
	}

	if err := instance.createDatabase(ctx); err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}

	db, err := instance.open(instance.Database)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}
	instance.DB = db

	if err := waitForQuery(ctx, db); err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}

	log.Printf("MSSQL container started at %s:%d", host, instance.Port)

	return instance, nil
}

// open returns a pool on a database of the container, built from the settings
// the production provider uses.
func (m *TestMSSQL) open(name string) (*sql.DB, error) {
	dsn, err := provider.MSSQLDSN(config.DataSourceConfig{
		Type:     database.TypeMSSQL,
		Hostname: m.Host,
		Port:     m.Port,
		Name:     name,
		Username: m.Username,
		Password: m.Password,
	}, 30*time.Second)
	if err != nil {
		return nil, err
	}
	return sql.Open(database.DriverMSSQL, dsn)
}

// createDatabase creates the test database with the collation the schema
// expects, retrying while the server finishes starting.
func (m *TestMSSQL) createDatabase(ctx context.Context) error {
	master, err := m.open("master")
	if err != nil {
		return err
	}
	defer func() { _ = master.Close() }()

	if err := waitForQuery(ctx, master); err != nil {
		return err
	}
	statement := fmt.Sprintf("CREATE DATABASE %s COLLATE Latin1_General_100_BIN2", m.Database)
	if _, err := master.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("failed to create the test database: %w", err)
	}
	return nil
}

// ApplySchemaFile runs a schema script as one batch.
func (m *TestMSSQL) ApplySchemaFile(path string) error {
	script, err := os.ReadFile(path) //nolint:gosec // the suite chooses the schema path
	if err != nil {
		return fmt.Errorf("failed to read the schema file: %w", err)
	}
	if _, err := m.DB.Exec(string(script)); err != nil {
		return fmt.Errorf("failed to execute the schema: %w", err)
	}
	return nil
}
