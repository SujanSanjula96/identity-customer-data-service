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

package main

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"
)

func TestMain(m *testing.M) {

	if err := log.Init("ERROR"); err != nil {
		panic(err)
	}
	config.OverrideCDSRuntime(config.Config{})
	os.Exit(m.Run())
}

// sequence records the steps of one shutdown in the order they ran.
type sequence struct {
	mu    sync.Mutex
	steps []string
}

func (s *sequence) record(step string) {

	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, step)
}

func (s *sequence) get() []string {

	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.steps...)
}

// Test_shutdown_closesThePoolAfterTheWorkersStop checks the order: the HTTP
// intake stops, every worker finishes its jobs, and only then does the database
// pool close.
func Test_shutdown_closesThePoolAfterTheWorkersStop(t *testing.T) {

	steps := &sequence{}
	var stopping atomic.Int64
	var stoppingAtClose atomic.Int64

	stop := func(name string) func(context.Context) error {
		return func(context.Context) error {
			stopping.Add(1)
			defer stopping.Add(-1)
			time.Sleep(10 * time.Millisecond)
			steps.record("worker:" + name)
			return nil
		}
	}

	httpShutdown := func(context.Context) error {
		steps.record("http")
		return nil
	}
	closeDB := func() error {
		stoppingAtClose.Store(stopping.Load())
		steps.record("pool")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	workerList := []namedWorker{
		{name: "profile", stop: stop("profile")},
		{name: "schema sync", stop: stop("schema sync")},
		{name: "cookie cleanup", stop: stop("cookie cleanup")},
	}

	if err := shutdown(ctx, log.GetLogger(), httpShutdown, workerList, closeDB); err != nil {
		t.Fatalf("expected a clean shutdown, got %v", err)
	}

	got := steps.get()
	if len(got) != 5 {
		t.Fatalf("expected five steps, got %v", got)
	}
	if got[0] != "http" {
		t.Errorf("expected the HTTP intake to stop first, the order was %v", got)
	}
	if got[4] != "pool" {
		t.Errorf("expected the pool to close last, the order was %v", got)
	}
	if n := stoppingAtClose.Load(); n != 0 {
		t.Errorf("expected no worker to be stopping when the pool closed, %d were", n)
	}
}

// Test_shutdown_reportsAForcedCloseAtTheDeadline checks that an incomplete
// shutdown is not reported as a clean stop, and that the pool still closes.
func Test_shutdown_reportsAForcedCloseAtTheDeadline(t *testing.T) {

	var closed atomic.Bool

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	workerList := []namedWorker{
		{name: "profile", stop: func(context.Context) error { return nil }},
		{name: "schema sync", stop: func(context.Context) error { return workers.ErrShutdownIncomplete }},
	}

	err := shutdown(ctx, log.GetLogger(),
		func(context.Context) error { return nil },
		workerList,
		func() error { closed.Store(true); return nil })

	if !errors.Is(err, workers.ErrShutdownIncomplete) {
		t.Fatalf("expected ErrShutdownIncomplete, got %v", err)
	}
	if !closed.Load() {
		t.Error("expected the pool to close even after a forced shutdown")
	}
}

// Test_shutdown_reportsAPoolThatDidNotClose checks that a failed close reaches
// the caller rather than only the log.
func Test_shutdown_reportsAPoolThatDidNotClose(t *testing.T) {

	closeFailed := errors.New("the pool did not close")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := shutdown(ctx, log.GetLogger(),
		func(context.Context) error { return nil },
		nil,
		func() error { return closeFailed })

	if !errors.Is(err, closeFailed) {
		t.Fatalf("expected the close error, got %v", err)
	}
}

// Test_shutdown_stopsTheWorkersAtTheSameTime checks that the workers stop at
// the same time. Each stop waits for the other two to start, so a sequence of
// stops cannot finish.
func Test_shutdown_stopsTheWorkersAtTheSameTime(t *testing.T) {

	const count = 3

	// A barrier: each stop reports that it started and then waits for the
	// other two.
	var barrier sync.WaitGroup
	barrier.Add(count)

	stop := func(context.Context) error {
		barrier.Done()
		barrier.Wait()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	workerList := make([]namedWorker, 0, count)
	for _, name := range []string{"profile", "schema sync", "cookie cleanup"} {
		workerList = append(workerList, namedWorker{name: name, stop: stop})
	}

	done := make(chan error, 1)
	go func() {
		done <- shutdown(ctx, log.GetLogger(), func(context.Context) error { return nil },
			workerList, func() error { return nil })
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected a clean shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the workers did not stop at the same time")
	}
}

// Test_shutdown_usesTheConfiguredGracePeriod checks that the number in the
// configuration becomes the deadline that bounds the sequence.
func Test_shutdown_usesTheConfiguredGracePeriod(t *testing.T) {

	grace, err := config.ResolveShutdownGracePeriod(config.ShutdownConfig{GracePeriodSeconds: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if grace != time.Second {
		t.Fatalf("got %s, want 1s", grace)
	}

	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	// A worker whose job never returns, so only the deadline can end the wait.
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	workerList := []namedWorker{{
		name: "profile",
		stop: func(ctx context.Context) error {
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return workers.ErrShutdownIncomplete
			}
		},
	}}

	start := time.Now()
	err = shutdown(ctx, log.GetLogger(), func(context.Context) error { return nil },
		workerList, func() error { return nil })
	elapsed := time.Since(start)

	if !errors.Is(err, workers.ErrShutdownIncomplete) {
		t.Fatalf("expected ErrShutdownIncomplete, got %v", err)
	}
	if elapsed < grace {
		t.Errorf("the shutdown returned after %s, before the configured %s", elapsed, grace)
	}
	if elapsed > grace+5*time.Second {
		t.Errorf("the shutdown took %s, well past the configured %s", elapsed, grace)
	}
}

// Test_shutdown_reportsAnHTTPServerThatDidNotStop checks that an HTTP server
// which reached the deadline reaches the caller. The workers and the pool stop
// cleanly, so nothing else would report it.
func Test_shutdown_reportsAnHTTPServerThatDidNotStop(t *testing.T) {

	var closed atomic.Bool

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	workerList := []namedWorker{
		{name: "profile", stop: func(context.Context) error { return nil }},
		{name: "schema sync", stop: func(context.Context) error { return nil }},
	}

	err := shutdown(ctx, log.GetLogger(),
		func(context.Context) error { return context.DeadlineExceeded },
		workerList,
		func() error { closed.Store(true); return nil })

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected the HTTP failure to reach the caller, got %v", err)
	}
	if !closed.Load() {
		t.Error("expected the pool to close even when the HTTP server did not stop")
	}
}
