/*
 * Copyright (c) 2026, WSO2 LLC. (http://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
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
	"errors"
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
	// wedgeNextConsumer makes the next connection that subscribes stop
	// reading after the SUBSCRIBE, ignore the prefetch, and pad the ack header
	// of each MESSAGE. The client's ACK frames then fill the socket, which
	// wedges the I/O goroutine of the client.
	wedgeNextConsumer bool
	subscribes        int
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
	wedged   bool
	inflight map[string]string
}

const ackPadding = 256 << 10

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
			b.subscribes++
			b.subscribeHeaders = append(b.subscribeHeaders, headers)
			sub := &brokerSub{
				conn:     conn,
				id:       headers["id"],
				auto:     headers["ack"] == "" || headers["ack"] == "auto",
				prefetch: prefetch,
				wedged:   b.wedgeNextConsumer,
				inflight: map[string]string{},
			}
			b.wedgeNextConsumer = false
			b.subs = append(b.subs, sub)
			b.mu.Unlock()
			b.dispatch()
			if sub.wedged {
				// Stop reading until the client closes the connection.
				<-conn.dead
				return
			}
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
		case "DISCONNECT":
			_ = conn.write(fmt.Sprintf("RECEIPT\nreceipt-id:%s\n\n\x00", headers["receipt"]))
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
				if sub.auto || sub.wedged || len(sub.inflight) < sub.prefetch {
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
		if target.wedged {
			ackID += strings.Repeat("p", ackPadding)
		}
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

// queued reports how many messages wait on the broker.
func (b *dispatchBroker) queued() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}

// shortenTimeouts makes the timeouts of the package short for one test.
func shortenTimeouts(t *testing.T) {
	t.Helper()
	send, retireAfter, interval := sendTimeout, retireTimeout, stallCheckInterval
	sendTimeout, retireTimeout, stallCheckInterval = time.Second, 200*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() {
		sendTimeout, retireTimeout, stallCheckInterval = send, retireAfter, interval
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
// When the producer and the consumer of a node shared one STOMP connection, the
// go-stomp I/O loop waited for the handler, the sends timed out, and the
// producers reconnected. The consumer stayed on the old connection, and the
// producers on the new connections built a backlog on the broker. The client
// then waited for the broker to read a SEND while the broker waited for the
// client to read a MESSAGE, and the consumer stopped for good.
func Test_consumerKeepsUpWithABacklog(t *testing.T) {
	shortenTimeouts(t)
	broker := startDispatchBroker(t)

	const nodes, producers, perProducer = 2, 4, 25
	handlerPause := sendTimeout + time.Second
	padding := strings.Repeat("x", 256<<10)
	handled := &handledSet{}

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
		}); err != nil {
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
						t.Errorf("enqueue of %s failed: %v", profile.ProfileID, err)
					}
				}
			}(q, n, p)
		}
	}

	waitForCount(t, handled, nodes*producers*perProducer, handlerPause+30*time.Second)
	wg.Wait()

	for _, headers := range broker.subscriptions() {
		if headers["ack"] != "client-individual" {
			t.Errorf("the consumer subscribed with ack %q, want client-individual", headers["ack"])
		}
		if headers[prefetchHeader] != strconv.Itoa(consumerPrefetch) {
			t.Errorf("the consumer subscribed with %s %q, want %d",
				prefetchHeader, headers[prefetchHeader], consumerPrefetch)
		}
	}
}

// Test_consumerRecoversFromAStalledConnection wedges the I/O loop of the
// consumer connection: the broker stops reading it, and large ACK frames fill
// the socket. The consumer must notice that frames wait on the connection,
// replace the connection, and receive the messages again on a new one.
func Test_consumerRecoversFromAStalledConnection(t *testing.T) {
	shortenTimeouts(t)
	broker := startDispatchBroker(t)

	q, err := NewProfileQueue(broker.addr(), "", "", "queue/profiles", config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to connect to the fake broker: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = q.Close(ctx)
	})

	const messages = 60
	for i := 0; i < messages; i++ {
		if err := q.Enqueue(profileModel.Profile{ProfileID: fmt.Sprintf("p-%d", i)}); err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}
	waitFor(t, func() bool { return broker.queued() == messages }, 5*time.Second, "the broker to queue the messages")

	broker.mu.Lock()
	broker.wedgeNextConsumer = true
	broker.mu.Unlock()

	handled := &handledSet{}
	if err := q.Start(func(p profileModel.Profile) error {
		handled.add(p.ProfileID)
		return nil
	}); err != nil {
		t.Fatalf("failed to start the consumer: %v", err)
	}

	waitForCount(t, handled, messages, initialBackoff+15*time.Second)

	if subs := len(broker.subscriptions()); subs < 2 {
		t.Errorf("the consumer subscribed %d times, want a new subscription after the stall", subs)
	}
}

// waitFor waits until condition is true.
func waitFor(t *testing.T, condition func() bool, within time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", within, what)
}

// Test_messageIsNotAcknowledgedWhenTheWorkerStops checks that a message the
// handler refuses because its worker stops goes back to the broker. The
// consumer of another node then receives it.
func Test_messageIsNotAcknowledgedWhenTheWorkerStops(t *testing.T) {
	shortenTimeouts(t)
	broker := startDispatchBroker(t)

	stopping, err := NewProfileQueue(broker.addr(), "", "", "queue/profiles", config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to connect to the fake broker: %v", err)
	}
	refused := &handledSet{}
	if err := stopping.Start(func(p profileModel.Profile) error {
		refused.add(p.ProfileID)
		return errors.New("the worker is stopping")
	}); err != nil {
		t.Fatalf("failed to start the consumer: %v", err)
	}

	const messages = 5
	for i := 0; i < messages; i++ {
		if err := stopping.Enqueue(profileModel.Profile{ProfileID: fmt.Sprintf("p-%d", i)}); err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}
	waitForCount(t, refused, 1, 5*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := stopping.Close(ctx); err != nil {
		t.Fatalf("expected a graceful close, got %v", err)
	}
	if got := refused.count(); got != 1 {
		t.Errorf("the handler received %d messages after it refused one, want 1", got)
	}

	other, err := NewProfileQueue(broker.addr(), "", "", "queue/profiles", config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to connect the second node to the fake broker: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = other.Close(ctx)
	})
	handled := &handledSet{}
	if err := other.Start(func(p profileModel.Profile) error {
		handled.add(p.ProfileID)
		return nil
	}); err != nil {
		t.Fatalf("failed to start the second consumer: %v", err)
	}

	waitForCount(t, handled, messages, 5*time.Second)
}
