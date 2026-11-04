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
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

func TestMain(m *testing.M) {

	if err := log.Init("ERROR"); err != nil {
		panic(err)
	}
	config.OverrideCDSRuntime(config.Config{})
	os.Exit(m.Run())
}

// fakeBroker answers a STOMP CONNECT and nothing else unless it is told to
// answer the receipt of a DISCONNECT.
type fakeBroker struct {
	listener net.Listener

	mu        sync.Mutex
	accepted  []net.Conn
	readEnded chan struct{}
}

// startFakeBroker listens on a local port. When answerReceipt is false the
// broker never answers the receipt of a DISCONNECT, so go-stomp waits for it.
func startFakeBroker(t *testing.T, answerReceipt bool) *fakeBroker {

	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	broker := &fakeBroker{listener: listener, readEnded: make(chan struct{}, 4)}
	t.Cleanup(func() {
		_ = listener.Close()
		broker.mu.Lock()
		for _, conn := range broker.accepted {
			_ = conn.Close()
		}
		broker.mu.Unlock()
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			broker.mu.Lock()
			broker.accepted = append(broker.accepted, conn)
			broker.mu.Unlock()
			go broker.serve(conn, answerReceipt)
		}
	}()

	return broker
}

// connections reports how many connections the broker has accepted.
func (b *fakeBroker) connections() int {

	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.accepted)
}

// addr returns the address in the form the queue takes.
func (b *fakeBroker) addr() string {

	return "tcp://" + b.listener.Addr().String()
}

// serve answers the CONNECT and then reads until the client goes away.
func (b *fakeBroker) serve(conn net.Conn, answerReceipt bool) {

	defer func() { b.readEnded <- struct{}{} }()

	reader := bufio.NewReader(conn)
	for {
		frame, err := readFrame(reader)
		if err != nil {
			return
		}
		command, headers := parseFrame(frame)

		switch command {
		case "CONNECT", "STOMP":
			if _, err := conn.Write([]byte("CONNECTED\nversion:1.2\n\n\x00")); err != nil {
				return
			}
		case "DISCONNECT":
			if !answerReceipt {
				continue
			}
			if _, err := fmt.Fprintf(conn, "RECEIPT\nreceipt-id:%s\n\n\x00", headers["receipt"]); err != nil {
				return
			}
		}
	}
}

// readFrame reads one STOMP frame, which ends at a null byte.
func readFrame(reader *bufio.Reader) (string, error) {

	frame, err := reader.ReadString(0)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(frame, "\x00"), nil
}

// parseFrame returns the command and the headers of one frame.
func parseFrame(frame string) (string, map[string]string) {

	lines := strings.Split(strings.TrimLeft(frame, "\n"), "\n")
	headers := map[string]string{}
	for _, line := range lines[1:] {
		if line == "" {
			break
		}
		name, value, found := strings.Cut(line, ":")
		if found {
			headers[name] = value
		}
	}
	return lines[0], headers
}

// Test_Close_endsTheConnectionAtTheDeadline checks that a silent broker does
// not hold the process. go-stomp allows 30 seconds for a disconnect receipt,
// which is longer than the whole shutdown deadline.
func Test_Close_endsTheConnectionAtTheDeadline(t *testing.T) {

	broker := startFakeBroker(t, false)

	baseline := settledGoroutines()

	q, err := NewProfileQueue(broker.addr(), "", "", "queue/profiles", config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to connect to the fake broker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	closeErr := q.Close(ctx)
	elapsed := time.Since(start)

	if closeErr == nil {
		t.Fatal("expected the forced close to be reported")
	}
	if !errors.Is(closeErr, context.DeadlineExceeded) {
		t.Errorf("expected the deadline to be the reason, got %v", closeErr)
	}
	if elapsed > 5*time.Second {
		t.Errorf("the close took %s, so the receipt wait was not bounded", elapsed)
	}

	// The socket must be closed, not abandoned. The broker's read ends only
	// when it is.
	select {
	case <-broker.readEnded:
	case <-time.After(5 * time.Second):
		t.Error("the connection to the broker was left open")
	}

	if after := settledGoroutines(); after > baseline {
		t.Errorf("the close left %d goroutines behind", after-baseline)
	}
}

// Test_Close_returnsWhenTheBrokerAnswers is the control for the test above.
func Test_Close_returnsWhenTheBrokerAnswers(t *testing.T) {

	broker := startFakeBroker(t, true)

	baseline := settledGoroutines()

	q, err := NewSchemaSyncQueue(broker.addr(), "", "", "queue/schema", config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to connect to the fake broker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := q.Close(ctx); err != nil {
		t.Fatalf("expected a graceful close, got %v", err)
	}
	if after := settledGoroutines(); after > baseline {
		t.Errorf("the close left %d goroutines behind", after-baseline)
	}
}

// Test_Close_isSafeWhenCalledMoreThanOnce checks the queue contract. The later
// calls report the first result rather than a stale "already closed".
func Test_Close_isSafeWhenCalledMoreThanOnce(t *testing.T) {

	broker := startFakeBroker(t, true)

	q, err := NewProfileQueue(broker.addr(), "", "", "queue/profiles", config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to connect to the fake broker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for attempt := 1; attempt <= 3; attempt++ {
		if err := q.Close(ctx); err != nil {
			t.Fatalf("call %d to Close failed: %v", attempt, err)
		}
	}
}

// settledGoroutines returns the goroutine count once it stops falling, so that
// a goroutine which is on its way out is not counted as a leak.
func settledGoroutines() int {

	previous := runtime.NumGoroutine()
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		current := runtime.NumGoroutine()
		if current >= previous {
			return current
		}
		previous = current
	}
	return previous
}

// Test_Close_endsAReconnectThatIsWaiting covers the race between a reconnect
// that is waiting out its backoff and a close. The reconnect must not install a
// connection after the close has returned, because nothing would close it.
func Test_Close_endsAReconnectThatIsWaiting(t *testing.T) {

	broker := startFakeBroker(t, true)

	q, err := NewProfileQueue(broker.addr(), "", "", "queue/profiles", config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to connect to the fake broker: %v", err)
	}

	reconnected := make(chan error, 1)
	go func() { reconnected <- q.mc.reconnectWithBackoff("test", 1) }()

	// Let the reconnect reach its backoff before the close starts.
	time.Sleep(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	if err := q.Close(ctx); err != nil {
		t.Fatalf("expected a graceful close, got %v", err)
	}

	select {
	case err := <-reconnected:
		if err == nil {
			t.Error("the reconnect installed a connection after the close returned")
		}
		if elapsed := time.Since(start); elapsed >= initialBackoff {
			t.Errorf("the reconnect waited %s for its backoff instead of ending at the close", elapsed)
		}
	case <-time.After(initialBackoff + 5*time.Second):
		t.Fatal("the reconnect did not end")
	}

	if n := broker.connections(); n != 1 {
		t.Errorf("the broker accepted %d connections, want 1", n)
	}
}

// Test_dial_doesNotInstallAConnectionAfterTheClose covers the narrow window
// where the close happens while a dial is already in flight. The connection the
// dial opened is closed instead of installed.
func Test_dial_doesNotInstallAConnectionAfterTheClose(t *testing.T) {

	broker := startFakeBroker(t, true)

	q, err := NewProfileQueue(broker.addr(), "", "", "queue/profiles", config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to connect to the fake broker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := q.Close(ctx); err != nil {
		t.Fatalf("expected a graceful close, got %v", err)
	}

	installed := q.mc.getConn()

	if err := q.mc.dial(); err == nil {
		t.Error("the dial installed a connection after the close")
	}
	if q.mc.getConn() != installed {
		t.Error("the connection was replaced after the close")
	}

	// The socket the dial opened must be closed, not left open. The broker sees
	// two reads end: the one the close ended, and the one the dial opened.
	for i := 0; i < 2; i++ {
		select {
		case <-broker.readEnded:
		case <-time.After(5 * time.Second):
			t.Fatalf("connection %d to the broker was left open", i+1)
		}
	}
}
