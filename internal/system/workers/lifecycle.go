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
	"sync"
)

// jobLifecycle tracks the jobs one queue consumer runs. It lets shutdown stop
// new jobs, wait for the jobs that are already at work, and end them when the
// deadline passes.
type jobLifecycle struct {
	// ctx is the parent of every job context. It is cancelled only when the
	// shutdown deadline passes.
	ctx    context.Context
	cancel context.CancelFunc

	// mu pairs stopping with active.Add, so that no job can register after
	// stop has decided to wait.
	mu       sync.Mutex
	stopping bool
	active   sync.WaitGroup
}

// ErrWorkerStopping says the job did not run because the worker is stopping.
var ErrWorkerStopping = errors.New("workers: the job did not run because the worker is stopping")

// ErrShutdownIncomplete says the shutdown deadline passed while a handler was
// still running. The job contexts are cancelled and the work did not complete.
var ErrShutdownIncomplete = errors.New("workers: the shutdown deadline passed while a job was still running")

// newJobLifecycle returns a lifecycle for one worker.
func newJobLifecycle() *jobLifecycle {

	ctx, cancel := context.WithCancel(context.Background())
	return &jobLifecycle{ctx: ctx, cancel: cancel}
}

// begin registers a job. It reports false once the worker has started to stop,
// and the job must then not run.
func (l *jobLifecycle) begin() bool {

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.stopping {
		return false
	}
	l.active.Add(1)
	return true
}

// run executes one job under the worker context and counts it as active until
// the handler returns. It refuses a job that arrives after the worker has
// started to stop.
func (l *jobLifecycle) run(work func(ctx context.Context)) error {

	if !l.begin() {
		return ErrWorkerStopping
	}
	defer l.active.Done()

	work(l.ctx)
	return nil
}

// stop ends the worker in one order: no further job starts, the jobs that are
// already at work finish, and the queue closes last. It always returns inside
// ctx, and the caller closes the database pool after it returns.
//
// It reports nil when every handler returned on its own, and
// ErrShutdownIncomplete when ctx expired first. The job contexts are cancelled
// in that case.
func (l *jobLifecycle) stop(ctx context.Context, closeQueue func(context.Context) error) error {

	l.mu.Lock()
	l.stopping = true
	l.mu.Unlock()

	finished := waitWithin(ctx, &l.active)
	if !finished {
		// Out of time. End the work that is still running.
		l.cancel()
	}

	closeErr := closeQueue(ctx)

	if !finished {
		return errors.Join(ErrShutdownIncomplete, closeErr)
	}
	l.cancel()
	return closeErr
}

// waitWithin reports whether every member of the group returned before ctx
// expired.
func waitWithin(ctx context.Context, group *sync.WaitGroup) bool {

	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}
