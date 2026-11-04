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

package queue

import (
	"context"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
)

// Provider type constants used in configuration.
const (
	TypeMemory   = "memory"
	TypeActiveMQ = "activemq"
)

// ProfileUnificationQueue defines the contract for enqueuing profiles for
// asynchronous unification processing.
type ProfileUnificationQueue interface {
	// Enqueue adds a profile to the queue for unification. It returns nil
	// on success or a descriptive error when the item cannot be accepted
	// (e.g. queue full, serialization failure, broker unreachable).
	Enqueue(profile profileModel.Profile) error

	// Start begins consuming queue items and invokes handler for each one.
	// Implementations must start the consumer loop in a separate goroutine
	// so that Start returns immediately. An error is returned when the queue
	// cannot be started (e.g. broker subscription failure).
	Start(handler func(profileModel.Profile)) error

	// Close performs a graceful shutdown of the queue, flushing any
	// in-flight items and releasing underlying resources (connections,
	// channels, goroutines). It is safe to call Close more than once.
	//
	// Close must return inside ctx. A provider whose graceful close has not
	// finished by then ends its connection by force, so that shutdown keeps
	// its deadline and leaves no goroutine behind.
	Close(ctx context.Context) error
}

// SchemaSyncQueue defines the contract for enqueuing schema synchronisation
// jobs for asynchronous processing.
type SchemaSyncQueue interface {
	// Enqueue adds a schema sync job to the queue. It returns nil on
	// success or a descriptive error when the item cannot be accepted
	// (e.g. queue full, serialization failure, broker unreachable).
	Enqueue(sync schemaModel.ProfileSchemaSync) error

	// Start begins consuming queue items and invokes handler for each one.
	// Implementations must start the consumer loop in a separate goroutine
	// so that Start returns immediately. An error is returned when the queue
	// cannot be started (e.g. broker subscription failure).
	Start(handler func(schemaModel.ProfileSchemaSync)) error

	// Close performs a graceful shutdown of the queue, flushing any
	// in-flight items and releasing underlying resources (connections,
	// channels, goroutines). It is safe to call Close more than once.
	//
	// Close must return inside ctx. A provider whose graceful close has not
	// finished by then ends its connection by force, so that shutdown keeps
	// its deadline and leaves no goroutine behind.
	Close(ctx context.Context) error
}
