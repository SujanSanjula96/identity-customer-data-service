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
	"context"
	"database/sql"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/client"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
)

var connectorPing = model.DBQuery{ID: "CONNECTOR-01", Query: "SELECT 1"}

// silentServerClient builds a client over a server that accepts a connection
// and then answers nothing.
//
// The pool is built the way getPostgresDB builds it, but without the startup
// check, because that check would itself take the whole connect timeout.
func silentServerClient(t *testing.T, connectTimeoutSeconds int, queryTimeout time.Duration) (
	client.DBClientInterface, *sql.DB) {

	t.Helper()

	host, port := silentServer(t)
	runtimeConfig := unreachableDataSource(host, port, connectTimeoutSeconds)

	dbConfig, err := getDBConfig(runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if want := "connect_timeout=" + strconv.Itoa(connectTimeoutSeconds); !strings.Contains(dbConfig.dsn, want) {
		t.Fatalf("expected the DSN to carry %q, got %q", want, dbConfig.dsn)
	}

	connector, err := pq.NewConnector(dbConfig.dsn)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(boundedConnector{inner: connector})
	t.Cleanup(func() { _ = db.Close() })

	return client.NewSharedDBClient(db, database.TypePostgres, client.Timeouts{Query: queryTimeout}), db
}

// Test_ExecuteQueryContext_endsAtTheCallersDeadline is the regression test for
// the blocking review finding.
//
// The connect timeout is ten seconds and the caller allows one. The caller has
// to win. Before boundedConnector this call returned after the connect timeout,
// because lib/pq reads no context once the dial succeeds, so a readiness probe
// with a two second deadline stayed blocked long after Kubernetes gave up.
func Test_ExecuteQueryContext_endsAtTheCallersDeadline(t *testing.T) {

	dbClient, _ := silentServerClient(t, 10, 30*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	_, err := dbClient.ExecuteQueryContext(ctx, connectorPing)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a server that never answers")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected the caller's deadline to end the call, got %v", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("the call took %v, so the handshake ignored the caller's deadline of 1s", elapsed)
	}
}

// Test_ExecuteQueryContext_endsAtTheConnectTimeout is the other half of the
// bound. The caller allows thirty seconds and the connect timeout is two, so
// the connect timeout has to win.
//
// Together the two tests show that the whole handshake ends at whichever of
// the two comes first.
func Test_ExecuteQueryContext_endsAtTheConnectTimeout(t *testing.T) {

	dbClient, _ := silentServerClient(t, 2, 30*time.Second)

	start := time.Now()
	_, err := dbClient.ExecuteQueryContext(context.Background(), connectorPing)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a server that never answers")
	}
	if elapsed > 8*time.Second {
		t.Errorf("the call took %v, so the connect timeout of 2s did not end the handshake", elapsed)
	}
}

// Test_BeginTxContext_endsAtTheCallersDeadline covers the other entry point.
// A transaction opens a connection the same way.
func Test_BeginTxContext_endsAtTheCallersDeadline(t *testing.T) {

	dbClient, _ := silentServerClient(t, 10, 30*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	_, err := dbClient.BeginTxContext(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a server that never answers")
	}
	if elapsed > 3*time.Second {
		t.Errorf("the call took %v, so the handshake ignored the caller's deadline of 1s", elapsed)
	}
}

// Test_boundedConnector_leavesNoGoroutineBehind checks the cost of giving up.
//
// The attempt keeps running after the caller leaves, because only the connect
// timeout ends it. That is bounded, and nothing may outlive it.
func Test_boundedConnector_leavesNoGoroutineBehind(t *testing.T) {

	dbClient, _ := silentServerClient(t, 1, 30*time.Second)

	before := runtime.NumGoroutine()

	for attempt := 0; attempt < 5; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_, _ = dbClient.ExecuteQueryContext(ctx, connectorPing)
		cancel()
	}

	// Every abandoned attempt ends within its connect timeout of one second.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Errorf("goroutines grew from %d to %d, so abandoned attempts are left behind",
		before, runtime.NumGoroutine())
}

// Test_getDBConfig_keepsTheConnectTimeout guards the other half of the bound.
// boundedConnector is not a reason to drop connect_timeout: without it an
// attempt that the caller abandoned would have nothing to end it.
func Test_getDBConfig_keepsTheConnectTimeout(t *testing.T) {

	dbConfig, err := getDBConfig(postgresDataSource("postgres"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dbConfig.dsn, "connect_timeout=") {
		t.Errorf("expected the DSN to bound the handshake, got %q", dbConfig.dsn)
	}
}
