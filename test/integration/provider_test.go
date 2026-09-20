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

package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/client"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
)

// backendPID asks PostgreSQL which server process answered. Two queries that
// report the same value travelled the same connection.
var backendPID = model.DBQuery{ID: "PROVIDER-IT-01", Query: "SELECT pg_backend_pid() AS pid"}

// providerPoolLimit is the open limit this suite configures. It is small, so
// that the number of server processes the instance uses is easy to count.
const providerPoolLimit = 3

// useProductionProvider points the runtime at the test container and takes the
// test override away.
//
// The rest of the suite installs the container's own handle with SetTestDB,
// which bypasses everything this provider does: the DSN, the connector, the
// pool settings, the check before the handle is published, the reuse of one
// pool and the shutdown. This test drives that path instead.
func useProductionProvider(t *testing.T) {

	t.Helper()

	if suiteDBType != database.TypePostgres || suitePostgres == nil {
		t.Skip("the production provider path is PostgreSQL only")
	}

	original := config.GetCDSRuntime().Config

	runtimeConfig := original
	runtimeConfig.DataSource = config.DataSourceConfig{
		Type:     database.TypePostgres,
		Hostname: suitePostgres.Host,
		Port:     suitePostgres.Port,
		Username: suitePostgres.Username,
		Password: suitePostgres.Password,
		Name:     suitePostgres.Database,
		SSLMode:  "disable",
		Postgres: config.PostgresConfig{
			MaxOpenConns:          providerPoolLimit,
			MaxIdleConns:          providerPoolLimit,
			ConnectTimeoutSeconds: 10,
		},
	}
	config.OverrideCDSRuntime(runtimeConfig)
	provider.SetTestDB(nil, "")

	t.Cleanup(func() {
		_ = provider.CloseDB()
		config.OverrideCDSRuntime(original)
		provider.SetTestDB(suiteDB, suiteDBType)
	})
}

// queryBackendPID runs one statement and returns the server process that
// answered it.
func queryBackendPID(t *testing.T, dbClient client.DBClientInterface) string {

	t.Helper()

	rows, err := dbClient.ExecuteQueryContext(context.Background(), backendPID)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	return fmt.Sprint(rows[0]["pid"])
}

// Test_ProductionProvider drives the path the PR changes, end to end, against
// a real PostgreSQL server.
//
// The subtests share the pool the first one opens, so they run in order.
func Test_ProductionProvider(t *testing.T) {

	useProductionProvider(t)

	// The configuration has to pass the same check the server runs at start.
	if err := provider.ValidateDataSource(config.GetCDSRuntime().Config.DataSource); err != nil {
		t.Fatalf("expected the container configuration to be valid: %v", err)
	}

	t.Run("EnsureDatabase opens and verifies the pool", func(t *testing.T) {
		if err := provider.EnsureDatabase(); err != nil {
			t.Fatalf("expected the pool to open and answer: %v", err)
		}
	})

	t.Run("concurrent first access gets one pool", func(t *testing.T) {
		// Start from a process that holds nothing, so these really are first
		// calls.
		if err := provider.CloseDB(); err != nil {
			t.Fatal(err)
		}
		provider.ResetLifecycleForTest()

		const callers = 16

		var start sync.WaitGroup
		var done sync.WaitGroup
		start.Add(1)

		clients := make([]client.DBClientInterface, callers)
		failures := make([]error, callers)

		for i := 0; i < callers; i++ {
			done.Add(1)
			go func(index int) {
				defer done.Done()
				start.Wait()
				clients[index], failures[index] = provider.NewDBProvider().GetDBClient()
			}(i)
		}

		start.Done()
		done.Wait()

		for i := 0; i < callers; i++ {
			if failures[i] != nil {
				t.Fatalf("caller %d failed: %v", i, failures[i])
			}
			if clients[i] == nil {
				t.Fatalf("caller %d got no client", i)
			}
		}

		// Every client works, and together they use no more server processes
		// than the pool allows. A pool per client would show many more.
		pids := map[string]bool{}
		for _, dbClient := range clients {
			pids[queryBackendPID(t, dbClient)] = true
		}
		t.Logf("%d concurrent clients used %d server processes", callers, len(pids))
		if len(pids) > providerPoolLimit {
			t.Errorf("the clients used %d server processes, above the pool limit of %d: %v",
				len(pids), providerPoolLimit, pids)
		}
	})

	t.Run("later clients reuse the same pool", func(t *testing.T) {
		pids := map[string]bool{}
		for attempt := 0; attempt < 30; attempt++ {
			dbClient, err := provider.NewDBProvider().GetDBClient()
			if err != nil {
				t.Fatal(err)
			}
			pids[queryBackendPID(t, dbClient)] = true
			// Every store does this, and it must not close the pool.
			if err := dbClient.Close(); err != nil {
				t.Fatal(err)
			}
		}

		t.Logf("30 sequential clients used %d server processes", len(pids))
		if len(pids) > providerPoolLimit {
			t.Errorf("30 clients used %d server processes, so the pool is not shared: %v",
				len(pids), pids)
		}
	})

	t.Run("the pool holds no more connections than configured", func(t *testing.T) {
		dbClient, err := provider.NewDBProvider().GetDBClient()
		if err != nil {
			t.Fatal(err)
		}

		// Hold every connection at once, so the pool has to grow to its limit.
		const callers = providerPoolLimit * 4

		var done sync.WaitGroup
		pids := make([]string, callers)
		failures := make([]error, callers)

		for i := 0; i < callers; i++ {
			done.Add(1)
			go func(index int) {
				defer done.Done()
				rows, err := dbClient.ExecuteQueryContext(context.Background(),
					model.DBQuery{ID: "PROVIDER-IT-02", Query: "SELECT pg_sleep(0.2), pg_backend_pid() AS pid"})
				if err != nil {
					failures[index] = err
					return
				}
				if len(rows) != 1 {
					failures[index] = fmt.Errorf("got %d rows, want 1", len(rows))
					return
				}
				pids[index] = fmt.Sprint(rows[0]["pid"])
			}(i)
		}
		done.Wait()

		// Every caller must answer. A bounded pool makes a caller wait for a
		// free connection, and waiting is correct; failing is not. Ignoring a
		// failure here would let the test pass on a pool that served one query
		// and refused the other eleven.
		for index, err := range failures {
			if err != nil {
				t.Errorf("concurrent caller %d failed: %v", index, err)
			}
		}

		distinct := map[string]bool{}
		for index, pid := range pids {
			if pid == "" {
				t.Errorf("concurrent caller %d reported no backend process", index)
				continue
			}
			distinct[pid] = true
		}
		if t.Failed() {
			t.FailNow()
		}
		t.Logf("%d concurrent queries used %d server processes", callers, len(distinct))
		if len(distinct) > providerPoolLimit {
			t.Errorf("%d concurrent queries used %d server processes, above the configured limit of %d",
				callers, len(distinct), providerPoolLimit)
		}
	})

	t.Run("shutdown closes the pool", func(t *testing.T) {
		dbClient, err := provider.NewDBProvider().GetDBClient()
		if err != nil {
			t.Fatal(err)
		}

		if err := provider.CloseDB(); err != nil {
			t.Fatal(err)
		}

		// A client held from before shutdown can no longer reach the server.
		if _, err := dbClient.ExecuteQueryContext(context.Background(), backendPID); err == nil {
			t.Error("expected a client over a closed pool to fail")
		}
	})

	t.Run("no pool is opened after shutdown", func(t *testing.T) {
		dbClient, err := provider.NewDBProvider().GetDBClient()
		if !errors.Is(err, provider.ErrDatabaseClosed) {
			t.Fatalf("expected ErrDatabaseClosed, got %v", err)
		}
		if dbClient != nil {
			t.Error("expected no client after shutdown")
		}
	})
}
