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

	"github.com/wso2/identity-customer-data-service/internal/profile/store"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

var (
	cookieCleanupMu sync.Mutex
	// cookieCleanupCancel stops the worker and cancels the database work that
	// a sweep has in flight.
	cookieCleanupCancel context.CancelFunc
	// cookieCleanupDone is closed when the worker goroutine returns. Shutdown
	// waits on it, so a sweep that is in flight unwinds before the database
	// pool closes. A sweep deletes in batches and is safe to cut short,
	// because the next start continues where it stopped.
	cookieCleanupDone chan struct{}
)

func StartCookieCleanupWorker(cfg config.CookieCleanupConfig) {

	logger := log.GetLogger()

	if cfg.Interval <= 0 {
		cfg.Interval = constants.DefaultCookieCleanupTime
		logger.Info("Cookie cleanup interval not set or invalid. Defaulting to 24 hours.")
	}

	interval := time.Duration(cfg.Interval) * time.Second
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 500
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	cookieCleanupMu.Lock()
	cookieCleanupCancel = cancel
	cookieCleanupDone = done
	cookieCleanupMu.Unlock()

	logger.Info(fmt.Sprintf("Cookie cleanup worker started. Interval: %s, Batch size: %d",
		interval, batchSize))

	ticker := time.NewTicker(interval)

	go func() {
		defer close(done)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				runCookieCleanup(ctx, batchSize)
			case <-ctx.Done():
				logger.Info("Cookie cleanup worker stopped")
				return
			}
		}
	}()
}

// StopCookieCleanupWorker stops the worker and returns when its goroutine has
// gone, so that the caller can close the database pool.
//
// A sweep deletes in batches and is safe to cut short, because the next start
// continues where it stopped. It is therefore cancelled at once rather than
// given time to finish.
//
// The wait shares the shutdown deadline with every other worker. It reports
// ErrForcedShutdown rather than returning while its goroutine still runs,
// which is the same contract the queue workers follow.
func StopCookieCleanupWorker(ctx context.Context) error {

	cookieCleanupMu.Lock()
	cancel, done := cookieCleanupCancel, cookieCleanupDone
	cookieCleanupCancel, cookieCleanupDone = nil, nil
	cookieCleanupMu.Unlock()

	if cancel == nil {
		return nil
	}
	cancel()

	if done == nil {
		return nil
	}

	timer := time.NewTimer(constants.WorkerShutdownTimeout)
	defer timer.Stop()

	select {
	case <-done:
		return nil
	case <-timer.C:
	case <-ctx.Done():
	}

	return ErrForcedShutdown
}

// runCookieCleanup deletes the inactive cookie records in batches.
//
// The sweep carries its own deadline, so it cannot hold a connection from the
// bounded pool without a limit. It also ends when the worker stops, because its
// context descends from the worker's.
func runCookieCleanup(parent context.Context, batchSize int) {

	logger := log.GetLogger()
	total := 0

	ctx, cancel := context.WithTimeout(parent, constants.CookieCleanupJobTimeout)
	defer cancel()

	for {
		deleted, err := store.DeleteInactiveCookieProfiles(ctx, batchSize)
		if err != nil {
			logger.Debug("Cookie cleanup batch error", log.Error(err))
			break
		}
		total += deleted
		if deleted < batchSize {
			break
		}
	}

	if total > 0 {
		logger.Info(fmt.Sprintf("Cookie cleanup: purged %d inactive records", total))
	}
}
