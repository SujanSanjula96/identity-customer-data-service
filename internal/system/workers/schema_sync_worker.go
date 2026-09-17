/*
 * Copyright (c) 2026, WSO2 LLC. (http://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
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

	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/profile_schema/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/queue"
)

// activeSchemaSyncQueue is the queue implementation used for schema
// synchronisation. It is initialised by StartSchemaSyncWorker. All access is
// guarded by schemaSyncQueueMu to prevent data races between concurrent
// Enqueue calls and shutdown.
var (
	schemaSyncQueueMu     sync.RWMutex
	activeSchemaSyncQueue queue.SchemaSyncQueue
)

// StartSchemaSyncWorker initialises the schema sync queue (using the provider
// configured in the runtime config) and starts the consumer goroutine. An
// error is returned when the queue cannot be created or started; the caller
// should treat this as a fatal startup failure.
func StartSchemaSyncWorker() error {
	cfg := config.GetCDSRuntime().Config
	q, err := queue.NewSchemaSyncQueue(cfg)
	if err != nil {
		return fmt.Errorf("workers: failed to create schema sync queue: %w", err)
	}
	if err := q.Start(processSchemaSyncJob); err != nil {
		_ = q.Close()
		return fmt.Errorf("workers: failed to start schema sync queue: %w", err)
	}
	schemaSyncQueueMu.Lock()
	activeSchemaSyncQueue = q
	schemaSyncQueueMu.Unlock()
	return nil
}

// EnqueueSchemaSyncJob adds a schema sync job to the active queue. It is a
// no-op when the worker has not been started or has been stopped. Enqueue
// errors are logged but not propagated, because schema sync is a best-effort
// background task.
func EnqueueSchemaSyncJob(schemaSync model.ProfileSchemaSync) error {
	schemaSyncQueueMu.RLock()
	q := activeSchemaSyncQueue
	schemaSyncQueueMu.RUnlock()
	if q == nil {
		return fmt.Errorf("schema sync queue is not initialized")
	}
	return q.Enqueue(schemaSync)
}

// StopSchemaSyncWorker gracefully shuts down the schema sync queue. It nils
// out the global reference under a write lock before calling Close, ensuring
// no concurrent Enqueue can send on a closed queue. It should be called
// during application shutdown.
func StopSchemaSyncWorker() error {
	schemaSyncQueueMu.Lock()
	q := activeSchemaSyncQueue
	activeSchemaSyncQueue = nil
	schemaSyncQueueMu.Unlock()
	if q != nil {
		return q.Close()
	}
	return nil
}

// processSchemaSyncJob processes a schema sync job
func processSchemaSyncJob(schemaSync model.ProfileSchemaSync) {

	// One message is one unit of work, so it carries its own deadline.
	ctx, cancel := context.WithTimeout(context.Background(), constants.WorkerJobTimeout)
	defer cancel()

	logger := log.GetLogger()
	logger.Info(fmt.Sprintf("Processing schema sync job for tenant: %s, event: %s", schemaSync.OrgId, schemaSync.Event))

	schemaProvider := provider.NewProfileSchemaProvider()
	schemaService := schemaProvider.GetProfileSchemaService()

	err := schemaService.SyncProfileSchema(ctx, schemaSync.OrgId)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to sync profile schema for tenant: %s", schemaSync.OrgId), log.Error(err))
		return
	}

	logger.Info(fmt.Sprintf("Profile schema sync completed successfully for tenant: %s", schemaSync.OrgId))
}
