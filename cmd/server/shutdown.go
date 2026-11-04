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
	"fmt"
	"sync"

	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"
)

// namedWorker is one background worker that shutdown stops.
type namedWorker struct {
	name string
	stop func(context.Context) error
}

// shutdown ends the process in one order: the HTTP server stops taking
// requests, every worker finishes the jobs it holds, and the shared database
// pool closes last. Every step shares ctx.
//
// It reports nil when the HTTP server and every worker stopped on their own,
// and joins the failures otherwise. workers.ErrShutdownIncomplete says that the
// deadline passed with work still running. The pool closes either way.
func shutdown(ctx context.Context, logger *log.Logger, httpShutdown func(context.Context) error,
	workerList []namedWorker, closeDB func() error) error {

	// Stop taking requests first, so no new work reaches the workers.
	var stopErr error
	if err := httpShutdown(ctx); err != nil {
		logger.Error("HTTP server shutdown error.", log.Error(err))
		stopErr = fmt.Errorf("http: %w", err)
	}

	// The workers do not depend on each other, so they stop at the same time.
	results := make([]error, len(workerList))
	var stopping sync.WaitGroup
	for i, worker := range workerList {
		stopping.Add(1)
		go func(i int, worker namedWorker) {
			defer stopping.Done()
			results[i] = worker.stop(ctx)
		}(i, worker)
	}
	stopping.Wait()

	for i, err := range results {
		if err == nil {
			continue
		}
		logger.Error(fmt.Sprintf("The %s worker did not stop cleanly.", workerList[i].name), log.Error(err))
		stopErr = errors.Join(stopErr, fmt.Errorf("%s: %w", workerList[i].name, err))
	}

	switch {
	case errors.Is(stopErr, workers.ErrShutdownIncomplete):
		logger.Error("The shutdown deadline passed while a job was still running. The jobs were " +
			"cancelled, their work did not complete, and the database pool closes anyway.")
	case stopErr == nil:
		logger.Info("Every worker stopped, so the database pool closes now.")
	}

	if err := closeDB(); err != nil {
		logger.Error("Failed to close the database connections.", log.Error(err))
		stopErr = errors.Join(stopErr, err)
	}

	return stopErr
}
