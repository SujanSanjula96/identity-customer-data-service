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
package inmemory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case item := <-ch:
		return item
	case <-time.After(5 * time.Second):
		t.Fatal("the handler was not called")
		var zero T
		return zero
	}
}

func TestProfileQueueDeliversEveryProfileInOrder(t *testing.T) {
	queue := NewProfileQueue(10)
	received := make(chan profileModel.Profile, 10)
	require.NoError(t, queue.Start(func(p profileModel.Profile) { received <- p }))

	for _, id := range []string{"p-1", "p-2", "p-3"} {
		require.NoError(t, queue.Enqueue(profileModel.Profile{ProfileId: id}))
	}

	for _, id := range []string{"p-1", "p-2", "p-3"} {
		assert.Equal(t, id, receive(t, received).ProfileId)
	}
	require.NoError(t, queue.Close(context.Background()))
}

func TestProfileQueueDropsAProfileWhenFull(t *testing.T) {
	queue := NewProfileQueue(1)

	require.NoError(t, queue.Enqueue(profileModel.Profile{ProfileId: "p-1"}))
	err := queue.Enqueue(profileModel.Profile{ProfileId: "p-2"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "full")
	assert.Contains(t, err.Error(), "p-2")
}

func TestProfileQueueRefusesAProfileOnceClosed(t *testing.T) {
	queue := NewProfileQueue(1)
	require.NoError(t, queue.Close(context.Background()))

	err := queue.Enqueue(profileModel.Profile{ProfileId: "p-1"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "closed")
}

func TestProfileQueueCanBeClosedMoreThanOnce(t *testing.T) {
	queue := NewProfileQueue(1)

	assert.NoError(t, queue.Close(context.Background()))
	assert.NoError(t, queue.Close(context.Background()))
}

func TestProfileQueueKeepsDrainingAfterAHandlerPanics(t *testing.T) {
	// A recovered panic is logged, so the logger has to exist.
	require.NoError(t, log.Init("ERROR"))
	queue := NewProfileQueue(10)
	var once sync.Once
	received := make(chan string, 10)
	require.NoError(t, queue.Start(func(p profileModel.Profile) {
		once.Do(func() { panic("a bad profile") })
		received <- p.ProfileId
	}))

	require.NoError(t, queue.Enqueue(profileModel.Profile{ProfileId: "bad"}))
	require.NoError(t, queue.Enqueue(profileModel.Profile{ProfileId: "good"}))

	assert.Equal(t, "good", receive(t, received))
	require.NoError(t, queue.Close(context.Background()))
}

func TestSchemaSyncQueueDeliversEveryJob(t *testing.T) {
	queue := NewSchemaSyncQueue(10)
	received := make(chan schemaModel.ProfileSchemaSync, 10)
	require.NoError(t, queue.Start(func(s schemaModel.ProfileSchemaSync) { received <- s }))

	require.NoError(t, queue.Enqueue(schemaModel.ProfileSchemaSync{OrgHandle: "acme",
		Event: "POST_ADD_EXTERNAL_CLAIM"}))
	require.NoError(t, queue.Enqueue(schemaModel.ProfileSchemaSync{OrgHandle: "beta",
		Event: "POST_DELETE_EXTERNAL_CLAIM"}))

	first := receive(t, received)
	second := receive(t, received)
	assert.Equal(t, "acme", first.OrgHandle)
	assert.Equal(t, "POST_ADD_EXTERNAL_CLAIM", first.Event)
	assert.Equal(t, "beta", second.OrgHandle)
	require.NoError(t, queue.Close(context.Background()))
}

func TestSchemaSyncQueueDropsAJobWhenFullOrClosed(t *testing.T) {
	queue := NewSchemaSyncQueue(1)

	require.NoError(t, queue.Enqueue(schemaModel.ProfileSchemaSync{OrgHandle: "acme"}))
	full := queue.Enqueue(schemaModel.ProfileSchemaSync{OrgHandle: "beta"})
	require.Error(t, full)
	assert.Contains(t, full.Error(), "full")

	require.NoError(t, queue.Close(context.Background()))
	require.NoError(t, queue.Close(context.Background()), "closing twice is fine")
	closed := queue.Enqueue(schemaModel.ProfileSchemaSync{OrgHandle: "gamma"})
	require.Error(t, closed)
	assert.Contains(t, closed.Error(), "closed")
}
