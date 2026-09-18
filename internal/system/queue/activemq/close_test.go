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

package activemq

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// silentBroker answers the STOMP handshake and then says nothing.
//
// It stands for a broker that still accepts writes but has stopped replying.
// A graceful disconnect waits for a receipt, so against this broker that wait
// never ends on its own.
func silentBroker(t *testing.T) string {

	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go func(conn net.Conn) {
				reader := bufio.NewReader(conn)

				// The client opens with a CONNECT frame, which ends at a null
				// byte.
				if _, err := reader.ReadString(0); err != nil {
					return
				}
				// Accept it, and say nothing ever again. No heart-beat header,
				// so neither side expects one.
				if _, err := conn.Write([]byte("CONNECTED\nversion:1.2\n\n\x00")); err != nil {
					return
				}

				// Read and drop whatever follows, the DISCONNECT included, so
				// the socket stays open and the client keeps waiting.
				buffer := make([]byte, 1024)
				for {
					if _, err := conn.Read(buffer); err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	return listener.Addr().String()
}

func TestMain(m *testing.M) {

	_ = log.Init("ERROR")
	m.Run()
}

// Test_Close_endsAtTheCallersDeadline is the case the review asked for.
//
// Shutdown allows the whole sequence 25 seconds. A graceful STOMP disconnect
// used to wait up to 30 for a receipt, so a broker that had stopped answering
// held the process open until Kubernetes killed it.
func Test_Close_endsAtTheCallersDeadline(t *testing.T) {

	queue, err := NewProfileQueue(silentBroker(t), "user", "pass",
		"/queue/close-test", config.TLSConfig{})
	if err != nil {
		t.Fatal(err)
	}

	const deadline = 500 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	start := time.Now()
	err = queue.Close(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected Close to report that it closed the connection by force")
	}
	// Twice the deadline leaves room for a slow machine, and still fails if
	// the receipt timeout of the library is what ended the wait.
	if elapsed > 2*deadline {
		t.Errorf("Close took %v, so the caller's deadline of %v did not end it", elapsed, deadline)
	}
}

// Test_Close_endsAtTheReceiptTimeout covers the caller that sets no deadline.
// The library's own wait has to be short enough for the shutdown budget, so
// that a caller without a deadline is still bounded.
func Test_Close_endsAtTheReceiptTimeout(t *testing.T) {

	queue, err := NewProfileQueue(silentBroker(t), "user", "pass",
		"/queue/close-test", config.TLSConfig{})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_ = queue.Close(context.Background())
	elapsed := time.Since(start)

	if elapsed > 3*disconnectReceiptTimeout {
		t.Errorf("Close took %v, well past the receipt timeout of %v",
			elapsed, disconnectReceiptTimeout)
	}
	// The default of the library is 30 seconds, which is past the whole
	// shutdown grace period.
	if elapsed > 10*time.Second {
		t.Errorf("Close took %v, so the default receipt timeout is still in use", elapsed)
	}
}
