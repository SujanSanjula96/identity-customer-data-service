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
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adminConfigModel "github.com/wso2/identity-customer-data-service/internal/admin_config/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

type deleteCall struct {
	orgID  string
	cutoff time.Time
	size   int
}

// fakeSource stands in for the database. backlog is how many expired profiles each organization holds.
type fakeSource struct {
	orgIDs      []string
	settings    map[string]adminConfigModel.TemporaryProfileCleanupSettings
	primaries   map[string]string
	backlog     map[string]int
	orgsErr     error
	settingsErr error
	primariesEr error
	failFor     map[string]error
	calls       []deleteCall
}

func (f *fakeSource) OrgIDs(context.Context) ([]string, error) { return f.orgIDs, f.orgsErr }

func (f *fakeSource) Settings(context.Context) (map[string]adminConfigModel.TemporaryProfileCleanupSettings, error) {
	return f.settings, f.settingsErr
}

func (f *fakeSource) Primaries(context.Context) (map[string]string, error) {
	return f.primaries, f.primariesEr
}

func (f *fakeSource) DeleteExpired(_ context.Context, orgID string, cutoff time.Time, size int) (int, error) {
	f.calls = append(f.calls, deleteCall{orgID, cutoff, size})
	if err := f.failFor[orgID]; err != nil {
		return 0, err
	}
	deleted := min(f.backlog[orgID], size)
	f.backlog[orgID] -= deleted
	return deleted, nil
}

// lastRunAt returns a record that each organization was last cleaned at the given time.
func lastRunAt(at time.Time, orgIDs ...string) map[string]time.Time {
	lastRun := map[string]time.Time{}
	for _, orgID := range orgIDs {
		lastRun[orgID] = at
	}
	return lastRun
}

var testNow = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

// daysAgo returns the time that many days before testNow, which is due for any interval up to that long.
func daysAgo(days int) time.Time { return testNow.AddDate(0, 0, -days) }

func TestSettingsForUsesTheOrganizationThenItsPrimaryThenTheDefaults(t *testing.T) {
	stored := map[string]adminConfigModel.TemporaryProfileCleanupSettings{
		"acme":   {RetentionDays: 30, IntervalDays: 6},
		"globex": {RetentionDays: 200},
	}
	primaries := map[string]string{"acme": "acme", "acme-sub": "acme", "globex": "globex", "initech": "initech"}

	assert.Equal(t, cleanupSettings{retentionDays: 30, intervalDays: 6},
		settingsFor("acme", stored, primaries), "an organization uses its own settings")
	assert.Equal(t, cleanupSettings{retentionDays: 30, intervalDays: 6},
		settingsFor("acme-sub", stored, primaries), "a sub organization follows its primary organization")
	assert.Equal(t, cleanupSettings{retentionDays: 200, intervalDays: 30},
		settingsFor("globex", stored, primaries), "a setting that is not stored takes its default")
	assert.Equal(t, cleanupSettings{retentionDays: 90, intervalDays: 30},
		settingsFor("initech", stored, primaries), "an organization with no settings uses the defaults")
	assert.Equal(t, cleanupSettings{retentionDays: 90, intervalDays: 30},
		settingsFor("unregistered", stored, primaries), "an organization that is not stored uses the defaults")
	ownRow := map[string]adminConfigModel.TemporaryProfileCleanupSettings{"acme-sub": {RetentionDays: 45}}
	assert.Equal(t, 90, settingsFor("acme-sub", ownRow, map[string]string{"acme-sub": "acme"}).retentionDays,
		"a sub organization cannot set its own settings, so a row of its own is ignored")
}

func TestAnOrganizationSeenForTheFirstTimeWaitsOneInterval(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	source := &fakeSource{
		orgIDs: []string{"acme"}, primaries: map[string]string{}, backlog: map[string]int{"acme": 3},
	}
	lastRun := map[string]time.Time{}

	total := runTemporaryProfileCleanup(context.Background(), source, lastRun, testNow)

	assert.Zero(t, total)
	assert.Empty(t, source.calls, "nothing is deleted on first sight")
	assert.Equal(t, testNow, lastRun["acme"], "the wait starts now")
}

func TestCleanupWaitsForTheOrganizationsInterval(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	source := &fakeSource{
		orgIDs: []string{"daily", "weekly"},
		settings: map[string]adminConfigModel.TemporaryProfileCleanupSettings{
			"daily":  {IntervalDays: 1},
			"weekly": {IntervalDays: 7},
		},
		primaries: map[string]string{},
		backlog:   map[string]int{"daily": 1, "weekly": 1},
	}
	lastRun := lastRunAt(daysAgo(2), "daily", "weekly")

	total := runTemporaryProfileCleanup(context.Background(), source, lastRun, testNow)

	assert.Equal(t, 1, total)
	require.Len(t, source.calls, 1)
	assert.Equal(t, "daily", source.calls[0].orgID, "only the organization whose interval has passed is cleaned")
	assert.Equal(t, testNow, lastRun["daily"])
	assert.Equal(t, daysAgo(2), lastRun["weekly"], "an organization that was not due keeps its time")
}

func TestACheckJustBeforeTheIntervalIsUpStillCleans(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	source := &fakeSource{orgIDs: []string{"acme"}, primaries: map[string]string{}, backlog: map[string]int{"acme": 1}}
	lastRun := lastRunAt(testNow.Add(-30*24*time.Hour+10*time.Second), "acme")

	total := runTemporaryProfileCleanup(context.Background(), source, lastRun, testNow)

	assert.Equal(t, 1, total, "a tick a few seconds early does not push the cleanup a whole tick later")
}

func TestCleanupAppliesEachOrganizationsSettings(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	source := &fakeSource{
		orgIDs: []string{"acme", "globex"},
		settings: map[string]adminConfigModel.TemporaryProfileCleanupSettings{
			"acme": {RetentionDays: 30},
		},
		primaries: map[string]string{},
		backlog:   map[string]int{"acme": 3, "globex": 2},
	}

	total := runTemporaryProfileCleanup(context.Background(), source, lastRunAt(daysAgo(31), "acme", "globex"), testNow)

	assert.Equal(t, 5, total)
	require.Len(t, source.calls, 2, "each organization has less than one batch to delete")
	assert.Equal(t, testNow.Add(-30*24*time.Hour), source.calls[0].cutoff, "acme keeps profiles for its own 30 days")
	assert.Equal(t, testNow.Add(-90*24*time.Hour), source.calls[1].cutoff, "globex gets the 90 day default")
	assert.Equal(t, constants.TemporaryProfileCleanupBatchSize, source.calls[0].size, "batches have the fixed size")
	assert.Equal(t, constants.TemporaryProfileCleanupBatchSize, source.calls[1].size)
}

func TestCleanupRunsBatchesUntilTheBacklogIsGone(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	source := &fakeSource{
		orgIDs:    []string{"acme"},
		primaries: map[string]string{},
		backlog:   map[string]int{"acme": 2*constants.TemporaryProfileCleanupBatchSize + 1},
	}

	total := runTemporaryProfileCleanup(context.Background(), source, lastRunAt(daysAgo(31), "acme"), testNow)

	assert.Equal(t, 2*constants.TemporaryProfileCleanupBatchSize+1, total)
	assert.Len(t, source.calls, 3, "two full batches and a short one; the short batch ends the run")
}

func TestCleanupStopsAnOrganizationAtItsBatchLimit(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	source := &fakeSource{
		orgIDs:    []string{"acme", "globex"},
		primaries: map[string]string{},
		backlog:   map[string]int{"acme": 1 << 30, "globex": 1},
	}

	runTemporaryProfileCleanup(context.Background(), source, lastRunAt(daysAgo(31), "acme", "globex"), testNow)

	acme := 0
	for _, call := range source.calls {
		if call.orgID == "acme" {
			acme++
		}
	}
	assert.Equal(t, maxTemporaryCleanupBatchesPerOrg, acme, "one organization cannot hold the run indefinitely")
	assert.Equal(t, 0, source.backlog["globex"], "the next organization is still served")
}

func TestCleanupContinuesPastAFailingOrganization(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	source := &fakeSource{
		orgIDs:    []string{"acme", "globex"},
		primaries: map[string]string{},
		backlog:   map[string]int{"globex": 2},
		failFor:   map[string]error{"acme": errors.New("boom")},
	}

	total := runTemporaryProfileCleanup(context.Background(), source, lastRunAt(daysAgo(31), "acme", "globex"), testNow)

	assert.Equal(t, 2, total)
}

func TestCleanupDeletesNothingWhenItCannotReadTheSettings(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	for name, source := range map[string]*fakeSource{
		"organizations": {orgsErr: errors.New("down")},
		"settings":      {orgIDs: []string{"acme"}, settingsErr: errors.New("down")},
		"primaries":     {orgIDs: []string{"acme"}, primariesEr: errors.New("down")},
	} {
		t.Run(name, func(t *testing.T) {
			source.backlog = map[string]int{"acme": 3}
			lastRun := lastRunAt(daysAgo(31), "acme")

			total := runTemporaryProfileCleanup(context.Background(), source, lastRun, testNow)

			assert.Zero(t, total)
			assert.Empty(t, source.calls, "the defaults could override a shorter retention, so nothing is deleted")
			assert.Equal(t, daysAgo(31), lastRun["acme"], "the organization is still due on the next check")
		})
	}
}

func TestCleanupStopsWhenTheContextEnds(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := &fakeSource{orgIDs: []string{"acme"}, primaries: map[string]string{}, backlog: map[string]int{"acme": 3}}

	total := runTemporaryProfileCleanup(ctx, source, lastRunAt(daysAgo(31), "acme"), testNow)

	assert.Zero(t, total)
	assert.Empty(t, source.calls)
}

func TestStopTemporaryProfileCleanupWorkerIsSafeWithoutAStart(t *testing.T) {
	require.NoError(t, StopTemporaryProfileCleanupWorker(context.Background()))
}

func TestCleanupSkipsAnOrganizationWhoseRetentionOutlastsEveryProfile(t *testing.T) {
	require.NoError(t, log.Init("ERROR"))
	source := &fakeSource{
		orgIDs: []string{"forever", "acme"},
		settings: map[string]adminConfigModel.TemporaryProfileCleanupSettings{
			"forever": {RetentionDays: math.MaxInt32},
			"acme":    {RetentionDays: maxMeaningfulRetentionDays},
		},
		primaries: map[string]string{},
		backlog:   map[string]int{"forever": 5, "acme": 1},
	}

	lastRun := lastRunAt(daysAgo(31), "forever", "acme")

	total := runTemporaryProfileCleanup(context.Background(), source, lastRun, testNow)

	assert.Equal(t, 1, total)
	require.Len(t, source.calls, 1, "an organization with an unreachable cutoff is not queried")
	assert.Equal(t, "acme", source.calls[0].orgID)
	assert.Equal(t, 5, source.backlog["forever"])
}

func TestAHugeIntervalDoesNotOverflow(t *testing.T) {
	lastRun := lastRunAt(daysAgo(30), "acme")

	assert.False(t, isDue(lastRun, "acme", math.MaxInt32, testNow))
	assert.True(t, isDue(lastRun, "acme", 7, testNow))
}
