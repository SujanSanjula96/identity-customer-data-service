/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
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

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
)

// MySQLContainerName is the name of the test container, so a teardown can
// remove it.
const MySQLContainerName = "cds-test-mysql"

// TestMySQL is a running MySQL container and the settings needed to reach it.
type TestMySQL struct {
	Container testcontainers.Container
	DB        *sql.DB
	Host      string
	Port      int
	Username  string
	Password  string
	Database  string
}

// SetupTestMySQL starts a MySQL container and opens a pool on it with the
// settings the production provider uses.
func SetupTestMySQL(ctx context.Context) (*TestMySQL, error) {
	req := testcontainers.ContainerRequest{
		Image:        "mysql:8.0",
		Name:         MySQLContainerName,
		ExposedPorts: []string{"3306/tcp"},
		Env: map[string]string{
			"MYSQL_ROOT_PASSWORD": "rootpass",
			"MYSQL_DATABASE":      "testdb",
			"MYSQL_USER":          "testuser",
			"MYSQL_PASSWORD":      "testpass",
		},
		// The image starts a temporary server to initialize the database and
		// then the real one. Only the real one listens on the port, and the
		// message below names it.
		WaitingFor: wait.ForAll(
			wait.ForLog("port: 3306  MySQL Community Server"),
			wait.ForListeningPort("3306/tcp"),
		).WithDeadline(3 * time.Minute),
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
	port, err := container.MappedPort(ctx, "3306")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}

	mysqlInstance := &TestMySQL{
		Container: container,
		Host:      host,
		Port:      int(port.Num()),
		Username:  "testuser",
		Password:  "testpass",
		Database:  "testdb",
	}

	dsn, err := provider.MySQLDSN(config.DataSourceConfig{
		Type:     database.TypeMySQL,
		Hostname: mysqlInstance.Host,
		Port:     mysqlInstance.Port,
		Name:     mysqlInstance.Database,
		Username: mysqlInstance.Username,
		Password: mysqlInstance.Password,
	}, 30*time.Second)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}
	db, err := sql.Open(database.DriverMySQL, dsn)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}
	mysqlInstance.DB = db

	if err := waitForQuery(ctx, db); err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}

	log.Printf("MySQL container started at %s:%d", host, mysqlInstance.Port)

	return mysqlInstance, nil
}

// ApplySchemaFile runs every statement of a schema script. It uses its own
// connection, because the script holds many statements and the driver runs
// several in one call only when asked to, which the test pool should not.
func (m *TestMySQL) ApplySchemaFile(path string) error {
	script, err := os.ReadFile(path) //nolint:gosec // the suite chooses the schema path
	if err != nil {
		return fmt.Errorf("failed to read the schema file: %w", err)
	}

	driverConfig := mysqldriver.NewConfig()
	driverConfig.User = m.Username
	driverConfig.Passwd = m.Password
	driverConfig.Net = "tcp"
	driverConfig.Addr = fmt.Sprintf("%s:%d", m.Host, m.Port)
	driverConfig.DBName = m.Database
	driverConfig.MultiStatements = true

	db, err := sql.Open(database.DriverMySQL, driverConfig.FormatDSN())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(string(script)); err != nil {
		return fmt.Errorf("failed to execute the schema: %w", err)
	}
	return nil
}
