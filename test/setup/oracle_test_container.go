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
	"regexp"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
)

// OracleContainerName is the name of the test container, so a teardown can
// remove it.
const OracleContainerName = "cds-test-oracle"

const (
	// defaultOracleImage is the Oracle Database the suite runs on, 23ai Free.
	// CDS_TEST_ORACLE_IMAGE replaces it, to run the suite on another release:
	// gvenzl/oracle-xe:21-slim for 21c. CDS_TEST_ORACLE_SERVICE then names the
	// pluggable database the image creates: FREEPDB1 on 23ai Free and XEPDB1
	// on 21c XE.
	defaultOracleImage   = "gvenzl/oracle-free:23-slim"
	defaultOracleService = "FREEPDB1"

	oracleSystemUser   = "system"
	oracleSystemSecret = "Cds_Test_Pass1"
	oracleTestUser     = "cdsuser"
	oracleTestPassword = "Cds_Test_Pass1"

	oracleReadyTimeout = 10 * time.Minute
	oracleProbe        = "SELECT 1 FROM dual"
)

// TestOracle is a running Oracle container and the settings needed to reach it.
type TestOracle struct {
	Container testcontainers.Container
	DB        *sql.DB
	Host      string
	Port      int
	Username  string
	Password  string
	// Database is the service name of the pluggable database.
	Database string
}

// SetupTestOracle starts an Oracle container, creates the application user the
// schema is applied as, and opens a pool on it with the settings the
// production provider uses.
func SetupTestOracle(ctx context.Context) (*TestOracle, error) {
	image, service := defaultOracleImage, defaultOracleService
	if override := os.Getenv("CDS_TEST_ORACLE_IMAGE"); override != "" {
		image = override
	}
	if override := os.Getenv("CDS_TEST_ORACLE_SERVICE"); override != "" {
		service = override
	}

	req := testcontainers.ContainerRequest{
		Image:        image,
		Name:         OracleContainerName,
		ExposedPorts: []string{"1521/tcp"},
		// The image names the administrator password differently, so both are set.
		Env: map[string]string{
			"ORACLE_PASSWORD": oracleSystemSecret,
			"ORACLE_PWD":      oracleSystemSecret,
		},
		WaitingFor: wait.ForAll(
			wait.ForLog("DATABASE IS READY TO USE!"),
			wait.ForListeningPort("1521/tcp"),
		).WithDeadline(oracleReadyTimeout),
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
	port, err := container.MappedPort(ctx, "1521")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}

	instance := &TestOracle{
		Container: container,
		Host:      host,
		Port:      int(port.Num()),
		Username:  oracleTestUser,
		Password:  oracleTestPassword,
		Database:  service,
	}

	if err := instance.createUser(ctx); err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}

	db, err := instance.open(instance.Username, instance.Password)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}
	instance.DB = db

	if err := waitForStatement(ctx, db, oracleProbe); err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}

	log.Printf("Oracle container started at %s:%d", host, instance.Port)

	return instance, nil
}

// open returns a pool on the pluggable database as a user, built from the
// settings the production provider uses.
func (o *TestOracle) open(username, password string) (*sql.DB, error) {
	dsn, err := provider.OracleDSN(config.DataSourceConfig{
		Type:     database.TypeOracle,
		Hostname: o.Host,
		Port:     o.Port,
		Name:     o.Database,
		Username: username,
		Password: password,
	}, 30*time.Second)
	if err != nil {
		return nil, err
	}
	return sql.Open(database.DriverOracle, dsn)
}

// createUser creates the user the schema is applied as, retrying while the
// pluggable database finishes opening. The user owns the tables, so it needs
// room in its tablespace.
func (o *TestOracle) createUser(ctx context.Context) error {
	admin, err := o.open(oracleSystemUser, oracleSystemSecret)
	if err != nil {
		return err
	}
	defer func() { _ = admin.Close() }()

	if err := waitForStatement(ctx, admin, oracleProbe); err != nil {
		return err
	}
	for _, statement := range []string{
		fmt.Sprintf("CREATE USER %s IDENTIFIED BY %s QUOTA UNLIMITED ON USERS", o.Username, o.Password),
		fmt.Sprintf("GRANT CONNECT, RESOURCE TO %s", o.Username),
	} {
		if _, err := admin.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("failed to create the test user: %w", err)
		}
	}
	return nil
}

// oracleBlockEnd matches the line that ends a block in a SQL*Plus script.
var oracleBlockEnd = regexp.MustCompile(`(?m)^/\s*$`)

// ApplySchemaFile runs a schema script block by block. A block ends at a line
// holding only a slash, as in SQL*Plus.
func (o *TestOracle) ApplySchemaFile(path string) error {
	script, err := os.ReadFile(path) //nolint:gosec // the suite chooses the schema path
	if err != nil {
		return fmt.Errorf("failed to read the schema file: %w", err)
	}
	for _, block := range oracleBlockEnd.Split(string(script), -1) {
		if strings.TrimSpace(block) == "" {
			continue
		}
		if _, err := o.DB.Exec(block); err != nil {
			return fmt.Errorf("failed to execute the schema: %w", err)
		}
	}
	return nil
}
