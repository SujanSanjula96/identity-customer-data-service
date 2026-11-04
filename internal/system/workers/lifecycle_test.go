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
)

// settleDelay is how long a check waits before it concludes that a call has
// not returned. It only has to outlast the scheduling of a goroutine.
const settleDelay = 50 * time.Millisecond

// recordingQueue stands in for a queue. It counts the closes and records how
// many handlers were still at work when the close ran.
type recordingQueue struct {
	active        *atomic.Int64
	closes        atomic.Int64
	activeAtClose atomic.Int64
}

func (q *recordingQueue) close(context.Context) error {

	q.closes.Add(1)
	q.activeAtClose.Store(q.active.Load())
	return nil
}

// Test_stop_returnsWhenNoJobIsActive covers the ordinary case: nothing is at
// work, so the stop returns at once and reports a clean stop.
func Test_stop_returnsWhenNoJobIsActive(t *testing.T) {

	lifecycle := newJobLifecycle()
	queue := &recordingQueue{active: &atomic.Int64{}}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := lifecycle.stop(ctx, queue.close); err != nil {
		t.Fatalf("expected a clean stop, got %v", err)
	}
	if queue.closes.Load() != 1 {
		t.Errorf("expected the queue to close once, it closed %d times", queue.closes.Load())
	}
}

// Test_stop_waitsForAnActiveJob blocks a handler, so the stop must not return.
func Test_stop_waitsForAnActiveJob(t *testing.T) {

	lifecycle := newJobLifecycle()
	queue := &recordingQueue{active: &atomic.Int64{}}

	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = lifecycle.run(func(context.Context) {
			queue.active.Add(1)
			close(started)
			<-release
			queue.active.Add(-1)
		})
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stopped := make(chan error, 1)
	go func() { stopped <- lifecycle.stop(ctx, queue.close) }()

	select {
	case err := <-stopped:
		t.Fatalf("stop returned while a job was still at work: %v", err)
	case <-time.After(settleDelay):
	}

	if queue.closes.Load() != 0 {
		t.Error("the queue closed before the job returned")
	}

	close(release)

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("expected a clean stop, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return after the job finished")
	}
}

// Test_stop_closesTheQueueAfterEveryHandlerReturns checks the order the pool
// close depends on. The queue close stands where provider.CloseDB stands in
// the server, so no handler may be at work when it runs.
func Test_stop_closesTheQueueAfterEveryHandlerReturns(t *testing.T) {

	lifecycle := newJobLifecycle()
	queue := &recordingQueue{active: &atomic.Int64{}}

	for i := 0; i < 8; i++ {
		go func() {
			_ = lifecycle.run(func(context.Context) {
				queue.active.Add(1)
				time.Sleep(time.Millisecond)
				queue.active.Add(-1)
			})
		}()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := lifecycle.stop(ctx, queue.close); err != nil {
		t.Fatalf("expected a clean stop, got %v", err)
	}
	if got := queue.activeAtClose.Load(); got != 0 {
		t.Errorf("expected no handler at work when the queue closed, %d were", got)
	}
}

// Test_run_refusesAJobAfterTheWorkerStops checks that a worker which is
// stopping takes no new work.
func Test_run_refusesAJobAfterTheWorkerStops(t *testing.T) {

	lifecycle := newJobLifecycle()
	queue := &recordingQueue{active: &atomic.Int64{}}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := lifecycle.stop(ctx, queue.close); err != nil {
		t.Fatalf("expected a clean stop, got %v", err)
	}

	var ran atomic.Bool
	err := lifecycle.run(func(context.Context) { ran.Store(true) })

	if !errors.Is(err, ErrWorkerStopping) {
		t.Errorf("expected ErrWorkerStopping, got %v", err)
	}
	if ran.Load() {
		t.Error("the job ran after the worker had started to stop")
	}
}

// Test_stop_cancelsTheJobAtTheDeadline checks that a handler which does not
// return inside the deadline has its context cancelled.
func Test_stop_cancelsTheJobAtTheDeadline(t *testing.T) {

	lifecycle := newJobLifecycle()
	queue := &recordingQueue{active: &atomic.Int64{}}

	started := make(chan struct{})
	cancelled := make(chan struct{})
	go func() {
		_ = lifecycle.run(func(jobCtx context.Context) {
			close(started)
			<-jobCtx.Done()
			close(cancelled)
		})
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), settleDelay)
	defer cancel()

	err := lifecycle.stop(ctx, queue.close)

	if !errors.Is(err, ErrShutdownIncomplete) {
		t.Fatalf("expected ErrShutdownIncomplete, got %v", err)
	}

	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the job context was not cancelled at the deadline")
	}
}

// Test_stop_reportsAnIncompleteShutdownAsSuch checks that a forced shutdown
// is never reported as a clean stop, even when the handler ignores its context.
func Test_stop_reportsAnIncompleteShutdownAsSuch(t *testing.T) {

	lifecycle := newJobLifecycle()
	queue := &recordingQueue{active: &atomic.Int64{}}

	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	go func() {
		_ = lifecycle.run(func(context.Context) {
			close(started)
			<-release
		})
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), settleDelay)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- lifecycle.stop(ctx, queue.close) }()

	select {
	case err := <-done:
		if !errors.Is(err, ErrShutdownIncomplete) {
			t.Fatalf("expected ErrShutdownIncomplete, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop waited for a handler that ignores its context")
	}
}

// Test_stop_isSafeWhenCalledMoreThanOnce covers a repeated shutdown.
func Test_stop_isSafeWhenCalledMoreThanOnce(t *testing.T) {

	lifecycle := newJobLifecycle()
	queue := &recordingQueue{active: &atomic.Int64{}}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for attempt := 1; attempt <= 3; attempt++ {
		if err := lifecycle.stop(ctx, queue.close); err != nil {
			t.Fatalf("call %d to stop failed: %v", attempt, err)
		}
	}
}

// Test_StopCookieCleanupWorker_isSafeWithoutAStart checks the stop of a worker
// the configuration disabled, and a repeated stop.
func Test_StopCookieCleanupWorker_isSafeWithoutAStart(t *testing.T) {

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for attempt := 1; attempt <= 2; attempt++ {
		if err := StopCookieCleanupWorker(ctx); err != nil {
			t.Fatalf("call %d failed: %v", attempt, err)
		}
	}
}

// Test_StopCookieCleanupWorker_waitsForItsGoroutine checks that the stop
// reports success only when the goroutine has returned. The sweep needs a
// database, so the state is installed here.
func Test_StopCookieCleanupWorker_waitsForItsGoroutine(t *testing.T) {

	done := make(chan struct{})
	installCookieCleanupWorker(t, done)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stopped := make(chan error, 1)
	go func() { stopped <- StopCookieCleanupWorker(ctx) }()

	select {
	case err := <-stopped:
		t.Fatalf("the stop returned while the sweep was still running: %v", err)
	case <-time.After(settleDelay):
	}

	close(done)

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("expected a clean stop, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stop did not return after the goroutine had gone")
	}
}

// Test_StopCookieCleanupWorker_reportsAnIncompleteShutdown checks the forced
// path. A sweep that has not returned at the deadline is reported, not called
// a success.
func Test_StopCookieCleanupWorker_reportsAnIncompleteShutdown(t *testing.T) {

	// The channel is never closed, so the goroutine never returns.
	installCookieCleanupWorker(t, make(chan struct{}))

	ctx, cancel := context.WithTimeout(context.Background(), settleDelay)
	defer cancel()

	if err := StopCookieCleanupWorker(ctx); !errors.Is(err, ErrShutdownIncomplete) {
		t.Fatalf("expected ErrShutdownIncomplete, got %v", err)
	}
}

// installCookieCleanupWorker puts the worker into the state a start leaves
// behind, with done standing for the goroutine that has not returned yet.
func installCookieCleanupWorker(t *testing.T, done chan struct{}) {

	t.Helper()

	cookieCleanupMu.Lock()
	cookieCleanupCancel = func() {}
	cookieCleanupDone = done
	cookieCleanupMu.Unlock()

	t.Cleanup(func() {
		cookieCleanupMu.Lock()
		cookieCleanupCancel, cookieCleanupDone = nil, nil
		cookieCleanupMu.Unlock()
	})
}
