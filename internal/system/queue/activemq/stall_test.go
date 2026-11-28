/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
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
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
)

// dispatchBroker is a fake STOMP broker with one queue. Like the blocking TCP
// transport of ActiveMQ, it dispatches a message from the reader goroutine of
// the connection whose frame made the message available. When a socket write
// blocks, that reader stops reading its own connection.
//
// It honors the ack mode and activemq.prefetchSize of a subscription. A
// message to an ack:auto subscription counts as acknowledged when it is
// written. A message to a client subscription stays in flight until the ACK,
// and goes back to the queue when its connection ends.
type dispatchBroker struct {
	listener net.Listener

	mu      sync.Mutex
	pending []string
	subs    []*brokerSub
	next    int
	seq     int
	conns   []*brokerConn
	// subscribeHeaders holds the headers of each SUBSCRIBE frame.
	subscribeHeaders []map[string]string
}

type brokerConn struct {
	net.Conn
	writeMu sync.Mutex
	dead    chan struct{}
	once    sync.Once
}

type brokerSub struct {
	conn     *brokerConn
	id       string
	auto     bool
	prefetch int
	inflight map[string]string
}

func startDispatchBroker(t *testing.T) *dispatchBroker {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	b := &dispatchBroker{listener: listener}
	t.Cleanup(func() {
		_ = listener.Close()
		b.mu.Lock()
		for _, c := range b.conns {
			c.kill()
		}
		b.mu.Unlock()
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if tcp, ok := conn.(*net.TCPConn); ok {
				// Small buffers fill fast, so the test does not need a large backlog.
				_ = tcp.SetReadBuffer(8 << 10)
				_ = tcp.SetWriteBuffer(8 << 10)
			}
			bc := &brokerConn{Conn: conn, dead: make(chan struct{})}
			b.mu.Lock()
			b.conns = append(b.conns, bc)
			b.mu.Unlock()
			go b.serve(bc)
		}
	}()
	return b
}

func (b *dispatchBroker) addr() string {
	return "tcp://" + b.listener.Addr().String()
}

func (c *brokerConn) kill() {
	c.once.Do(func() {
		close(c.dead)
		_ = c.Close()
	})
}

func (c *brokerConn) write(frame string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.Write([]byte(frame))
	return err
}

func (b *dispatchBroker) serve(conn *brokerConn) {
	defer b.drop(conn)

	reader := bufio.NewReader(conn)
	for {
		raw, err := readFrame(reader)
		if err != nil {
			return
		}
		command, headers := parseFrame(raw)
		_, body, _ := strings.Cut(raw, "\n\n")

		switch command {
		case "CONNECT", "STOMP":
			if conn.write("CONNECTED\nversion:1.2\n\n\x00") != nil {
				return
			}
		case "SUBSCRIBE":
			prefetch := 1000
			if value, err := strconv.Atoi(headers["activemq.prefetchSize"]); err == nil && value > 0 {
				prefetch = value
			}
			b.mu.Lock()
			b.subscribeHeaders = append(b.subscribeHeaders, headers)
			sub := &brokerSub{
				conn:     conn,
				id:       headers["id"],
				auto:     headers["ack"] == "" || headers["ack"] == "auto",
				prefetch: prefetch,
				inflight: map[string]string{},
			}
			b.subs = append(b.subs, sub)
			b.mu.Unlock()
			b.dispatch()
		case "SEND":
			b.mu.Lock()
			b.pending = append(b.pending, body)
			b.mu.Unlock()
			b.dispatch()
		case "ACK":
			b.mu.Lock()
			for _, sub := range b.subs {
				delete(sub.inflight, headers["id"])
			}
			b.mu.Unlock()
			b.dispatch()
		case "DISCONNECT", "ABORT", "COMMIT":
			if receipt := headers["receipt"]; receipt != "" {
				_ = conn.write(fmt.Sprintf("RECEIPT\nreceipt-id:%s\n\n\x00", receipt))
			}
		}
	}
}

// drop removes the subscriptions of a connection that ended, and puts their
// unacknowledged messages back on the queue.
func (b *dispatchBroker) drop(conn *brokerConn) {
	conn.kill()
	b.mu.Lock()
	kept := b.subs[:0]
	for _, sub := range b.subs {
		if sub.conn != conn {
			kept = append(kept, sub)
			continue
		}
		for _, body := range sub.inflight {
			b.pending = append(b.pending, body)
		}
	}
	b.subs = kept
	b.mu.Unlock()
	b.dispatch()
}

// dispatch writes pending messages to subscriptions that have room. The write
// happens on the calling goroutine and can block.
func (b *dispatchBroker) dispatch() {
	for {
		b.mu.Lock()
		var target *brokerSub
		if len(b.pending) > 0 {
			for i := range b.subs {
				sub := b.subs[(b.next+i)%len(b.subs)]
				if sub.auto || len(sub.inflight) < sub.prefetch {
					target = sub
					b.next = (b.next + i + 1) % len(b.subs)
					break
				}
			}
		}
		if target == nil {
			b.mu.Unlock()
			return
		}
		body := b.pending[0]
		b.pending = b.pending[1:]
		b.seq++
		messageID := fmt.Sprintf("msg-%d", b.seq)
		ackID := messageID
		if !target.auto {
			target.inflight[ackID] = body
		}
		b.mu.Unlock()

		frame := fmt.Sprintf("MESSAGE\nsubscription:%s\nmessage-id:%s\nack:%s\ndestination:/queue/test\n"+
			"content-type:application/json\ncontent-length:%d\n\n%s\x00",
			target.id, messageID, ackID, len(body), body)
		if err := target.conn.write(frame); err != nil {
			target.conn.kill()
			return
		}
	}
}

// subscriptions returns the headers of the SUBSCRIBE frames that the broker
// received.
func (b *dispatchBroker) subscriptions() []map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]map[string]string(nil), b.subscribeHeaders...)
}

// shortenTimeouts makes the timeouts of the package short for one test.
func shortenTimeouts(t *testing.T) {
	t.Helper()
	send, retireAfter := sendTimeout, retireTimeout
	// The send timeout stays long enough for a slow CI runner with the race
	// detector and coverage.
	sendTimeout, retireTimeout = 3*time.Second, 200*time.Millisecond
	t.Cleanup(func() {
		sendTimeout, retireTimeout = send, retireAfter
	})
}

// handledSet records the profiles that the handler saw.
type handledSet struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (h *handledSet) add(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.seen == nil {
		h.seen = map[string]bool{}
	}
	h.seen[id] = true
}

func (h *handledSet) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.seen)
}

// waitForCount waits until the handler saw want distinct profiles.
func waitForCount(t *testing.T, handled *handledSet, want int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	last, lastChange := -1, time.Now()
	for time.Now().Before(deadline) {
		got := handled.count()
		if got >= want {
			return
		}
		if got != last {
			last, lastChange = got, time.Now()
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the consumer handled %d of %d profiles in %s, and made no progress for the last %s",
		handled.count(), want, within, time.Since(lastChange).Round(time.Second))
}

// Test_consumerKeepsUpWithABacklog reproduces the stall from the performance
// run, where two CDS nodes shared one broker. The first handler call on one
// node takes longer than the send timeout, as a slow database call can.
//
// When the producer and the consumer of a node shared one STOMP connection with
// a large prefetch, the go-stomp I/O loop waited for the handler, the sends
// timed out, and the producers reconnected. The consumer stayed on the old connection, and the
// producers on the new connections built a backlog on the broker. The client
// then waited for the broker to read a SEND while the broker waited for the
// client to read a MESSAGE, and the consumer stopped for good.
func Test_consumerKeepsUpWithABacklog(t *testing.T) {
	shortenTimeouts(t)
	// Large messages through the fake broker are slow under the race detector
	// and coverage, so the sends of this test get more time.
	sendTimeout = 5 * time.Second
	broker := startDispatchBroker(t)

	const nodes, producers, perProducer, workers = 2, 4, 25, 5
	handlerPause := sendTimeout + time.Second
	padding := strings.Repeat("x", 256<<10)
	handled := &handledSet{}
	accepted := &handledSet{}
	failed := &handledSet{}

	queues := make([]*ProfileQueue, nodes)
	for n := range queues {
		q, err := NewProfileQueue(broker.addr(), "", "", "queue/profiles", config.TLSConfig{})
		if err != nil {
			t.Fatalf("failed to connect node %d to the fake broker: %v", n, err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = q.Close(ctx)
		})
		var pauseOnce sync.Once
		if err := q.Start(func(p profileModel.Profile) error {
			if n == 0 {
				pauseOnce.Do(func() { time.Sleep(handlerPause) })
			}
			time.Sleep(20 * time.Millisecond)
			handled.add(p.ProfileID)
			return nil
		}, workers); err != nil {
			t.Fatalf("failed to start the consumer of node %d: %v", n, err)
		}
		queues[n] = q
	}

	var wg sync.WaitGroup
	for n, q := range queues {
		for p := 0; p < producers; p++ {
			wg.Add(1)
			go func(q *ProfileQueue, n, p int) {
				defer wg.Done()
				for i := 0; i < perProducer; i++ {
					profile := profileModel.Profile{
						ProfileID: fmt.Sprintf("n%d-p%d-%d", n, p, i),
						Traits:    map[string]any{"padding": padding},
					}
					if err := q.Enqueue(profile); err != nil {
						t.Logf("enqueue of %s failed: %v", profile.ProfileID, err)
						failed.add(profile.ProfileID)
						continue
					}
					accepted.add(profile.ProfileID)
				}
			}(q, n, p)
		}
	}

	wg.Wait()
	// A failed send is a producer problem, and the consumer cannot receive
	// that message. Report it as such, and check the consumer only against the
	// messages that the broker accepted.
	if n := failed.count(); n > 0 {
		t.Errorf("%d of %d sends failed, so the producers did not keep up with the backlog",
			n, nodes*producers*perProducer)
	}
	waitForCount(t, handled, accepted.count(), handlerPause+30*time.Second)

	for _, headers := range broker.subscriptions() {
		if headers["ack"] != "client-individual" {
			t.Errorf("the consumer subscribed with ack %q, want client-individual", headers["ack"])
		}
		if headers[prefetchHeader] != strconv.Itoa(workers) {
			t.Errorf("the consumer subscribed with %s %q, want %d",
				prefetchHeader, headers[prefetchHeader], workers)
		}
	}
}
