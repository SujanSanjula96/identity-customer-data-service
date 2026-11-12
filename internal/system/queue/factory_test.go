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
package queue

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/queue/inmemory"
)

func configOfType(queueType string) config.Config {
	return config.Config{MessageQueue: config.MessageQueueConfig{Type: queueType}}
}

func TestTheMemoryQueueIsTheDefault(t *testing.T) {
	for _, queueType := range []string{"", TypeMemory} {
		profiles, err := NewProfileUnificationQueue(configOfType(queueType))
		require.NoError(t, err)
		assert.IsType(t, &inmemory.ProfileQueue{}, profiles)

		jobs, err := NewSchemaSyncQueue(configOfType(queueType))
		require.NoError(t, err)
		assert.IsType(t, &inmemory.SchemaSyncQueue{}, jobs)
	}
}

func TestAnUnknownProviderIsRefused(t *testing.T) {
	_, err := NewProfileUnificationQueue(configOfType("carrier-pigeon"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "carrier-pigeon")

	_, err = NewSchemaSyncQueue(configOfType("carrier-pigeon"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "carrier-pigeon")
}

type fakeProfileQueue struct{}

func (fakeProfileQueue) Enqueue(profileModel.Profile) error     { return nil }
func (fakeProfileQueue) Start(func(profileModel.Profile)) error { return nil }
func (fakeProfileQueue) Close(context.Context) error            { return nil }

type fakeSchemaQueue struct{}

func (fakeSchemaQueue) Enqueue(schemaModel.ProfileSchemaSync) error     { return nil }
func (fakeSchemaQueue) Start(func(schemaModel.ProfileSchemaSync)) error { return nil }
func (fakeSchemaQueue) Close(context.Context) error                     { return nil }

func TestARegisteredProviderIsUsedWithTheBrokerAndTLSConfig(t *testing.T) {
	var gotBroker config.ExternalBrokerConfig
	var gotTLS config.TLSConfig
	RegisterProfileQueueProvider("fake", func(cfg config.ExternalBrokerConfig, tlsCfg config.TLSConfig) (
		ProfileUnificationQueue, error) {
		gotBroker, gotTLS = cfg, tlsCfg
		return fakeProfileQueue{}, nil
	})
	RegisterSchemaSyncQueueProvider("fake", func(config.ExternalBrokerConfig, config.TLSConfig) (SchemaSyncQueue,
		error) {
		return fakeSchemaQueue{}, nil
	})
	RegisterSchemaSyncQueueProvider("broken", func(config.ExternalBrokerConfig, config.TLSConfig) (SchemaSyncQueue,
		error) {
		return nil, errors.New("the broker is not reachable")
	})
	conf := configOfType("fake")
	conf.MessageQueue.Broker.Addr = "broker.example.com:61613"
	conf.TLS.CertDir = "/certs"

	profiles, err := NewProfileUnificationQueue(conf)
	require.NoError(t, err)
	assert.Equal(t, fakeProfileQueue{}, profiles)
	assert.Equal(t, "broker.example.com:61613", gotBroker.Addr)
	assert.Equal(t, "/certs", gotTLS.CertDir)

	jobs, err := NewSchemaSyncQueue(conf)
	require.NoError(t, err)
	assert.Equal(t, fakeSchemaQueue{}, jobs)

	_, err = NewSchemaSyncQueue(configOfType("broken"))
	assert.EqualError(t, err, "the broker is not reachable")
}
