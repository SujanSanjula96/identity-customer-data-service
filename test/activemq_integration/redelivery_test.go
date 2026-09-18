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

package activemqintegration

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-stomp/stomp/v3"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/queue/activemq"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"
)

// redeliveryWait is how long a test waits for the broker to send a message
// again. ActiveMQ delays a redelivery, so this is well above that delay.
const redeliveryWait = 30 * time.Second

// newRedeliveryQueue opens a consumer of its own on the given destination, so
// that the test does not disturb the workers the suite started.
func newRedeliveryQueue(t *testing.T, destination string) *activemq.ProfileQueue {

	t.Helper()

	broker := config.GetCDSRuntime().Config.MessageQueue.Broker
	q, err := activemq.NewProfileQueue(broker.Addr, broker.Username, broker.Password,
		destination, config.TLSConfig{})
	if err != nil {
		t.Fatalf("failed to open a queue on %s: %v", destination, err)
	}
	return q
}

// Test_ActiveMQ_keepsAJobRefusedAtShutdown is the message loss the review
// found.
//
// A worker that is shutting down refuses a job rather than start it against a
// pool that is about to close. With AckAuto the broker had already treated the
// message as delivered, so refusing it lost the work. The job has to survive
// the restart instead.
func Test_ActiveMQ_keepsAJobRefusedAtShutdown(t *testing.T) {

	destination := fmt.Sprintf("/queue/cds-test-redelivery-%d", time.Now().UnixNano())
	profileID := "refused-at-shutdown"

	// The consumer that is shutting down. It refuses whatever it is given.
	stopping := newRedeliveryQueue(t, destination)

	delivered := make(chan string, 8)
	if err := stopping.Start(func(profile profileModel.Profile) error {
		select {
		case delivered <- profile.ProfileId:
		default:
		}
		return workers.ErrWorkerStopping
	}); err != nil {
		t.Fatal(err)
	}

	if err := stopping.Enqueue(profileModel.Profile{ProfileId: profileID}); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-delivered:
		if got != profileID {
			t.Fatalf("expected %q, got %q", profileID, got)
		}
	case <-time.After(redeliveryWait):
		t.Fatal("the message never reached the consumer that was shutting down")
	}

	// Shutdown finishes. The message was never acknowledged.
	if err := stopping.Close(context.Background()); err != nil {
		t.Logf("close reported %v", err)
	}

	// A new instance starts, and this one processes the job.
	restarted := newRedeliveryQueue(t, destination)
	t.Cleanup(func() { _ = restarted.Close(context.Background()) })

	processed := make(chan string, 8)
	if err := restarted.Start(func(profile profileModel.Profile) error {
		select {
		case processed <- profile.ProfileId:
		default:
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-processed:
		if got != profileID {
			t.Fatalf("expected the same message back, got %q", got)
		}
	case <-time.After(redeliveryWait):
		// Say where the message went, not only that it never arrived. A
		// message in the dead letter queue means the broker took it out of
		// the rotation rather than losing it, which is a different fault with
		// a different fix.
		dead := deadLetterBodies(t, 5*time.Second)
		t.Fatalf("the refused message was lost: the broker never sent it again. "+
			"the dead letter queue holds %d message(s): %v", len(dead), dead)
	}
}

// deadLetterBodies reports what is waiting in the dead letter queue.
//
// It reads with AckAuto, so it consumes what it finds. That is deliberate: the
// test is already failing, and a message left behind would confuse the next
// run.
func deadLetterBodies(t *testing.T, wait time.Duration) []string {

	t.Helper()

	broker := config.GetCDSRuntime().Config.MessageQueue.Broker
	conn, err := stomp.Dial("tcp", broker.Addr,
		stomp.ConnOpt.Login(broker.Username, broker.Password))
	if err != nil {
		t.Logf("could not reach the broker to read the dead letter queue: %v", err)
		return nil
	}
	defer func() { _ = conn.Disconnect() }()

	sub, err := conn.Subscribe("/queue/ActiveMQ.DLQ", stomp.AckAuto)
	if err != nil {
		t.Logf("could not subscribe to the dead letter queue: %v", err)
		return nil
	}

	var bodies []string
	timer := time.NewTimer(wait)
	defer timer.Stop()

	for {
		select {
		case msg, ok := <-sub.C:
			if !ok {
				return bodies
			}
			if msg.Err == nil {
				bodies = append(bodies, string(msg.Body))
			}
		case <-timer.C:
			return bodies
		}
	}
}

// Test_ActiveMQ_doesNotRepeatAJobThatWasProcessed is the control. Without it
// the test above would pass on a queue that repeats every message.
func Test_ActiveMQ_doesNotRepeatAJobThatWasProcessed(t *testing.T) {

	destination := fmt.Sprintf("/queue/cds-test-processed-%d", time.Now().UnixNano())
	profileID := "processed-once"

	first := newRedeliveryQueue(t, destination)

	processed := make(chan string, 8)
	if err := first.Start(func(profile profileModel.Profile) error {
		select {
		case processed <- profile.ProfileId:
		default:
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := first.Enqueue(profileModel.Profile{ProfileId: profileID}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-processed:
	case <-time.After(redeliveryWait):
		t.Fatal("the message never reached the consumer")
	}

	if err := first.Close(context.Background()); err != nil {
		t.Logf("close reported %v", err)
	}

	// Nothing is left on the queue for the next instance.
	second := newRedeliveryQueue(t, destination)
	t.Cleanup(func() { _ = second.Close(context.Background()) })

	again := make(chan string, 8)
	if err := second.Start(func(profile profileModel.Profile) error {
		select {
		case again <- profile.ProfileId:
		default:
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-again:
		t.Errorf("expected an acknowledged message to be gone, got %q again", got)
	case <-time.After(10 * time.Second):
	}
}

// Test_ActiveMQ_redeliversAfterAFailedAttempt is the case the review named.
//
// A refused message stays unacknowledged, and the broker keeps it. It used to
// come back only when the connection closed, which is when the process stops,
// so a transient database failure left the job stuck until the pod restarted.
// Nobody closes the consumer here.
func Test_ActiveMQ_redeliversAfterAFailedAttempt(t *testing.T) {

	destination := fmt.Sprintf("/queue/cds-test-retry-%d", time.Now().UnixNano())
	profileID := "fails-once"

	queue := newRedeliveryQueue(t, destination)
	t.Cleanup(func() { _ = queue.Close(context.Background()) })

	attempts := make(chan int32, 8)
	var count atomic.Int32

	if err := queue.Start(func(profile profileModel.Profile) error {
		attempt := count.Add(1)
		select {
		case attempts <- attempt:
		default:
		}
		if attempt == 1 {
			return errors.New("a transient failure, as a database that is briefly unreachable gives")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := queue.Enqueue(profileModel.Profile{ProfileId: profileID}); err != nil {
		t.Fatal(err)
	}

	select {
	case attempt := <-attempts:
		if attempt != 1 {
			t.Fatalf("expected the first attempt, got %d", attempt)
		}
	case <-time.After(redeliveryWait):
		t.Fatal("the message never reached the handler")
	}

	// The consumer stays open. The message has to come back anyway.
	select {
	case attempt := <-attempts:
		if attempt != 2 {
			t.Fatalf("expected a second attempt, got %d", attempt)
		}
	case <-time.After(redeliveryWait):
		t.Fatal("the message was not redelivered while the consumer stayed open")
	}
}
