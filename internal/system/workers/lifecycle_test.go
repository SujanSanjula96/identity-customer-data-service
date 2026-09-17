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

package workers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/internal/system/queue/inmemory"
)

// settle is how long a test waits before it concludes that a call is blocked.
const settle = 300 * time.Millisecond

// Test_stop_waitsForARunningJob is the test the review asked for.
//
// A job holds the database. Shutdown must not return while it does, because
// the caller closes the connection pool next and would cut the job in half.
func Test_stop_waitsForARunningJob(t *testing.T) {

	lifecycle := newJobLifecycle()

	started := make(chan struct{})
	release := make(chan struct{})
	var finished atomic.Bool

	go func() {
		_ = lifecycle.run(func(ctx context.Context) error {
			close(started)
			// Stands for a multi-step merge that holds a transaction.
			<-release
			finished.Store(true)
			return nil
		})
	}()
	<-started

	stopped := make(chan error, 1)
	go func() { stopped <- lifecycle.stop(func() error { return nil }) }()

	select {
	case <-stopped:
		t.Fatal("stop returned while a job was still at work")
	case <-time.After(settle):
	}

	close(release)

	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("expected a clean stop, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return after the job finished")
	}

	if !finished.Load() {
		t.Error("expected the job to run to its end")
	}
}

// Test_stop_dropsAJobThatHasNotStarted covers the other half. A queue keeps
// calling its handler for whatever it has buffered, even after Close. Those
// jobs must not start work against a pool that is about to close.
func Test_stop_dropsAJobThatHasNotStarted(t *testing.T) {

	lifecycle := newJobLifecycle()

	if err := lifecycle.stop(func() error { return nil }); err != nil {
		t.Fatal(err)
	}

	var ran atomic.Bool
	err := lifecycle.run(func(context.Context) error {
		ran.Store(true)
		return nil
	})

	if ran.Load() {
		t.Error("expected a job that arrives after shutdown to be refused")
	}
	// The error is what keeps the message. A broker leaves an unacknowledged
	// message for redelivery, so the work survives the restart.
	if !errors.Is(err, ErrWorkerStopping) {
		t.Errorf("expected ErrWorkerStopping so the message is not acknowledged, got %v", err)
	}
}

// Test_stop_cancelsAJobThatOutlastsTheBudget checks the last resort. A job
// that will not finish must not hold shutdown open for ever.
func Test_stop_cancelsAJobThatOutlastsTheBudget(t *testing.T) {

	lifecycle := newJobLifecycle()
	lifecycle.budget = 200 * time.Millisecond

	started := make(chan struct{})
	var cancelled atomic.Bool

	go func() {
		_ = lifecycle.run(func(ctx context.Context) error {
			close(started)
			// A job that ignores its own deadline still ends, because shutdown
			// cancels the context every job derives from.
			<-ctx.Done()
			cancelled.Store(true)
			return ctx.Err()
		})
	}()
	<-started

	start := time.Now()
	err := lifecycle.stop(func() error { return nil })
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected stop to report that a job had to be cancelled")
	}
	if elapsed > 5*time.Second {
		t.Errorf("stop took %v, so the budget did not bound it", elapsed)
	}
	if !cancelled.Load() {
		t.Error("expected the job's context to end")
	}
}

// Test_stop_waitsForARunningJobOnTheRealQueue runs the same case through the
// queue the worker really uses.
//
// Closing the in-memory queue closes its channel. The consumer then keeps
// calling the handler for everything already buffered, so Close on its own
// says nothing about whether work has stopped.
func Test_stop_waitsForARunningJobOnTheRealQueue(t *testing.T) {

	q := inmemory.NewProfileQueue(10)
	lifecycle := newJobLifecycle()

	started := make(chan struct{})
	release := make(chan struct{})
	var handled atomic.Int32

	// Exactly what StartProfileWorker builds.
	refused := make(chan error, 16)
	if err := q.Start(func(profile profileModel.Profile) error {
		err := lifecycle.run(func(context.Context) error {
			if handled.Add(1) == 1 {
				close(started)
				<-release
			}
			return nil
		})
		if err != nil {
			select {
			case refused <- err:
			default:
			}
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// One job to block on, and more behind it to be dropped.
	for i := 0; i < 5; i++ {
		if err := q.Enqueue(profileModel.Profile{ProfileId: "p"}); err != nil {
			t.Fatal(err)
		}
	}
	<-started

	stopped := make(chan error, 1)
	go func() { stopped <- lifecycle.stop(q.Close) }()

	select {
	case <-stopped:
		t.Fatal("stop returned while a job was still at work")
	case <-time.After(settle):
	}

	close(release)

	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("expected a clean stop, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return after the job finished")
	}

	// The queue may keep handing buffered items to the handler, but none of
	// them starts work.
	if got := handled.Load(); got != 1 {
		t.Errorf("expected only the job that was already running to do work, got %d", got)
	}

	// Each of those items was refused rather than silently dropped, so a
	// broker would keep it.
	select {
	case err := <-refused:
		if !errors.Is(err, ErrWorkerStopping) {
			t.Errorf("expected ErrWorkerStopping for a buffered item, got %v", err)
		}
	case <-time.After(time.Second):
		t.Error("expected the buffered items to be refused with an error")
	}
}
