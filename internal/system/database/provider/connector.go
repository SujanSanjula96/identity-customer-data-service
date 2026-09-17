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
)

// boundedConnector ends a connection attempt at the caller's deadline.
//
// lib/pq reads the context while it dials, but the startup handshake that
// follows, TLS and authentication, is bounded only by the connect_timeout of
// the DSN. A server that accepts the connection and then answers nothing
// therefore held a caller for the whole connect timeout, whatever deadline the
// caller had set. A readiness query with a two second deadline could wait ten
// seconds for a new connection, which is not what ExecuteQueryContext
// promises, and the Kubernetes probe had given up long before.
//
// The attempt runs in its own goroutine, so the wait for it ends at whichever
// comes first, the caller's deadline or the connect timeout. The effective
// bound on the whole handshake is therefore the smaller of the two.
//
// The goroutine always ends, because connect_timeout bounds the attempt it
// waits on. A connection that arrives after the caller left is closed rather
// than dropped, so an abandoned attempt leaks no socket.
type boundedConnector struct {
	inner driver.Connector
}

// connectResult is what one connection attempt produced.
type connectResult struct {
	conn driver.Conn
	err  error
}

// Connect opens a connection, and gives up when the context ends.
func (c boundedConnector) Connect(ctx context.Context) (driver.Conn, error) {

	// The channel holds one value, so the attempt never blocks on a send that
	// nobody waits for.
	done := make(chan connectResult, 1)

	go func() {
		conn, err := c.inner.Connect(ctx)
		done <- connectResult{conn: conn, err: err}
	}()

	select {
	case result := <-done:
		return result.conn, result.err

	case <-ctx.Done():
		go func() {
			if result := <-done; result.conn != nil {
				_ = result.conn.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

// Driver returns the driver of the connector this one wraps.
func (c boundedConnector) Driver() driver.Driver {

	return c.inner.Driver()
}
