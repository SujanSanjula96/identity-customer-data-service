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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	schemaStore "github.com/wso2/identity-customer-data-service/internal/profile_schema/store"
	cdserrors "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

// Test_StoreStopsOnACancelledContext checks that the context a handler passes
// reaches the database call. A caller that goes away must not leave the store
// at work, and must not hold a connection from the bounded pool.
func Test_StoreStopsOnACancelledContext(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := profileStore.GetProfile(ctx, "any-profile-id", "any-org")
	require.Error(t, err)
	requireCauseIs(t, err, context.Canceled)
	require.Less(t, time.Since(start), 5*time.Second)
}

// Test_StoreStopsOnAnExpiredContext covers the deadline, which is how a
// background job bounds its own work.
func Test_StoreStopsOnAnExpiredContext(t *testing.T) {

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)

	_, err := schemaStore.GetProfileSchemaAttributesForOrg(ctx, "any-org")
	require.Error(t, err)
	requireCauseIs(t, err, context.DeadlineExceeded)
}

// Test_TransactionStopsOnACancelledContext covers the transaction path, which
// holds its connection until it ends.
func Test_TransactionStopsOnACancelledContext(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := profileStore.UpdateProfileConsents(ctx, "any-profile-id", nil)
	require.Error(t, err)
	requireCauseIs(t, err, context.Canceled)
}

// requireCauseIs checks that the cause of a store failure is target.
//
// A store wraps its cause in a ServerError, which carries it in a field rather
// than in the error chain, so errors.Is on the result alone would not find it.
// The chain below that field is intact, because every layer under it wraps
// with %w.
func requireCauseIs(t *testing.T, err error, target error) {

	t.Helper()

	var serverErr *cdserrors.ServerError
	require.ErrorAs(t, err, &serverErr)
	require.ErrorIs(t, serverErr.Err, target)
}
