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

// Package activemq provides the queues backed by an ActiveMQ broker over STOMP. It registers itself as
// the activemq provider of the queue package.
package activemq

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-stomp/stomp/v3"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/queue"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

const (
	contentTypeJSON   = "application/json"
	initialBackoff    = 2 * time.Second
	maxBackoff        = 60 * time.Second
	backoffMultiplier = 2

	// consumerPrefetch is the number of messages that the broker sends to a
	// consumer before the consumer acknowledges them. A small value keeps the
	// broker from filling the socket of a slow consumer, and leaves the rest of a
	// backlog on the broker for the consumers on other nodes.
	consumerPrefetch = 10
	prefetchHeader   = "activemq.prefetchSize"
)

// The tests shorten these values.
var (
	// sendTimeout is the time that a send waits for the I/O loop of go-stomp to
	// take the frame.
	sendTimeout = stomp.DefaultMsgSendTimeout
	// retireTimeout is the time that a replaced connection gets for a graceful
	// disconnect before its socket is closed by force.
	retireTimeout = 5 * time.Second
	// stallCheckInterval is the time without a message after which a consumer
	// probes its connection.
	stallCheckInterval = 15 * time.Second
	// probeTimeout is the time that the broker gets to answer a probe before
	// the consumer replaces the connection.
	probeTimeout = 15 * time.Second
)

// errProbeTimeout says the broker did not answer a probe in time.
var errProbeTimeout = errors.New("activemq: the broker did not answer the probe in time")

func init() {
	queue.RegisterProfileQueueProvider(queue.TypeActiveMQ,
		func(cfg config.ExternalBrokerConfig, tlsCfg config.TLSConfig) (queue.ProfileUnificationQueue, error) {
			return NewProfileQueue(cfg.Addr, cfg.Username, cfg.Password, cfg.ProfileQueueName, tlsCfg)
		},
	)
	queue.RegisterSchemaSyncQueueProvider(queue.TypeActiveMQ,
		func(cfg config.ExternalBrokerConfig, tlsCfg config.TLSConfig) (queue.SchemaSyncQueue, error) {
			return NewSchemaSyncQueue(cfg.Addr, cfg.Username, cfg.Password, cfg.SchemaSyncQueueName, tlsCfg)
		},
	)
}

// managedConn holds a STOMP connection and re-dials transparently when the
// connection is lost. The conn field is protected by mu to prevent data races
// between concurrent Enqueue calls, consumer goroutines, and reconnect
// attempts. The generation is incremented every time a new connection is
// installed so consumers can detect whether a closed subscription belongs to
// an intentionally retired connection or to the current live connection.
// The done channel is used to signal consumer goroutines to exit during
// graceful shutdown. The netConn field holds the socket of the current
// connection, so that a close which does not finish inside its deadline can be
// ended by force.
type managedConn struct {
	addr     string
	username string
	password string
	tlsCfg   config.TLSConfig

	mu          sync.RWMutex
	conn        *stomp.Conn
	netConn     net.Conn
	generation  uint64
	reconnectMu sync.Mutex
	done        chan struct{}
	doneOnce    sync.Once
	// closed guards the install in dial against a concurrent shutdown. It is
	// read and written under mu, which is also the lock that installs a
	// connection, so a connection is never installed after shutdown.
	closed bool

	// closeOnce disconnects once and keeps the result, so a repeated Close
	// returns the same answer.
	closeOnce sync.Once
	closeErr  error
}

func newManagedConn(addr, username, password string, tlsCfg config.TLSConfig) (*managedConn, error) {
	mc := &managedConn{
		addr:     addr,
		username: username,
		password: password,
		tlsCfg:   tlsCfg,
		done:     make(chan struct{}),
	}
	if err := mc.dial(); err != nil {
		return nil, err
	}
	return mc, nil
}

// parseAddr strips a transport scheme from addr and returns the bare
// "host:port" and whether TLS should be used.
//
// Supported schemes:
//   - "ssl://"  → TLS (ActiveMQ SSL transport)
//   - "tcp://"  → plain TCP
//   - no scheme → plain TCP (bare "host:port")
func parseAddr(addr string) (hostPort string, useTLS bool) {
	switch {
	case strings.HasPrefix(addr, "ssl://"):
		return strings.TrimPrefix(addr, "ssl://"), true
	case strings.HasPrefix(addr, "tcp://"):
		return strings.TrimPrefix(addr, "tcp://"), false
	}
	return addr, false
}

func (mc *managedConn) dial() error {
	hostPort, useTLS := parseAddr(mc.addr)

	opts := []func(*stomp.Conn) error{
		stomp.ConnOpt.Login(mc.username, mc.password),
		// Disable STOMP-level heartbeats to avoid spurious read-timeout
		// disconnects when the broker sends heartbeats less frequently than
		// the negotiated interval. TCP keepalive (set below) provides
		// equivalent liveness detection for half-open connections.
		stomp.ConnOpt.HeartBeat(0, 0),
		stomp.ConnOpt.MsgSendTimeout(sendTimeout),
	}

	// Use a dialer with an explicit connect timeout and TCP keepalive.
	// The timeout prevents indefinite blocking during reconnects. Keepalive
	// lets the OS probe idle connections and surface half-open or
	// load-balancer-dropped sockets without relying on STOMP heartbeats.
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	var netConn net.Conn
	var err error
	if useTLS {
		// Build CA pool from system roots, then append the trust store if
		// configured — same pattern as identity_client.go. This allows the
		// broker's internal CA cert to be added to the existing trust_store
		// without any new config fields.
		rootCAs, sysErr := x509.SystemCertPool()
		if sysErr != nil || rootCAs == nil {
			rootCAs = x509.NewCertPool()
		}
		if mc.tlsCfg.TrustStore != "" {
			certDir := mc.tlsCfg.CertDir
			if certDir == "" {
				certDir = filepath.Join(utils.GetCDSHome(), "etc", "certs")
			}
			if !filepath.IsAbs(certDir) {
				if abs, absErr := filepath.Abs(certDir); absErr == nil {
					certDir = abs
				}
			}
			trustPEM, readErr := os.ReadFile(filepath.Clean(filepath.Join(certDir, mc.tlsCfg.TrustStore)))
			if readErr != nil {
				return fmt.Errorf("activemq: failed to read trust_store: %w", readErr)
			}
			if ok := rootCAs.AppendCertsFromPEM(trustPEM); !ok {
				return fmt.Errorf("activemq: failed to append certs from trust_store")
			}
		}
		netConn, err = tls.DialWithDialer(dialer, "tcp", hostPort, &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    rootCAs,
		})
	} else {
		netConn, err = dialer.Dial("tcp", hostPort)
	}
	if err != nil {
		return fmt.Errorf("activemq: dial %s: %w", mc.addr, err)
	}

	stompConn, err := stomp.Connect(netConn, opts...)
	if err != nil {
		_ = netConn.Close() // prevent fd leak if STOMP handshake fails
		return fmt.Errorf("activemq: dial %s: %w", mc.addr, err)
	}

	mc.mu.Lock()
	if mc.closed {
		mc.mu.Unlock()
		_ = netConn.Close()
		return fmt.Errorf("activemq: the connection to %s was not installed because the queue is closed", mc.addr)
	}
	oldConn, oldNetConn := mc.conn, mc.netConn
	mc.conn = stompConn
	mc.netConn = netConn
	mc.generation++
	mc.mu.Unlock()

	if oldConn != nil {
		go retire(oldConn, oldNetConn, retireTimeout)
	}
	return nil
}

// retire ends a connection that a reconnect replaced. It asks for a graceful
// disconnect, but closes the socket by force when the disconnect does not
// finish inside timeout. The I/O loop of a stalled connection never
// answers, and the socket must close so that the broker drops the consumer of
// the connection and gives its unacknowledged messages to another consumer.
func retire(conn *stomp.Conn, netConn net.Conn, timeout time.Duration) {
	disconnected := make(chan struct{})
	go func() {
		_ = conn.Disconnect()
		close(disconnected)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-disconnected:
	case <-timer.C:
	}

	if netConn != nil {
		_ = netConn.Close()
	}
}

// getConn returns the current connection under a read lock.
func (mc *managedConn) getConn() *stomp.Conn {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	return mc.conn
}

// getConnAndGeneration returns the current connection and its generation under
// a read lock. Consumers use this to detect whether a closed subscription came
// from a stale, intentionally retired connection.
func (mc *managedConn) getConnAndGeneration() (*stomp.Conn, uint64) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	return mc.conn, mc.generation
}

// shutdown signals all consumer goroutines to stop. Safe to call more than
// once.
func (mc *managedConn) shutdown() {
	mc.mu.Lock()
	mc.closed = true
	mc.mu.Unlock()

	mc.doneOnce.Do(func() { close(mc.done) })
}

// isShuttingDown returns true after shutdown has been called.
func (mc *managedConn) isShuttingDown() bool {
	select {
	case <-mc.done:
		return true
	default:
		return false
	}
}

// closeWithin stops the consumer goroutines and disconnects from the broker
// inside ctx. It is safe to call more than once.
func (mc *managedConn) closeWithin(ctx context.Context) error {
	mc.closeOnce.Do(func() { mc.closeErr = mc.disconnectWithin(ctx) })
	return mc.closeErr
}

// disconnectWithin ends the current connection inside ctx. go-stomp waits
// longer for a disconnect receipt than the whole shutdown deadline, so the
// socket is closed by force when ctx expires first.
//
// The connection itself is kept, so a late Enqueue reports a send error.
func (mc *managedConn) disconnectWithin(ctx context.Context) error {
	mc.shutdown()

	mc.mu.RLock()
	conn, netConn := mc.conn, mc.netConn
	mc.mu.RUnlock()

	if conn == nil {
		return nil
	}

	result := make(chan error, 1)
	go func() { result <- conn.Disconnect() }()

	select {
	case err := <-result:
		return err
	case <-ctx.Done():
	}

	if netConn != nil {
		_ = netConn.Close()
	}
	<-result

	return fmt.Errorf("activemq: the shutdown deadline passed, so the connection to %s was closed by force: %w",
		mc.addr, ctx.Err())
}

// currentGeneration returns the generation of the current connection.
func (mc *managedConn) currentGeneration() uint64 {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	return mc.generation
}

// reconnectWithBackoff replaces the connection of generation failedGen,
// retrying up to maxAttempts times with exponential backoff capped at
// maxBackoff. Pass 0 for unlimited retries (used by long-lived consumers).
//
// When another caller has already replaced that connection, it returns nil at
// once. Many senders fail together when a connection stops, and without this
// check each of them would replace the connection that the previous one made.
//
// It returns an error if all attempts are exhausted or if shutdown is
// signaled.
func (mc *managedConn) reconnectWithBackoff(context string, failedGen uint64, maxAttempts int) error {
	mc.reconnectMu.Lock()
	defer mc.reconnectMu.Unlock()

	if mc.isShuttingDown() {
		return fmt.Errorf("activemq: reconnect aborted, shutting down (%s)", context)
	}
	if mc.currentGeneration() != failedGen {
		return nil
	}

	logger := log.GetLogger()
	backoff := initialBackoff
	attempt := 0

	for {
		if mc.isShuttingDown() {
			return fmt.Errorf("activemq: reconnect aborted, shutting down (%s)", context)
		}

		attempt++
		if maxAttempts > 0 && attempt > maxAttempts {
			return fmt.Errorf("activemq: exhausted %d reconnect attempts (%s)", maxAttempts, context)
		}

		logger.Error(fmt.Sprintf(
			"activemq: connection lost (%s), reconnecting in %s (attempt %d)…",
			context, backoff, attempt,
		))

		timer := time.NewTimer(backoff)
		select {
		case <-mc.done:
			timer.Stop()
			return fmt.Errorf("activemq: reconnect aborted, shutting down (%s)", context)
		case <-timer.C:
		}

		if mc.isShuttingDown() {
			return fmt.Errorf("activemq: reconnect aborted, shutting down (%s)", context)
		}

		if err := mc.dial(); err != nil {
			logger.Error(fmt.Sprintf("activemq: reconnect failed: %v", err))
			backoff *= backoffMultiplier
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		logger.Info(fmt.Sprintf("activemq: reconnected successfully (%s)", context))
		return nil
	}
}

// send sends one message on the current connection.
//
// Retry policy: if the send fails (typically a dead connection), send
// reconnects once and retries a single time. This is intentionally limited
// compared to the unlimited-retry loop of the consumer, because send is called
// in the request path where blocking indefinitely is unacceptable. Callers that
// need stronger delivery guarantees should persist the item and retry
// externally.
func (mc *managedConn) send(destination string, data []byte, subject, reconnectContext string) error {
	conn, generation := mc.getConnAndGeneration()
	err := conn.Send(destination, contentTypeJSON, data)
	if err == nil {
		return nil
	}

	log.GetLogger().Error(fmt.Sprintf(
		"activemq: send failed for %s, will reconnect and retry: %v", subject, err))

	if reconnErr := mc.reconnectWithBackoff(reconnectContext, generation, 1); reconnErr != nil {
		return fmt.Errorf("activemq: send failed for %s: %w", subject, reconnErr)
	}

	// Single retry after reconnect.
	if retryErr := mc.getConn().Send(destination, contentTypeJSON, data); retryErr != nil {
		return fmt.Errorf("activemq: retry send failed for %s: %w", subject, retryErr)
	}
	return nil
}

// subscribeCurrent subscribes on the current live connection and returns the
// subscription together with the generation the subscription belongs to.
//
// The subscription uses client-individual acknowledgement with a small
// prefetch. The broker sends at most consumerPrefetch messages that the
// consumer has not acknowledged, and a message that the consumer has not
// acknowledged goes back to the queue when the connection ends.
func (mc *managedConn) subscribeCurrent(destination string) (*stomp.Subscription, uint64, error) {
	conn, generation := mc.getConnAndGeneration()
	if conn == nil {
		return nil, generation, fmt.Errorf("activemq: no active connection available for subscription")
	}

	sub, err := conn.Subscribe(destination, stomp.AckClientIndividual,
		stomp.SubscribeOpt.Header(prefetchHeader, strconv.Itoa(consumerPrefetch)))
	if err != nil {
		return nil, generation, err
	}

	return sub, generation, nil
}

// probe makes a round trip to the broker on the connection of generation: an
// empty transaction that the broker must abort and confirm with a receipt. The
// result arrives on the returned channel, which has room for it, so the probe
// does not block when nobody reads the result.
//
// The broker reads the frames of a connection in order, so the receipt comes
// only after the broker has read every ACK that the consumer sent before. A
// broker that stops reading the connection, or stops processing its frames,
// does not answer. So does a client whose go-stomp I/O loop is blocked: the
// loop writes frames and hands received frames to the subscriptions on the same
// goroutine. Neither case closes the socket, and TCP keepalive sees a live
// peer.
//
// A transaction needs no permission on a destination.
func (mc *managedConn) probe(generation uint64) <-chan error {
	result := make(chan error, 1)
	timeout := probeTimeout
	conn, current := mc.getConnAndGeneration()
	if conn == nil || current != generation {
		result <- nil
		return result
	}

	go func() {
		answered := make(chan error, 1)
		go func() {
			// go-stomp waits for the receipt without a time limit. When the probe
			// times out, the connection is replaced, and the closed socket ends
			// this wait.
			tx, err := conn.BeginWithError()
			if err == nil {
				err = tx.AbortWithReceipt()
			}
			answered <- err
		}()

		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case err := <-answered:
			result <- err
		case <-timer.C:
			result <- errProbeTimeout
		}
	}()
	return result
}

// abandon reads a subscription that the consumer no longer uses until go-stomp
// closes it, so that go-stomp is not blocked on it. The messages are not
// acknowledged, and the broker sends them again when the connection ends.
func abandon(sub *stomp.Subscription) {
	if sub == nil {
		return
	}
	go func() {
		for range sub.C { //nolint:revive // The loop only drains the channel.
		}
	}()
}

// -----------------------------------------------------------------------
// Queue connections
// -----------------------------------------------------------------------

// queueConns holds the two connections of one queue. The producer and the
// consumer must not share a connection: go-stomp writes frames and hands
// received messages to the consumer on the same goroutine. With a backlog, the
// client then waits for the broker to read a SEND while the broker waits for
// the client to read a MESSAGE, and neither side moves again.
type queueConns struct {
	addr        string
	username    string
	password    string
	tlsCfg      config.TLSConfig
	destination string
	name        string

	producer *managedConn

	mu           sync.Mutex
	consumerConn *managedConn
	closed       bool
}

func newQueueConns(
	addr, username, password, destination, name string,
	tlsCfg config.TLSConfig,
) (*queueConns, error) {
	producer, err := newManagedConn(addr, username, password, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("activemq: failed to connect for %s: %w", name, err)
	}
	return &queueConns{
		addr:        addr,
		username:    username,
		password:    password,
		tlsCfg:      tlsCfg,
		destination: destination,
		name:        name,
		producer:    producer,
	}, nil
}

// start opens the consumer connection, subscribes to the destination and
// launches the consumer goroutine. handle receives the body of each message.
func (qc *queueConns) start(handle func(body []byte) error) error {
	qc.mu.Lock()
	defer qc.mu.Unlock()

	if qc.closed {
		return fmt.Errorf("activemq: %s is closed", qc.name)
	}
	if qc.consumerConn != nil {
		return fmt.Errorf("activemq: %s consumer is already started", qc.name)
	}

	mc, err := newManagedConn(qc.addr, qc.username, qc.password, qc.tlsCfg)
	if err != nil {
		return fmt.Errorf("activemq: failed to connect the consumer of %s: %w", qc.name, err)
	}

	c := &consumer{mc: mc, destination: qc.destination, name: qc.name, handle: handle}
	sub, generation, err := mc.subscribeCurrent(qc.destination)
	if err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), retireTimeout)
		defer cancel()
		_ = mc.closeWithin(ctx)
		return fmt.Errorf("activemq: failed to subscribe to %s %s: %w", qc.name, qc.destination, err)
	}

	qc.consumerConn = mc
	go c.run(sub, generation)
	return nil
}

// close stops the consumer and disconnects both connections inside ctx. Safe
// to call more than once.
func (qc *queueConns) close(ctx context.Context) error {
	qc.mu.Lock()
	qc.closed = true
	consumerConn := qc.consumerConn
	qc.mu.Unlock()

	// Close the two connections at the same time, so that a connection that
	// overruns the deadline does not delay the other one.
	consumerErr := make(chan error, 1)
	go func() {
		if consumerConn == nil {
			consumerErr <- nil
			return
		}
		consumerErr <- consumerConn.closeWithin(ctx)
	}()
	producerErr := qc.producer.closeWithin(ctx)
	return errors.Join(<-consumerErr, producerErr)
}

// -----------------------------------------------------------------------
// Consumer
// -----------------------------------------------------------------------

// consumer runs the consumer loop of one queue on its own connection.
type consumer struct {
	mc          *managedConn
	destination string
	name        string
	handle      func(body []byte) error
}

// run receives messages until shutdown, and acknowledges each message after
// the handler returns. When the handler returns an error, the worker stops: the
// loop ends without the acknowledgement, and the broker gives the message and
// the prefetched messages to another consumer when the connection closes.
//
// Retry policy: the loop retries forever (maxAttempts=0) with exponential
// backoff when the subscription is lost, because the consumer is a long-lived
// background goroutine that must stay alive for the lifetime of the process.
// It replaces the connection when the subscription closes, when an ACK fails,
// and when the broker does not answer a probe. The consumer sends a probe after
// stallCheckInterval without a message. The loop exits cleanly when Close is
// called, which signals shutdown via the done channel.
func (c *consumer) run(sub *stomp.Subscription, generation uint64) {
	defer utils.RecoverPanic("activemq consumer loop")
	// After the loop ends, nothing else reads the subscription. go-stomp can
	// block on it while the connection closes.
	defer func() { abandon(sub) }()
	logger := log.GetLogger()

	ticker := time.NewTicker(stallCheckInterval)
	defer ticker.Stop()
	// received is true when a message arrived since the last tick.
	received := false
	// probing is the result channel of the probe that runs, or nil.
	var probing <-chan error

	for {
		var msg *stomp.Message
		var ok bool

		select {
		case <-c.mc.done:
			logger.Info(fmt.Sprintf("activemq: %s consumer stopped (shutdown)", c.name))
			return
		case <-ticker.C:
			if received || probing != nil {
				received = false
				continue
			}
			probing = c.mc.probe(generation)
			continue
		case err := <-probing:
			probing = nil
			if err == nil {
				continue
			}
			logger.Error(fmt.Sprintf(
				"activemq: %s consumer connection did not answer a probe, reconnecting: %v", c.name, err))
			if sub, generation, ok = c.replace(sub, generation); !ok {
				return
			}
			continue
		case msg, ok = <-sub.C:
		}
		received = true

		if !ok {
			if c.mc.isShuttingDown() {
				logger.Info(fmt.Sprintf("activemq: %s consumer stopped (shutdown)", c.name))
				return
			}
			logger.Error(fmt.Sprintf("activemq: %s subscription channel closed, reconnecting…", c.name))
			if sub, generation, ok = c.replace(sub, generation); !ok {
				return
			}
			// A probe of the replaced connection must not replace the new one.
			probing = nil
			continue
		}

		if msg.Err != nil {
			// go-stomp closes the channel after an error, and the next receive
			// replaces the subscription.
			logger.Error(fmt.Sprintf("activemq: error receiving %s message: %v", c.name, msg.Err))
			continue
		}

		if err := c.handle(msg.Body); err != nil {
			logger.Info(fmt.Sprintf(
				"activemq: %s consumer stopped without an acknowledgement, so the broker sends the message again: %v",
				c.name, err))
			return
		}

		if err := msg.Conn.Ack(msg); err != nil {
			if c.mc.isShuttingDown() {
				logger.Info(fmt.Sprintf("activemq: %s consumer stopped (shutdown)", c.name))
				return
			}
			logger.Error(fmt.Sprintf(
				"activemq: failed to acknowledge a %s message, reconnecting: %v", c.name, err))
			if sub, generation, ok = c.replace(sub, generation); !ok {
				return
			}
			// A probe of the replaced connection must not replace the new one.
			probing = nil
		}
	}
}

// replace drops a subscription and subscribes again on a working connection.
// It reconnects first, unless the connection of the subscription was already
// replaced. It reports false when the queue shuts down.
func (c *consumer) replace(old *stomp.Subscription, failedGen uint64) (*stomp.Subscription, uint64, bool) {
	logger := log.GetLogger()
	abandon(old)

	for {
		if err := c.mc.reconnectWithBackoff(c.name+" consumer", failedGen, 0); err != nil {
			logger.Info(fmt.Sprintf("activemq: %s consumer exiting: %v", c.name, err))
			return nil, 0, false
		}

		sub, generation, err := c.mc.subscribeCurrent(c.destination)
		if err == nil {
			return sub, generation, true
		}
		logger.Error(fmt.Sprintf("activemq: failed to re-subscribe to %s: %v", c.name, err))
		failedGen = generation
	}
}

// -----------------------------------------------------------------------
// ProfileQueue
// -----------------------------------------------------------------------

// ProfileQueue is the ActiveMQ-backed ProfileUnificationQueue. It sends on one
// connection and consumes on another.
type ProfileQueue struct {
	*queueConns
}

// NewProfileQueue connects to the broker and returns a ProfileQueue that sends to the destination.
func NewProfileQueue(addr, username, password, destination string, tlsCfg config.TLSConfig) (*ProfileQueue, error) {
	qc, err := newQueueConns(addr, username, password, destination, "profile queue", tlsCfg)
	if err != nil {
		return nil, err
	}
	return &ProfileQueue{queueConns: qc}, nil
}

// Enqueue marshals the profile to JSON and sends it to ActiveMQ. See
// managedConn.send for the retry policy.
func (q *ProfileQueue) Enqueue(profile profileModel.Profile) error {
	data, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("activemq: failed to marshal profile %s: %w", profile.ProfileID, err)
	}
	return q.producer.send(q.destination, data, "profile "+profile.ProfileID, "profile enqueue")
}

// Start opens the consumer connection, subscribes to the destination and
// launches a consumer goroutine. See consumer.run for the retry policy.
func (q *ProfileQueue) Start(handler func(profileModel.Profile) error) error {
	return q.start(func(body []byte) error {
		var profile profileModel.Profile
		if err := json.Unmarshal(body, &profile); err != nil {
			log.GetLogger().Error(fmt.Sprintf("activemq: failed to unmarshal profile message: %v", err))
			return nil
		}
		// A recovered panic returns nil, so the message is acknowledged and
		// does not come back.
		defer utils.RecoverPanic("activemq profile unification handler")
		return handler(profile)
	})
}

// Close signals the consumer goroutine to stop and disconnects from ActiveMQ
// inside ctx. Safe to call more than once.
func (q *ProfileQueue) Close(ctx context.Context) error {
	return q.close(ctx)
}

// -----------------------------------------------------------------------
// SchemaSyncQueue
// -----------------------------------------------------------------------

// SchemaSyncQueue is the ActiveMQ-backed SchemaSyncQueue. It sends on one
// connection and consumes on another.
type SchemaSyncQueue struct {
	*queueConns
}

// NewSchemaSyncQueue connects to the broker and returns a SchemaSyncQueue that sends to the destination.
func NewSchemaSyncQueue(
	addr, username, password, destination string,
	tlsCfg config.TLSConfig,
) (*SchemaSyncQueue, error) {
	qc, err := newQueueConns(addr, username, password, destination, "schema sync queue", tlsCfg)
	if err != nil {
		return nil, err
	}
	return &SchemaSyncQueue{queueConns: qc}, nil
}

// Enqueue marshals the schema sync to JSON and sends it to ActiveMQ. See
// managedConn.send for the retry policy.
func (q *SchemaSyncQueue) Enqueue(sync schemaModel.ProfileSchemaSync) error {
	data, err := json.Marshal(sync)
	if err != nil {
		return fmt.Errorf("activemq: failed to marshal schema sync for tenant %s: %w", sync.OrgHandle, err)
	}
	return q.producer.send(q.destination, data, "schema sync tenant "+sync.OrgHandle, "schema sync enqueue")
}

// Start opens the consumer connection, subscribes to the destination and
// launches a consumer goroutine. See consumer.run for the retry policy.
func (q *SchemaSyncQueue) Start(handler func(schemaModel.ProfileSchemaSync) error) error {
	return q.start(func(body []byte) error {
		var sync schemaModel.ProfileSchemaSync
		if err := json.Unmarshal(body, &sync); err != nil {
			log.GetLogger().Error(fmt.Sprintf("activemq: failed to unmarshal schema sync message: %v", err))
			return nil
		}
		// A recovered panic returns nil, so the message is acknowledged and
		// does not come back.
		defer utils.RecoverPanic("activemq schema sync handler")
		return handler(sync)
	})
}

// Close signals the consumer goroutine to stop and disconnects from ActiveMQ
// inside ctx. Safe to call more than once.
func (q *SchemaSyncQueue) Close(ctx context.Context) error {
	return q.close(ctx)
}
