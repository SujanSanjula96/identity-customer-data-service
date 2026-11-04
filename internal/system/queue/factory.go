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
	"fmt"
	"sync"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/queue/inmemory"
)

// ProfileQueueProvider is the constructor signature for a
// ProfileUnificationQueue provider. It receives the broker config and the
// system TLS config (used to build the CA pool for SSL connections).
type ProfileQueueProvider func(cfg config.ExternalBrokerConfig, tlsCfg config.TLSConfig) (ProfileUnificationQueue, error)

// SchemaSyncQueueProvider is the constructor signature for a SchemaSyncQueue
// provider. It receives the broker config and the system TLS config (used to
// build the CA pool for SSL connections).
type SchemaSyncQueueProvider func(cfg config.ExternalBrokerConfig, tlsCfg config.TLSConfig) (SchemaSyncQueue, error)

var (
	mu                       sync.RWMutex
	profileQueueProviders    = map[string]ProfileQueueProvider{}
	schemaSyncQueueProviders = map[string]SchemaSyncQueueProvider{}
)

// RegisterProfileQueueProvider registers a ProfileQueueProvider under the
// given name. Call this inside an init() function in your provider package so
// the provider is available as soon as the package is imported.
//
// Example:
//
//	func init() {
//	    queue.RegisterProfileQueueProvider("myprovider", func(cfg config.ExternalBrokerConfig) (queue.ProfileUnificationQueue, error) {
//	        return newMyProviderQueue(cfg)
//	    })
//	}
func RegisterProfileQueueProvider(name string, p ProfileQueueProvider) {
	mu.Lock()
	defer mu.Unlock()
	profileQueueProviders[name] = p
}

// RegisterSchemaSyncQueueProvider registers a SchemaSyncQueueProvider under
// the given name. Call this inside an init() function in your provider
// package so the provider is available as soon as the package is imported.
func RegisterSchemaSyncQueueProvider(name string, p SchemaSyncQueueProvider) {
	mu.Lock()
	defer mu.Unlock()
	schemaSyncQueueProviders[name] = p
}

// NewProfileUnificationQueue returns the ProfileUnificationQueue for the
// provider named in cfg.Type. When the type is empty or "memory" the default
// in-memory provider is returned. For any other type the provider must have
// been registered (e.g. via an init() function) before this call; an error is
// returned if no matching provider is found.
func NewProfileUnificationQueue(cfg config.Config) (ProfileUnificationQueue, error) {
	if cfg.MessageQueue.Type == TypeMemory || cfg.MessageQueue.Type == "" {
		return inmemory.NewProfileQueue(constants.DefaultQueueSize), nil
	}
	mu.RLock()
	p, ok := profileQueueProviders[cfg.MessageQueue.Type]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("queue: unknown profile queue provider %q; "+
			"register it by importing its package (see docs/extending-queue-providers.md)", cfg.MessageQueue.Type)
	}
	return p(cfg.MessageQueue.Broker, cfg.TLS)
}

// NewSchemaSyncQueue returns the SchemaSyncQueue for the provider named in
// cfg.Type. When the type is empty or "memory" the default in-memory provider
// is returned. For any other type the provider must have been registered
// (e.g. via an init() function) before this call; an error is returned if no
// matching provider is found.
func NewSchemaSyncQueue(cfg config.Config) (SchemaSyncQueue, error) {
	if cfg.MessageQueue.Type == TypeMemory || cfg.MessageQueue.Type == "" {
		return inmemory.NewSchemaSyncQueue(constants.DefaultQueueSize), nil
	}
	mu.RLock()
	p, ok := schemaSyncQueueProviders[cfg.MessageQueue.Type]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("queue: unknown schema sync queue provider %q; "+
			"register it by importing its package (see docs/extending-queue-providers.md)", cfg.MessageQueue.Type)
	}
	return p(cfg.MessageQueue.Broker, cfg.TLS)
}
