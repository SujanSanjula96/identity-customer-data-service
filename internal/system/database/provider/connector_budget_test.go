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
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// socketCounter counts the sockets one driver holds, and remembers the most it
// ever held at once.
//
// A socket exists from the moment an attempt starts until the connection it
// produced is closed, so an established connection and a handshake count the
// same. That total is what max_open_conns has to bound.
type socketCounter struct {
	live atomic.Int64
	peak atomic.Int64
}

func (s *socketCounter) open() {

	running := s.live.Add(1)
	for {
		peak := s.peak.Load()
		if running <= peak || s.peak.CompareAndSwap(peak, running) {
			break
		}
	}
}

func (s *socketCounter) closeOne() { s.live.Add(-1) }

// stallingConnector answers the first few attempts and then stalls.
//
// The stall ignores the context, exactly as the reads of a PostgreSQL startup
// handshake do. That is what leaves an attempt running after its caller has
// given up.
type stallingConnector struct {
	sockets  *socketCounter
	answered atomic.Int64
	// answerFirst is how many attempts succeed before the rest stall.
	answerFirst int64
	// stall is how long an attempt runs after its caller has gone.
	stall time.Duration
}

func (c *stallingConnector) Connect(context.Context) (driver.Conn, error) {

	c.sockets.open()

	if c.answered.Add(1) <= c.answerFirst {
		return &stubConn{sockets: c.sockets}, nil
	}

	timer := time.NewTimer(c.stall)
	defer timer.Stop()
	<-timer.C

	c.sockets.closeOne()
	return nil, errors.New("the server never finished the handshake")
}

func (c *stallingConnector) Driver() driver.Driver { return nil }

// stubConn is the smallest connection database/sql can use: it answers a
// query and starts a transaction, and it reports when it is closed.
type stubConn struct {
	sockets *socketCounter
	once    sync.Once
}

func (c *stubConn) Prepare(string) (driver.Stmt, error) {

	return nil, errors.New("the stub connection prepares nothing")
}

func (c *stubConn) Close() error {

	c.once.Do(c.sockets.closeOne)
	return nil
}

func (c *stubConn) Begin() (driver.Tx, error) { return stubTx{}, nil }

func (c *stubConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {

	return &stubRows{}, nil
}

type stubTx struct{}

func (stubTx) Commit() error   { return nil }
func (stubTx) Rollback() error { return nil }

type stubRows struct{}

func (*stubRows) Columns() []string         { return []string{"one"} }
func (*stubRows) Close() error              { return nil }
func (*stubRows) Next([]driver.Value) error { return io.EOF }

// Test_boundedConnector_countsEstablishedConnectionsToo is the invariant the
// review asked for:
//
//	established connections + attempts in flight <= max_open_conns
//
// The earlier stress test drove the connector directly, so there were no
// established connections and it could not see this. Here the pool holds
// connections open while later callers give up, which is the state that used
// to pass the limit: database/sql stops counting an abandoned attempt, and the
// connector used to stop counting a connection once its handshake was done.
func Test_boundedConnector_countsEstablishedConnectionsToo(t *testing.T) {

	const (
		limit = 5
		held  = 3
	)

	sockets := &socketCounter{}
	inner := &stallingConnector{
		sockets:     sockets,
		answerFirst: held,
		stall:       300 * time.Millisecond,
	}

	db := sql.OpenDB(newBoundedConnector(inner, limit))
	db.SetMaxOpenConns(limit)
	t.Cleanup(func() { _ = db.Close() })

	// Hold connections open, as a busy instance does.
	for i := 0; i < held; i++ {
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatalf("expected the first %d connections to be established: %v", held, err)
		}
		defer func() { _ = conn.Close() }()
	}

	if got := sockets.live.Load(); got != held {
		t.Fatalf("expected %d established connections, got %d", held, got)
	}

	// Now flood the pool with callers that give up long before a handshake
	// could finish. Every one of them makes database/sql ask for another
	// connection.
	var done sync.WaitGroup
	for i := 0; i < 60; i++ {
		done.Add(1)
		go func() {
			defer done.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()

			rows, err := db.QueryContext(ctx, "SELECT 1")
			if err == nil {
				_ = rows.Close()
			}
		}()
	}
	done.Wait()

	// Measure while the abandoned attempts are still running.
	peak := sockets.peak.Load()
	t.Logf("%d connections held open, plus callers that gave up, reached %d sockets at once",
		held, peak)

	if peak > limit {
		t.Errorf("the driver held %d sockets at once, above max_open_conns of %d", peak, limit)
	}
	if peak <= held {
		t.Errorf("expected the flood to open more sockets than the %d held, got %d", held, peak)
	}
}
