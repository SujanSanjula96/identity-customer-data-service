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

// Package inmemory provides in-memory (buffered channel) implementations of
// the queue.ProfileUnificationQueue and queue.SchemaSyncQueue interfaces.
// This is the default provider and is suitable for single-instance, local,
// and development deployments.
package inmemory

import (
	"fmt"
	"log"
	"sync"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
)

// -----------------------------------------------------------------------
// ProfileQueue
// -----------------------------------------------------------------------

// ProfileQueue is the in-memory implementation of queue.ProfileUnificationQueue.
// It uses a buffered Go channel as the underlying queue. The mu/closed fields
// synchronize Enqueue and Close to prevent sending on a closed channel.
type ProfileQueue struct {
	ch        chan profileModel.Profile
	closeOnce sync.Once
	mu        sync.RWMutex
	closed    bool
}

// NewProfileQueue creates a new ProfileQueue with the given buffer size.
func NewProfileQueue(size int) *ProfileQueue {
	return &ProfileQueue{ch: make(chan profileModel.Profile, size)}
}

// Enqueue adds a profile to the in-memory channel. It is non-blocking: if
// the channel is full or closed the item is dropped and an error is returned.
func (q *ProfileQueue) Enqueue(profile profileModel.Profile) error {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		return fmt.Errorf("inmemory: profile queue closed, dropping profile %s", profile.ProfileId)
	}
	select {
	case q.ch <- profile:
		return nil
	default:
		return fmt.Errorf("inmemory: profile queue full, dropping profile %s", profile.ProfileId)
	}
}

// Start launches a goroutine that reads profiles from the channel and
// forwards each one to handler. The goroutine runs until the channel is
// closed. Always returns nil.
func (q *ProfileQueue) Start(handler func(profileModel.Profile) error) error {
	go func() {
		for profile := range q.ch {
			// An item the handler refuses is lost. This queue holds its items
			// in the memory of one process, so it cannot redeliver them and it
			// does not survive a restart. A deployment that must not lose an
			// item runs a broker instead.
			if err := handler(profile); err != nil {
				log.Printf("inmemory: dropping an item the handler did not process: %v", err)
			}
		}
	}()
	return nil
}

// Close marks the queue as closed and closes the underlying channel, which
// causes the consumer goroutine started by Start to exit. It is safe to call
// Close more than once.
func (q *ProfileQueue) Close() error {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.closeOnce.Do(func() { close(q.ch) })
	return nil
}

// -----------------------------------------------------------------------
// SchemaSyncQueue
// -----------------------------------------------------------------------

// SchemaSyncQueue is the in-memory implementation of queue.SchemaSyncQueue.
// It uses a buffered Go channel as the underlying queue. The mu/closed fields
// synchronize Enqueue and Close to prevent sending on a closed channel.
type SchemaSyncQueue struct {
	ch        chan schemaModel.ProfileSchemaSync
	closeOnce sync.Once
	mu        sync.RWMutex
	closed    bool
}

// NewSchemaSyncQueue creates a new SchemaSyncQueue with the given buffer size.
func NewSchemaSyncQueue(size int) *SchemaSyncQueue {
	return &SchemaSyncQueue{ch: make(chan schemaModel.ProfileSchemaSync, size)}
}

// Enqueue adds a schema sync job to the in-memory channel. It is non-blocking:
// if the channel is full or closed the item is dropped and an error is returned.
func (q *SchemaSyncQueue) Enqueue(sync schemaModel.ProfileSchemaSync) error {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		return fmt.Errorf("inmemory: schema sync queue closed, dropping job for tenant %s", sync.OrgId)
	}
	select {
	case q.ch <- sync:
		return nil
	default:
		return fmt.Errorf("inmemory: schema sync queue full, dropping job for tenant %s", sync.OrgId)
	}
}

// Start launches a goroutine that reads schema sync jobs from the channel and
// forwards each one to handler. The goroutine runs until the channel is
// closed. Always returns nil.
func (q *SchemaSyncQueue) Start(handler func(schemaModel.ProfileSchemaSync) error) error {
	go func() {
		for sync := range q.ch {
			// An item the handler refuses is lost. This queue holds its items
			// in the memory of one process, so it cannot redeliver them and it
			// does not survive a restart. A deployment that must not lose an
			// item runs a broker instead.
			if err := handler(sync); err != nil {
				log.Printf("inmemory: dropping an item the handler did not process: %v", err)
			}
		}
	}()
	return nil
}

// Close marks the queue as closed and closes the underlying channel, which
// causes the consumer goroutine started by Start to exit. It is safe to call
// Close more than once.
func (q *SchemaSyncQueue) Close() error {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.closeOnce.Do(func() { close(q.ch) })
	return nil
}
