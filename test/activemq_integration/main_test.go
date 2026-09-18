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

// Package activemqintegration contains integration tests that exercise the
// full profile-unification pipeline end-to-end using the ActiveMQ queue
// backend.  Each test binary that belongs to this package starts its own
// PostgreSQL and ActiveMQ testcontainers, configures the CDS workers to use
// ActiveMQ, and then runs the test scenarios.
package activemqintegration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	_ "github.com/wso2/identity-customer-data-service/internal/system/queue/activemq" // registers the ActiveMQ queue provider
	"github.com/wso2/identity-customer-data-service/internal/system/workers"
	integrationUtils "github.com/wso2/identity-customer-data-service/test/integration/utils"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	// ── Start PostgreSQL ──────────────────────────────────────────────────────
	pg, err := setup.SetupTestPostgres(ctx)
	if err != nil {
		fmt.Println("Failed to start test Postgres:", err)
		os.Exit(1)
	}

	// ── Start ActiveMQ ────────────────────────────────────────────────────────
	amq, err := setup.SetupTestActiveMQ(ctx)
	if err != nil {
		fmt.Println("Failed to start test ActiveMQ:", err)
		_ = pg.Container.Terminate(ctx)
		os.Exit(1)
	}

	// ── Runtime config: point workers at ActiveMQ ─────────────────────────────
	conf := config.Config{
		Log:        config.LogConfig{LogLevel: "DEBUG"},
		DataSource: config.DataSourceConfig{Type: "postgres"},
		MessageQueue: config.MessageQueueConfig{
			Type: "activemq",
			Broker: config.ExternalBrokerConfig{
				Addr:                amq.Addr,
				Username:            amq.Username,
				Password:            amq.Password,
				ProfileQueueName:    "/queue/cds-test-profile-unification",
				SchemaSyncQueueName: "/queue/cds-test-schema-sync",
			},
		},
	}
	config.OverrideCDSRuntime(conf)
	_ = log.Init("DEBUG")

	// ── Database setup ────────────────────────────────────────────────────────
	provider.SetTestDB(pg.DB, database.TypePostgres)
	if err := integrationUtils.CreateTablesFromFile(pg.DB, integrationUtils.GetSchemaPath()); err != nil {
		fmt.Println("Failed to create tables:", err)
		os.Exit(1)
	}

	// ── Start workers (ActiveMQ-backed) ───────────────────────────────────────
	if err := workers.StartProfileWorker(); err != nil {
		fmt.Println("Failed to start profile worker:", err)
		os.Exit(1)
	}
	if err := workers.StartSchemaSyncWorker(); err != nil {
		fmt.Println("Failed to start schema sync worker:", err)
		os.Exit(1)
	}

	// ── Run tests ─────────────────────────────────────────────────────────────
	code := m.Run()

	// ── Teardown ──────────────────────────────────────────────────────────────
	_ = workers.StopProfileWorker(ctx)
	_ = workers.StopSchemaSyncWorker(ctx)
	_ = pg.Container.Terminate(ctx)
	_ = amq.Container.Terminate(ctx)

	cmd := exec.Command("docker", "rm", "-f", "cds-test-postgres")
	_, _ = cmd.CombinedOutput()

	os.Exit(code)
}
