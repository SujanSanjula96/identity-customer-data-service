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

package setup

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// postgresReadyTimeout bounds the wait for a server that answers a query.
const postgresReadyTimeout = 60 * time.Second

type TestPostgres struct {
	Container testcontainers.Container
	DB        *sql.DB
}

func SetupTestPostgres(ctx context.Context) (*TestPostgres, error) {
	req := testcontainers.ContainerRequest{
		Image:        "postgres:15-alpine",
		Name:         "cds-test-postgres",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "testuser",
			"POSTGRES_PASSWORD": "testpass",
			"POSTGRES_DB":       "testdb",
		},
		// The image starts the server twice: once for initdb and once for
		// real. The ready message therefore appears twice, and only the second
		// one means the server accepts connections.
		//
		// A wait on the port alone returns during the first start, and the
		// query that follows fails with "the database system is starting up".
		// That is what the mq-test job hit on 2026-09-17.
		WaitingFor: wait.ForAll(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
			wait.ForListeningPort("5432/tcp"),
		).WithDeadline(2 * time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	host, _ := container.Host(ctx)
	port, _ := container.MappedPort(ctx, "5432")

	dsn := fmt.Sprintf("host=%s port=%s user=testuser password=testpass dbname=testdb sslmode=disable", host, port.Port())
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}

	if err := waitForQuery(ctx, db); err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}

	log.Printf("Postgres container started at %s:%s", host, port.Port())

	return &TestPostgres{
		Container: container,
		DB:        db,
	}, nil
}

// waitForQuery runs a statement until the server answers it.
//
// The container wait already reports a server that accepts connections, so
// this normally succeeds on the first attempt. It is the last guard: the
// readiness of the test database is a successful query, not an open port.
func waitForQuery(ctx context.Context, db *sql.DB) error {

	deadline, cancel := context.WithTimeout(ctx, postgresReadyTimeout)
	defer cancel()

	var lastErr error
	for {
		_, err := db.ExecContext(deadline, "SELECT 1")
		if err == nil {
			return nil
		}
		lastErr = err

		select {
		case <-deadline.Done():
			return fmt.Errorf("the test database did not answer a query within %s: %w",
				postgresReadyTimeout, lastErr)
		case <-time.After(200 * time.Millisecond):
		}
	}
}
