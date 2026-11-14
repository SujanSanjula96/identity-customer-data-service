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
	"context"
	"testing"
	"time"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
)

// nextSend returns the headers of the next SEND frame the broker receives.
func (b *fakeBroker) nextSend(t *testing.T) map[string]string {

	t.Helper()

	select {
	case headers := <-b.sends:
		return headers
	case <-time.After(5 * time.Second):
		t.Fatal("the broker received no SEND frame")
		return nil
	}
}

// closeQueue closes a queue at the end of a test.
func closeQueue(t *testing.T, close func(context.Context) error) {

	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := close(ctx); err != nil {
		t.Errorf("failed to close the queue: %v", err)
	}
}

// Test_ProfileQueue_Enqueue_sendsAPersistentMessage checks that a profile job
// asks the broker to store it. ActiveMQ keeps a STOMP message without the
// persistent header only in memory, so a broker restart discards it.
func Test_ProfileQueue_Enqueue_sendsAPersistentMessage(t *testing.T) {

	broker := startFakeBroker(t, true)

	q, err := NewProfileQueue(broker.addr(), "", "", "/queue/profiles", config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to connect to the fake broker: %v", err)
	}
	defer closeQueue(t, q.Close)

	if err := q.Enqueue(profileModel.Profile{ProfileId: "p-1"}); err != nil {
		t.Fatalf("failed to enqueue the profile: %v", err)
	}

	headers := broker.nextSend(t)
	if got := headers["persistent"]; got != "true" {
		t.Errorf("persistent header = %q, want \"true\"", got)
	}
	if got := headers["destination"]; got != "/queue/profiles" {
		t.Errorf("destination header = %q, want \"/queue/profiles\"", got)
	}
}

// Test_SchemaSyncQueue_Enqueue_sendsAPersistentMessage is the same check for
// the schema sync queue.
func Test_SchemaSyncQueue_Enqueue_sendsAPersistentMessage(t *testing.T) {

	broker := startFakeBroker(t, true)

	q, err := NewSchemaSyncQueue(broker.addr(), "", "", "/queue/schema", config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to connect to the fake broker: %v", err)
	}
	defer closeQueue(t, q.Close)

	if err := q.Enqueue(schemaModel.ProfileSchemaSync{OrgHandle: "org-1"}); err != nil {
		t.Fatalf("failed to enqueue the schema sync: %v", err)
	}

	headers := broker.nextSend(t)
	if got := headers["persistent"]; got != "true" {
		t.Errorf("persistent header = %q, want \"true\"", got)
	}
	if got := headers["destination"]; got != "/queue/schema" {
		t.Errorf("destination header = %q, want \"/queue/schema\"", got)
	}
}
