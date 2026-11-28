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
	"errors"
	"fmt"
	"testing"
	"time"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
)

// nextFrame returns the headers of the next frame on frames.
func nextFrame(t *testing.T, frames chan map[string]string, kind string) map[string]string {
	t.Helper()

	select {
	case headers := <-frames:
		return headers
	case <-time.After(5 * time.Second):
		t.Fatalf("the broker received no %s frame", kind)
		return nil
	}
}

// noFrame fails when a frame arrives on frames within a short wait.
func noFrame(t *testing.T, frames chan map[string]string, kind string) {
	t.Helper()

	select {
	case headers := <-frames:
		t.Fatalf("the broker received an unexpected %s frame: %v", kind, headers)
	case <-time.After(200 * time.Millisecond):
	}
}

// deliver sends a MESSAGE to the last subscriber. Its ack header is the message ID.
func (b *fakeBroker) deliver(t *testing.T, messageID, body string) {
	t.Helper()

	b.writeMu.Lock()
	conn, subscription := b.subscriber, b.subscription
	b.writeMu.Unlock()
	frame := fmt.Sprintf("MESSAGE\ndestination:/queue/profiles\nmessage-id:%s\nsubscription:%s\nack:%s\n"+
		"content-type:application/json\n\n%s\x00", messageID, subscription, messageID, body)
	if err := b.write(conn, frame); err != nil {
		t.Fatalf("failed to deliver the message: %v", err)
	}
}

// startProfileConsumer connects a profile queue to a fake broker and starts it with handler.
func startProfileConsumer(t *testing.T, handler func(profileModel.Profile) error, workers int) *fakeBroker {
	t.Helper()

	broker := startFakeBroker(t, true)
	q, err := NewProfileQueue(broker.addr(), "", "", "/queue/profiles", config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to connect to the fake broker: %v", err)
	}
	t.Cleanup(func() { closeQueue(t, q.Close) })

	if err := q.Start(handler, workers); err != nil {
		t.Fatalf("failed to start the queue: %v", err)
	}
	nextFrame(t, broker.subscribes, "SUBSCRIBE")
	return broker
}

// Test_ProfileQueue_Start_asksForNoMoreJobsThanItHasWorkers checks that each job is acknowledged
// on its own, and that the broker holds back jobs the workers cannot take yet, so they stay with
// the broker for another instance.
func Test_ProfileQueue_Start_asksForNoMoreJobsThanItHasWorkers(t *testing.T) {
	broker := startFakeBroker(t, true)
	q, err := NewProfileQueue(broker.addr(), "", "", "/queue/profiles", config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to connect to the fake broker: %v", err)
	}
	defer closeQueue(t, q.Close)

	if err := q.Start(func(profileModel.Profile) error { return nil }, 3); err != nil {
		t.Fatalf("failed to start the queue: %v", err)
	}

	headers := nextFrame(t, broker.subscribes, "SUBSCRIBE")
	if got := headers["ack"]; got != "client-individual" {
		t.Errorf("ack header = %q, want \"client-individual\"", got)
	}
	if got := headers["activemq.prefetchSize"]; got != "3" {
		t.Errorf("activemq.prefetchSize header = %q, want \"3\"", got)
	}
}

// Test_ProfileQueue_acknowledgesAJobOnceItsHandlerReturns checks that a job is acknowledged only
// after it is done, so a job that an instance was working on when it stopped is delivered again.
func Test_ProfileQueue_acknowledgesAJobOnceItsHandlerReturns(t *testing.T) {
	received := make(chan string, 1)
	release := make(chan struct{})
	broker := startProfileConsumer(t, func(p profileModel.Profile) error {
		received <- p.ProfileID
		<-release
		return nil
	}, 1)

	broker.deliver(t, "m-1", `{"profile_id":"p-1","org_id":"org-1"}`)
	select {
	case id := <-received:
		if id != "p-1" {
			t.Errorf("handler received profile %q, want \"p-1\"", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler was not called")
	}
	noFrame(t, broker.acks, "ACK")

	close(release)
	if got := nextFrame(t, broker.acks, "ACK")["id"]; got != "m-1" {
		t.Errorf("ACK id = %q, want \"m-1\"", got)
	}
}

// Test_ProfileQueue_leavesAJobForTheBrokerWhenItsHandlerFails checks that a job the handler did
// not process is not acknowledged, so the broker delivers it again.
func Test_ProfileQueue_leavesAJobForTheBrokerWhenItsHandlerFails(t *testing.T) {
	called := make(chan struct{}, 1)
	broker := startProfileConsumer(t, func(profileModel.Profile) error {
		called <- struct{}{}
		return errors.New("the worker is stopping")
	}, 1)

	broker.deliver(t, "m-1", `{"profile_id":"p-1","org_id":"org-1"}`)
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler was not called")
	}
	noFrame(t, broker.acks, "ACK")
}

// Test_ProfileQueue_dropsAJobItCannotRead checks that a message that is not a profile is
// acknowledged without calling the handler, since a second delivery could not read it either.
func Test_ProfileQueue_dropsAJobItCannotRead(t *testing.T) {
	called := make(chan struct{}, 1)
	broker := startProfileConsumer(t, func(profileModel.Profile) error {
		called <- struct{}{}
		return nil
	}, 1)

	broker.deliver(t, "m-1", `not json`)
	if got := nextFrame(t, broker.acks, "ACK")["id"]; got != "m-1" {
		t.Errorf("ACK id = %q, want \"m-1\"", got)
	}
	select {
	case <-called:
		t.Error("the handler was called for a message that is not a profile")
	default:
	}
}
