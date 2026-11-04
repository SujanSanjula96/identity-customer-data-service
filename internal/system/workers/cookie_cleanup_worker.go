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

	"github.com/wso2/identity-customer-data-service/internal/system/utils"
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
	// waits on it before the database pool closes.
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
		defer utils.RecoverPanic("cookie cleanup worker")
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
// The sweep is cancelled at once, because it deletes in batches and the next
// start continues where it stopped.
//
// It returns ErrShutdownIncomplete when ctx expires first, and it is safe to
// call more than once.
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

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ErrShutdownIncomplete
	}
}

// runCookieCleanup deletes the inactive cookie records in batches. The sweep
// runs under the worker's context, so it stops when the worker stops.
func runCookieCleanup(ctx context.Context, batchSize int) {

	logger := log.GetLogger()
	total := 0

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
