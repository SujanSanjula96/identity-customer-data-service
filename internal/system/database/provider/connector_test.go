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
	"database/sql/driver"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
func silentServerClient(t *testing.T, connectTimeoutSeconds int) (client.DBClientInterface, *sql.DB) {

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
	connector.Dialer(attemptDialer{})

	db := sql.OpenDB(newBoundedConnector(connector, database.DefaultPostgresMaxOpenConns))
	t.Cleanup(func() { _ = db.Close() })

	return client.NewSharedDBClient(db, database.TypePostgres), db
}

// Test_ExecuteQueryContext_endsAtTheCallersDeadline is the regression test for
// the blocking review finding.
//
// The connect timeout is ten seconds and the caller allows one. The caller has
// to win. Before boundedConnector this call returned after the connect timeout,
// because lib/pq reads no context once the dial succeeds, so a readiness probe
// with a two second deadline stayed blocked long after Kubernetes gave up.
func Test_ExecuteQueryContext_endsAtTheCallersDeadline(t *testing.T) {

	dbClient, _ := silentServerClient(t, 10)

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
// bound. The caller sets no deadline at all, so the connect timeout of two
// seconds is the only thing left to end the attempt.
//
// Together the two tests show that the handshake ends at whichever of the two
// comes first.
func Test_ExecuteQueryContext_endsAtTheConnectTimeout(t *testing.T) {

	dbClient, _ := silentServerClient(t, 2)

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

// Test_boundedConnector_leavesNoGoroutineBehind checks the cost of giving up.
//
// The attempt keeps running after the caller leaves, because only the connect
// timeout ends it. That is bounded, and nothing may outlive it.
func Test_boundedConnector_leavesNoGoroutineBehind(t *testing.T) {

	dbClient, _ := silentServerClient(t, 1)

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

// countingConnector stands in for the driver. It records how many connection
// attempts run at the same time, which is the number the pool limit has to
// bound.
type countingConnector struct {
	inFlight atomic.Int64
	peak     atomic.Int64
	// hold is how long one attempt takes. It stands for a server that accepts
	// a connection and then answers slowly.
	hold time.Duration
}

func (c *countingConnector) Connect(ctx context.Context) (driver.Conn, error) {

	running := c.inFlight.Add(1)
	defer c.inFlight.Add(-1)

	for {
		peak := c.peak.Load()
		if running <= peak || c.peak.CompareAndSwap(peak, running) {
			break
		}
	}

	// The driver does not read the context once the dial is done, which is the
	// whole reason boundedConnector exists.
	timer := time.NewTimer(c.hold)
	defer timer.Stop()
	<-timer.C

	return nil, errors.New("the server never finished the handshake")
}

func (c *countingConnector) Driver() driver.Driver { return nil }

// Test_boundedConnector_neverExceedsTheOpenLimit is the stress test the review
// asked for.
//
// Callers give up long before the handshake ends, so database/sql stops
// counting each attempt and opens another. Without a budget of its own the
// connector would then run generation after generation of handshakes at once,
// each holding a socket, and the process would pass max_open_conns however
// small that limit is.
func Test_boundedConnector_neverExceedsTheOpenLimit(t *testing.T) {

	const (
		limit   = 3
		callers = 60
	)

	inner := &countingConnector{hold: 400 * time.Millisecond}
	connector := newBoundedConnector(inner, limit)

	var done sync.WaitGroup
	for i := 0; i < callers; i++ {
		done.Add(1)
		go func() {
			defer done.Done()
			// Far shorter than the attempt takes, so every caller gives up.
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()

			conn, err := connector.Connect(ctx)
			if err == nil && conn != nil {
				_ = conn.Close()
			}
		}()
	}
	done.Wait()

	// The attempts that were abandoned are still running, so measure now
	// rather than after they drain.
	peak := inner.peak.Load()
	t.Logf("%d callers, each giving up early, reached %d simultaneous attempts", callers, peak)

	if peak > limit {
		t.Errorf("%d attempts ran at once, above the configured limit of %d", peak, limit)
	}
	if peak == 0 {
		t.Error("expected the callers to reach the driver at all")
	}
}

// Test_boundedConnector_freesItsSlotWhenTheCallerGivesUp is why the socket is
// closed rather than left to the connect timeout.
//
// A slot that is held for the whole connect timeout would starve every later
// caller. The attempt has to end when its caller does.
func Test_boundedConnector_freesItsSlotWhenTheCallerGivesUp(t *testing.T) {

	host, port := silentServer(t)
	// Ten seconds, so a slot that waits for the connect timeout is obvious.
	dbConfig, err := getDBConfig(unreachableDataSource(host, port, 10))
	if err != nil {
		t.Fatal(err)
	}

	inner, err := pq.NewConnector(dbConfig.dsn)
	if err != nil {
		t.Fatal(err)
	}
	inner.Dialer(attemptDialer{})

	connector := newBoundedConnector(inner, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if _, err := connector.Connect(ctx); err == nil {
		t.Fatal("expected the attempt to end at the caller's deadline")
	}

	// The attempt keeps its slot until the handshake really ends. That has to
	// happen now, not in ten seconds.
	start := time.Now()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(connector.slots) == 0 {
			t.Logf("the abandoned attempt gave its slot back after %v", time.Since(start))
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Error("the abandoned attempt still holds its slot, so it ran to the connect timeout")
}
