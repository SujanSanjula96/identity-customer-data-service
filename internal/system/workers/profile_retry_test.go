/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
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
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

// errLockNotGranted stands for a transient database failure, such as a lock that was not granted in time.
var errLockNotGranted = fmt.Errorf("query CDS-PRF-26 failed: %w", driver.ErrBadConn)

// enqueueRecorder is a profile queue that records what is enqueued, and fails to enqueue with err.
type enqueueRecorder struct {
	enqueued []string
	err      error
}

func (q *enqueueRecorder) Enqueue(profile profileModel.Profile) error {
	if q.err != nil {
		return q.err
	}
	q.enqueued = append(q.enqueued, profile.ProfileID)
	return nil
}

func (q *enqueueRecorder) Start(func(profileModel.Profile) error, int) error { return nil }

func (q *enqueueRecorder) Close(context.Context) error { return nil }

// retryWorker sets up a worker whose identity resolution is resolve, with short retry pauses, and
// returns its database and the queue message of a profile to unify.
func retryWorker(t *testing.T, q *enqueueRecorder,
	resolve func(ctx context.Context, orgID string, profile profileModel.Profile) error) (
	*sql.DB, profileModel.Profile) {
	t.Helper()
	db := candidateWorkerDB(t)
	require.NoError(t, setup.InsertTestOrganization(db, tenantOrg.ID, "worker-tenant"))
	workerProfile(t, db, "incoming", "", `{"email":"a"}`, 0)

	previousResolve, previousDelays := fuzzyResolveFunc, unificationRetryDelays
	RegisterFuzzyResolveFunc(resolve)
	unificationRetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	profileQueueMu.Lock()
	activeProfileQueue = q
	profileQueueMu.Unlock()
	t.Cleanup(func() {
		fuzzyResolveFunc, unificationRetryDelays = previousResolve, previousDelays
		profileQueueMu.Lock()
		activeProfileQueue = nil
		profileQueueMu.Unlock()
	})
	return db, profileModel.Profile{ProfileID: "incoming", OrgID: tenantOrg.ID}
}

func TestUnificationThatFailsOnALockIsTriedAgain(t *testing.T) {
	var resolved []string
	q := &enqueueRecorder{}
	_, job := retryWorker(t, q, func(_ context.Context, _ string, p profileModel.Profile) error {
		resolved = append(resolved, p.ProfileID)
		if len(resolved) < 3 {
			return errLockNotGranted
		}
		return nil
	})

	require.NoError(t, runProfileJob(newJobLifecycle(), newProfileJobs(), job))

	require.Equal(t, []string{"incoming", "incoming", "incoming"}, resolved)
	require.Empty(t, q.enqueued)
}

func TestUnificationThatFailsOtherwiseIsNotTriedAgain(t *testing.T) {
	calls := 0
	q := &enqueueRecorder{}
	_, job := retryWorker(t, q, func(context.Context, string, profileModel.Profile) error {
		calls++
		return errors.New("the profile cannot be unified")
	})

	require.NoError(t, runProfileJob(newJobLifecycle(), newProfileJobs(), job))

	require.Equal(t, 1, calls)
	require.Empty(t, q.enqueued)
}

// TestUnificationThatKeepsFailingOnALockIsQueuedAgain covers a database that stays locked for longer
// than the retries last. The profile goes to the back of the queue rather than being lost.
func TestUnificationThatKeepsFailingOnALockIsQueuedAgain(t *testing.T) {
	calls := 0
	q := &enqueueRecorder{}
	_, job := retryWorker(t, q, func(context.Context, string, profileModel.Profile) error {
		calls++
		return errLockNotGranted
	})

	require.NoError(t, runProfileJob(newJobLifecycle(), newProfileJobs(), job))

	require.Equal(t, len(unificationRetryDelays)+1, calls)
	require.Equal(t, []string{"incoming"}, q.enqueued)
}

// TestUnificationThatCannotBeQueuedAgainIsNotAcknowledged covers a queue that refuses the profile. The
// job reports the failure, so a broker delivers the message again.
func TestUnificationThatCannotBeQueuedAgainIsNotAcknowledged(t *testing.T) {
	q := &enqueueRecorder{err: errors.New("the broker is unreachable")}
	_, job := retryWorker(t, q, func(context.Context, string, profileModel.Profile) error {
		return errLockNotGranted
	})

	err := runProfileJob(newJobLifecycle(), newProfileJobs(), job)

	require.ErrorIs(t, err, driver.ErrBadConn)
}

// TestRetryOfAProfileMergedByTheFailedAttemptUnifiesItsMaster covers an attempt that merged the profile
// and then failed. The profile is a child now and would be skipped, so the retry evaluates its master,
// which holds its data.
func TestRetryOfAProfileMergedByTheFailedAttemptUnifiesItsMaster(t *testing.T) {
	var resolved []string
	var db *sql.DB
	db, job := retryWorker(t, &enqueueRecorder{}, func(_ context.Context, _ string, p profileModel.Profile) error {
		resolved = append(resolved, p.ProfileID)
		if len(resolved) == 1 {
			_, err := db.Exec(`UPDATE profile_reference SET profile_status = 'MERGED_TO',
				reference_profile_id = 'master' WHERE profile_id = 'incoming'`)
			require.NoError(t, err)
			return errLockNotGranted
		}
		return nil
	})
	workerProfile(t, db, "master", "", `{"email":"a"}`, time.Hour)

	require.NoError(t, runProfileJob(newJobLifecycle(), newProfileJobs(), job))

	require.Equal(t, []string{"incoming", "master"}, resolved)
}
