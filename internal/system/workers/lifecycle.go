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
	"fmt"
	"sync"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// jobLifecycle bounds the work a queue consumer does, and the wait for that
// work at shutdown.
//
// Stop used to close the queue and return at once. Closing a queue stops
// intake, but the consumer keeps calling the handler for whatever is already
// buffered, and every job built its context from context.Background(), which
// shutdown never reaches. The database pool was therefore closed while a merge
// or a schema update was half done.
//
// The lifecycle gives shutdown three things: a way to stop new jobs, a count
// of the jobs that run, and a context that ends them when the wait runs out.
type jobLifecycle struct {
	// ctx is the parent of every job context. It is cancelled only when the
	// wait at shutdown runs out of time.
	ctx    context.Context
	cancel context.CancelFunc

	// mu pairs draining with running.Add, so that no job can register after
	// stop has decided to wait.
	mu       sync.Mutex
	draining bool
	running  sync.WaitGroup

	// budget bounds the wait for the jobs that are already running.
	budget time.Duration
}

// newJobLifecycle returns a lifecycle for one worker.
func newJobLifecycle() *jobLifecycle {

	ctx, cancel := context.WithCancel(context.Background())
	return &jobLifecycle{
		ctx:    ctx,
		cancel: cancel,
		budget: constants.WorkerShutdownTimeout,
	}
}

// begin registers a job. It reports false once shutdown has started, and the
// job must then not run.
func (l *jobLifecycle) begin() bool {

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.draining {
		return false
	}
	l.running.Add(1)
	return true
}

// run executes one job under a context that is certain to end, and that
// shutdown waits for.
//
// A job that arrives after shutdown started is dropped rather than run against
// a pool that is about to close. The in-memory queue loses that message, which
// it does anyway when the process stops. A broker redelivers it.
func (l *jobLifecycle) run(work func(ctx context.Context)) {

	if !l.begin() {
		return
	}
	defer l.running.Done()

	// One message is one unit of work, so it carries its own deadline.
	ctx, cancel := context.WithTimeout(l.ctx, constants.WorkerJobTimeout)
	defer cancel()

	work(ctx)
}

// stop ends the worker and returns only when no job is running.
//
// The order matters. Intake stops first, so nothing new arrives. Jobs that
// have not started are dropped. The jobs that are running keep their context,
// because a merge cut in half is worse than a slow shutdown, and they are given
// the budget to finish. Only when the budget runs out are they cancelled, and
// their transactions then roll back.
//
// The caller closes the database pool after this returns.
func (l *jobLifecycle) stop(closeQueue func() error) error {

	l.mu.Lock()
	l.draining = true
	l.mu.Unlock()

	closeErr := closeQueue()

	if waitWithin(&l.running, l.budget) {
		l.cancel()
		return closeErr
	}

	// Out of time. End the jobs that are still running and wait for them to
	// unwind, so that the pool is not closed under them.
	l.cancel()
	l.running.Wait()

	if closeErr != nil {
		return closeErr
	}
	return fmt.Errorf("workers: a job did not finish within %s and was cancelled", l.budget)
}

// waitWithin reports whether the group finished within the timeout.
func waitWithin(group *sync.WaitGroup, timeout time.Duration) bool {

	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
