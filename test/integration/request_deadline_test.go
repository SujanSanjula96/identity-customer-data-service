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

package integration

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	healthHandler "github.com/wso2/identity-customer-data-service/internal/health_check/handler"
	healthService "github.com/wso2/identity-customer-data-service/internal/health_check/service"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	_ "modernc.org/sqlite"
)

// useSaturatedPool points the stores at a pool that holds one connection, and
// takes that connection with an open transaction. Every call then waits, which
// is the state a bounded pool reaches under load. The suite datasource is
// restored when the test ends.
func useSaturatedPool(t *testing.T) {

	t.Helper()

	path := filepath.Join(t.TempDir(), "saturated.db")
	db, err := sql.Open(database.DriverSQLite, path+"?"+database.DefaultSQLiteOptions)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, db.Ping())

	holder, err := db.Begin()
	require.NoError(t, err)

	provider.SetTestDB(db, database.TypeSQLite)
	t.Cleanup(func() {
		provider.SetTestDB(suiteDB, suiteDBType)
		_ = holder.Rollback()
		_ = db.Close()
	})
}

// Test_RequestDeadlineReachesTheDatabase follows one deadline from the HTTP
// handler, through the service, to the pool. The pool has no free connection,
// so the call can only end on the deadline the request carries.
func Test_RequestDeadlineReachesTheDatabase(t *testing.T) {

	useSaturatedPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	request := httptest.NewRequest(http.MethodGet, "/ready", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()

	start := time.Now()
	healthHandler.NewHealthHandler().HandleReadiness(recorder, request)
	elapsed := time.Since(start)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.GreaterOrEqual(t, elapsed, 250*time.Millisecond,
		"the handler answered before the deadline, so it never reached the pool")
	require.Less(t, elapsed, 10*time.Second,
		"the deadline did not end the wait for a connection")
}

// Test_ServiceDeadlineReachesTheDatabase is the same check one layer down, for
// the callers that reach a service without an HTTP request.
func Test_ServiceDeadlineReachesTheDatabase(t *testing.T) {

	useSaturatedPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := healthService.GetHealthCheckService().CheckReadiness(ctx)
	elapsed := time.Since(start)

	require.Error(t, err)
	require.GreaterOrEqual(t, elapsed, 250*time.Millisecond)
	require.Less(t, elapsed, 10*time.Second)
}

// Test_RequestCancellationReachesTheDatabase covers the client that goes away
// while the call waits for a connection. The wait must end with it.
func Test_RequestCancellationReachesTheDatabase(t *testing.T) {

	useSaturatedPool(t)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	request := httptest.NewRequest(http.MethodGet, "/ready", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()

	start := time.Now()
	healthHandler.NewHealthHandler().HandleReadiness(recorder, request)
	elapsed := time.Since(start)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Less(t, elapsed, 10*time.Second,
		"the cancel did not end the wait for a connection")
}
