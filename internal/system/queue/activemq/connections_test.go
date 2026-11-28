/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
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
	"context"
	"testing"
	"time"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
)

// Test_ProfileQueue_producerReconnectKeepsTheSubscription checks that the producer and the consumer
// use separate connections. A reconnect after a failed send must not end the subscription: on a
// shared connection, the jobs in the hands of the workers could not be acknowledged, and the broker
// would deliver them again.
func Test_ProfileQueue_producerReconnectKeepsTheSubscription(t *testing.T) {
	broker := startFakeBroker(t, true)
	q, err := NewProfileQueue(broker.addr(), "", "", "/queue/profiles", config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to connect to the fake broker: %v", err)
	}
	defer closeQueue(t, q.Close)

	handled := make(chan string, 1)
	if err := q.Start(func(p profileModel.Profile) error {
		handled <- p.ProfileID
		return nil
	}, 1); err != nil {
		t.Fatalf("failed to start the queue: %v", err)
	}
	nextFrame(t, broker.subscribes, "SUBSCRIBE")
	if got := broker.connections(); got != 2 {
		t.Fatalf("the queue opened %d connections, want 2 (one to send, one to consume)", got)
	}

	if err := q.producer.dial(); err != nil {
		t.Fatalf("the producer did not reconnect: %v", err)
	}
	noFrame(t, broker.subscribes, "SUBSCRIBE")

	broker.deliver(t, "msg-1", `{"profile_id":"p-1"}`)
	select {
	case id := <-handled:
		if id != "p-1" {
			t.Fatalf("the handler got profile %q, want p-1", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the subscription did not deliver the job after the producer reconnected")
	}
	if got := nextFrame(t, broker.acks, "ACK")["id"]; got != "msg-1" {
		t.Errorf("ACK id = %q, want msg-1", got)
	}
}

// Test_SchemaSyncQueue_usesSeparateConnections checks the same split for the schema sync queue.
func Test_SchemaSyncQueue_usesSeparateConnections(t *testing.T) {
	broker := startFakeBroker(t, true)
	q, err := NewSchemaSyncQueue(broker.addr(), "", "", "/queue/schema", config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to connect to the fake broker: %v", err)
	}
	defer closeQueue(t, q.Close)

	if err := q.Start(func(schemaModel.ProfileSchemaSync) {}); err != nil {
		t.Fatalf("failed to start the queue: %v", err)
	}
	nextFrame(t, broker.subscribes, "SUBSCRIBE")
	if got := broker.connections(); got != 2 {
		t.Fatalf("the queue opened %d connections, want 2 (one to send, one to consume)", got)
	}

	if err := q.producer.dial(); err != nil {
		t.Fatalf("the producer did not reconnect: %v", err)
	}
	noFrame(t, broker.subscribes, "SUBSCRIBE")
}

// Test_dial_doesNotWaitForTheReplacedConnection checks that a reconnect does not wait for the old
// connection. go-stomp waits up to 30 seconds for a disconnect receipt, and a reconnect holds the
// lock that the sends of the request path wait on. The old socket closes by force after
// retireTimeout.
func Test_dial_doesNotWaitForTheReplacedConnection(t *testing.T) {
	retireAfter := retireTimeout
	retireTimeout = 200 * time.Millisecond
	t.Cleanup(func() { retireTimeout = retireAfter })

	broker := startFakeBroker(t, false)
	q, err := NewProfileQueue(broker.addr(), "", "", "/queue/profiles", config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to connect to the fake broker: %v", err)
	}
	// The broker never answers a disconnect, so Close ends at its deadline.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = q.Close(ctx)
	}()

	start := time.Now()
	if err := q.producer.dial(); err != nil {
		t.Fatalf("the producer did not reconnect: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("the reconnect took %s, so it waited for the old connection", elapsed)
	}

	select {
	case <-broker.readEnded:
	case <-time.After(5 * time.Second):
		t.Fatal("the old connection was not closed after retireTimeout")
	}
}
