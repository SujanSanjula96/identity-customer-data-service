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

package provider

import (
	"database/sql"
	"sync"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database/client"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
)

// concurrentCallers is how many goroutines each test releases at once. It is
// well above the core count of a CI runner, so the goroutines really do
// overlap.
const concurrentCallers = 32

// atOnce runs work in concurrentCallers goroutines and releases them together.
// A barrier, rather than a plain loop, is what makes the first calls
// simultaneous.
func atOnce(work func(index int)) {

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)

	for i := 0; i < concurrentCallers; i++ {
		done.Add(1)
		go func(index int) {
			defer done.Done()
			start.Wait()
			work(index)
		}(i)
	}

	start.Done()
	done.Wait()
}

// Test_getSQLiteDB_opensOnePoolUnderConcurrency checks that simultaneous first
// calls build exactly one pool. The earlier test called the function twice in
// a row, which proves only that the second call reuses what the first left.
//
// Pointer equality across every caller is the count. A caller either returns
// the published handle or builds a pool and publishes it. A second build would
// replace the first, so the caller that holds the first would report a
// different pool. One pointer for everybody therefore means one pool.
//
// The inbuilt datasource stands in for PostgreSQL here. Both go through the
// same mutex and the same publish-last order, and this one needs no server, so
// the test runs in every job. Run it with -race.
func Test_getSQLiteDB_opensOnePoolUnderConcurrency(t *testing.T) {

	config.OverrideCDSRuntime(sqliteDataSource(t))
	isolatePools(t)

	pools := make([]*sql.DB, concurrentCallers)
	failures := make([]error, concurrentCallers)

	atOnce(func(index int) {
		pools[index], failures[index] = getSQLiteDB()
	})

	for i := 0; i < concurrentCallers; i++ {
		if failures[i] != nil {
			t.Fatalf("caller %d failed: %v", i, failures[i])
		}
		if pools[i] == nil {
			t.Fatalf("caller %d got no pool", i)
		}
		if pools[i] != pools[0] {
			t.Fatalf("caller %d got a different pool, so more than one was opened", i)
		}
	}

	dbMu.Lock()
	published := sqliteHandle
	dbMu.Unlock()

	if published != pools[0] {
		t.Error("expected the published handle to be the pool every caller holds")
	}
	if err := pools[0].Ping(); err != nil {
		t.Errorf("expected the one pool to answer: %v", err)
	}
}

// Test_getPostgresDB_concurrentFailuresPublishNoHandle checks that simultaneous
// failures leave the process as they found it.
//
// A failed attempt must not be cached, and it must not publish a pool nobody
// can reach. The port refuses the connection, so each attempt ends at once.
// Run it with -race.
func Test_getPostgresDB_concurrentFailuresPublishNoHandle(t *testing.T) {

	config.OverrideCDSRuntime(unreachableDataSource("127.0.0.1", refusedPort(t), 1))
	isolatePools(t)

	pools := make([]*sql.DB, concurrentCallers)
	failures := make([]error, concurrentCallers)

	atOnce(func(index int) {
		pools[index], failures[index] = getPostgresDB()
	})

	for i := 0; i < concurrentCallers; i++ {
		if failures[i] == nil {
			t.Errorf("caller %d expected an error from a port that refuses the connection", i)
		}
		if pools[i] != nil {
			t.Errorf("caller %d got a pool from a failed attempt", i)
		}
	}

	dbMu.Lock()
	published := postgresHandle
	dbMu.Unlock()

	if published != nil {
		t.Error("expected no handle after concurrent failures")
	}

	// The failures are not cached, so a call after them still tries.
	if _, err := getPostgresDB(); err == nil {
		t.Error("expected a later call to try again and fail again")
	}
}

// Test_GetDBClient_sharesOnePoolUnderConcurrency runs the whole path a store
// takes: ask the provider for a client, run a statement, close the client.
//
// Every client must work, and the process must still hold exactly one pool
// when they are done. Run it with -race.
func Test_GetDBClient_sharesOnePoolUnderConcurrency(t *testing.T) {

	config.OverrideCDSRuntime(sqliteDataSource(t))
	isolatePools(t)

	ping := model.DBQuery{ID: "PROVIDER-CONCURRENCY-01", Query: "SELECT 1"}
	dbProvider := NewDBProvider()

	clients := make([]client.DBClientInterface, concurrentCallers)
	failures := make([]error, concurrentCallers)

	atOnce(func(index int) {
		dbClient, err := dbProvider.GetDBClient()
		if err != nil {
			failures[index] = err
			return
		}
		clients[index] = dbClient
		// Every store does exactly this.
		defer func() { _ = dbClient.Close() }()

		_, failures[index] = dbClient.ExecuteQuery(ping)
	})

	for i := 0; i < concurrentCallers; i++ {
		if failures[i] != nil {
			t.Errorf("client %d failed: %v", i, failures[i])
		}
		if clients[i] == nil {
			t.Errorf("client %d was never built", i)
		}
	}

	dbMu.Lock()
	published := sqliteHandle
	dbMu.Unlock()

	if published == nil {
		t.Fatal("expected the process to hold a pool")
	}
	// Every client closed, and the pool is still the one the process opened.
	if err := published.Ping(); err != nil {
		t.Errorf("expected the shared pool to outlive every client: %v", err)
	}
}
