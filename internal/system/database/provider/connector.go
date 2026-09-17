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
	"database/sql/driver"
	"net"
	"sync"
	"time"
)

// boundedConnector ends a connection attempt at the caller's deadline, and
// keeps the attempt inside the connection budget while it does.
//
// Two problems are solved here, and they pull in opposite directions.
//
// The first is the deadline. lib/pq reads the context while it dials, but the
// startup handshake that follows, TLS and authentication, is bounded only by
// the connect_timeout of the DSN. A server that accepts a connection and then
// answers nothing therefore held a caller for the whole connect timeout,
// whatever deadline that caller had set. A readiness probe that allows two
// seconds waited ten, so the handler was still running long after Kubernetes
// had given up.
//
// The second is the budget. database/sql counts an attempt against
// MaxOpenConns only until Connect returns. An attempt that is merely
// abandoned, with its socket still open, therefore frees a pool slot that the
// operating system has not freed, and the process can hold several generations
// of handshakes for every configured connection.
//
// So the attempt is both ended and counted:
//
//   - A slot is taken before the attempt starts and given back only when the
//     attempt really finishes, so the number of sockets in a handshake never
//     passes the configured open limit.
//   - The socket of the attempt is closed when the caller gives up, so the
//     handshake fails at once rather than at the connect timeout. The slot
//     therefore comes back in microseconds, not seconds, and the next caller
//     is not made to queue behind a connection nobody wants.
type boundedConnector struct {
	inner driver.Connector
	// slots holds one entry for every attempt that is running.
	slots chan struct{}
}

// newBoundedConnector bounds inner at the open limit of the pool it feeds.
func newBoundedConnector(inner driver.Connector, maxOpenConns int) *boundedConnector {

	if maxOpenConns <= 0 {
		maxOpenConns = 1
	}
	return &boundedConnector{
		inner: inner,
		slots: make(chan struct{}, maxOpenConns),
	}
}

// connectResult is what one connection attempt produced.
type connectResult struct {
	conn driver.Conn
	err  error
}

// Connect opens a connection, and gives up when the context ends.
func (c *boundedConnector) Connect(ctx context.Context) (driver.Conn, error) {

	// Take a slot first. A caller that cannot get one waits exactly as it
	// waits for a busy pool, and its own deadline ends that wait.
	select {
	case c.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	socket := &attemptSocket{}
	done := make(chan connectResult, 1)

	go func() {
		// The slot is held until the handshake really ends, whether or not a
		// caller is still waiting for it.
		defer func() { <-c.slots }()

		conn, err := c.inner.Connect(withAttemptSocket(ctx, socket))
		done <- connectResult{conn: conn, err: err}
	}()

	select {
	case result := <-done:
		// The connection belongs to the pool now, so stop watching its socket.
		socket.release()
		return result.conn, result.err

	case <-ctx.Done():
		// Close the socket, so the handshake fails now rather than at the
		// connect timeout.
		socket.abandon()

		go func() {
			if result := <-done; result.conn != nil {
				_ = result.conn.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

// Driver returns the driver of the connector this one wraps.
func (c *boundedConnector) Driver() driver.Driver {

	return c.inner.Driver()
}

// attemptSocketKey carries one attempt's socket down to the dialer.
type attemptSocketKey struct{}

// withAttemptSocket marks a context as belonging to one connection attempt.
func withAttemptSocket(ctx context.Context, socket *attemptSocket) context.Context {

	return context.WithValue(ctx, attemptSocketKey{}, socket)
}

// attemptSocket holds the network connection of one attempt, so that the
// attempt can be ended when its caller gives up.
//
// The socket belongs to this type from the moment the dial returns until the
// handshake ends. After that it belongs to the pool, and nothing here may
// touch it: a connection that is already serving queries must not be closed
// because the context that opened it has ended.
type attemptSocket struct {
	mu        sync.Mutex
	conn      net.Conn
	released  bool
	abandoned bool
}

// set records the socket the dialer opened.
func (s *attemptSocket) set(conn net.Conn) {

	s.mu.Lock()
	defer s.mu.Unlock()

	// The caller gave up while the dial was still running, so this socket is
	// already unwanted.
	if s.abandoned {
		_ = conn.Close()
		return
	}
	s.conn = conn
}

// abandon closes the socket, so that the reads of the handshake fail at once.
func (s *attemptSocket) abandon() {

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.released {
		return
	}
	s.abandoned = true
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
}

// release hands the socket to the pool.
func (s *attemptSocket) release() {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.released = true
	s.conn = nil
}

// attemptDialer opens the socket of a connection attempt and reports it to the
// attempt, so that the attempt can be ended.
//
// lib/pq derives its own context from the one it is given, for the connect
// timeout, but a value lookup walks the parents, so the attempt is still
// found.
type attemptDialer struct {
	dialer net.Dialer
}

// Dial opens a socket without a deadline. lib/pq uses it only when the DSN
// carries no connect_timeout, which the provider always sets.
func (d attemptDialer) Dial(network, address string) (net.Conn, error) {

	return d.dialer.Dial(network, address)
}

// DialTimeout opens a socket with a deadline.
func (d attemptDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return d.dialer.DialContext(ctx, network, address)
}

// DialContext opens a socket and hands it to the attempt that asked for it.
func (d attemptDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {

	conn, err := d.dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}

	if socket, ok := ctx.Value(attemptSocketKey{}).(*attemptSocket); ok {
		socket.set(conn)
	}
	return conn, nil
}
