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
	"fmt"
	"sync"
	"time"

	adminConfigModel "github.com/wso2/identity-customer-data-service/internal/admin_config/model"
	adminConfigStore "github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

// maxMeaningfulRetentionDays is 100 years. A longer retention deletes nothing, so the organization is skipped.
const maxMeaningfulRetentionDays = 36500

// temporaryCleanupTick is how often the worker looks for organizations whose interval has passed.
const temporaryCleanupTick = time.Hour

// maxTemporaryCleanupBatchesPerOrg caps one organization's share of a run; a larger backlog continues
// in the next run.
const maxTemporaryCleanupBatchesPerOrg = 200

var (
	temporaryCleanupMu sync.Mutex
	// temporaryCleanupCancel stops the worker and cancels in-flight database work.
	temporaryCleanupCancel context.CancelFunc
	// temporaryCleanupDone is closed when the worker goroutine returns.
	temporaryCleanupDone chan struct{}
)

// temporaryProfileSource is what the sweep needs from the database.
type temporaryProfileSource interface {
	// OrgIDs lists the organizations that have profiles.
	OrgIDs(ctx context.Context) ([]string, error)
	// Settings lists the cleanup settings that organizations have set for themselves.
	Settings(ctx context.Context) (map[string]adminConfigModel.TemporaryProfileCleanupSettings, error)
	// Primaries maps each stored organization to its primary organization.
	Primaries(ctx context.Context) (map[string]string, error)
	// DeleteExpired deletes up to batchSize temporary profiles not updated since cutoff.
	DeleteExpired(ctx context.Context, orgID string, cutoff time.Time, batchSize int) (int, error)
}

type databaseTemporaryProfileSource struct{}

func (databaseTemporaryProfileSource) OrgIDs(ctx context.Context) ([]string, error) {
	return profileStore.ListProfileOrgIDs(ctx)
}

func (databaseTemporaryProfileSource) Settings(ctx context.Context) (
	map[string]adminConfigModel.TemporaryProfileCleanupSettings, error) {
	return adminConfigStore.ListTemporaryProfileCleanupSettings(ctx)
}

func (databaseTemporaryProfileSource) Primaries(ctx context.Context) (map[string]string, error) {
	return orgStore.ListPrimaryOrgIDs(ctx)
}

func (databaseTemporaryProfileSource) DeleteExpired(ctx context.Context, orgID string, cutoff time.Time,
	batchSize int) (int, error) {
	return profileStore.DeleteExpiredTemporaryProfiles(ctx, orgID, cutoff, batchSize)
}

// StartTemporaryProfileCleanupWorker deletes expired temporary profiles. It checks every hour, and
// cleans an organization when its interval has passed, using the retention and batch size of that
// organization. StopTemporaryProfileCleanupWorker stops it.
func StartTemporaryProfileCleanupWorker() {
	logger := log.GetLogger()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	temporaryCleanupMu.Lock()
	temporaryCleanupCancel = cancel
	temporaryCleanupDone = done
	temporaryCleanupMu.Unlock()

	logger.Info(fmt.Sprintf("Temporary profile cleanup worker started. It checks every %s", temporaryCleanupTick))

	ticker := time.NewTicker(temporaryCleanupTick)
	lastRun := map[string]time.Time{}

	go func() {
		defer close(done)
		defer utils.RecoverPanic("temporary profile cleanup worker")
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				runTemporaryProfileCleanup(ctx, databaseTemporaryProfileSource{}, lastRun, time.Now())
			case <-ctx.Done():
				logger.Info("Temporary profile cleanup worker stopped")
				return
			}
		}
	}()
}

// StopTemporaryProfileCleanupWorker cancels the sweep and waits for the goroutine to exit. A cut off
// batch is rolled back.
func StopTemporaryProfileCleanupWorker(ctx context.Context) error {
	temporaryCleanupMu.Lock()
	cancel, done := temporaryCleanupCancel, temporaryCleanupDone
	temporaryCleanupCancel, temporaryCleanupDone = nil, nil
	temporaryCleanupMu.Unlock()

	if cancel == nil {
		return nil
	}
	cancel()

	if done == nil {
		return nil
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ErrShutdownIncomplete
	}
}

// cleanupSettings are the settings that apply to an organization, with the defaults filled in.
type cleanupSettings struct {
	retentionDays int
	intervalDays  int
}

// settingsFor returns an organization's cleanup settings. A sub organization follows its primary, and a
// setting that is not stored takes its default.
func settingsFor(orgID string, stored map[string]adminConfigModel.TemporaryProfileCleanupSettings,
	primaries map[string]string) cleanupSettings {
	owner := orgID
	if primary, ok := primaries[orgID]; ok && primary != "" {
		owner = primary
	}
	own := stored[owner]
	settings := cleanupSettings{
		retentionDays: constants.DefaultTemporaryProfileRetentionDays,
		intervalDays:  constants.DefaultTemporaryProfileCleanupIntervalDays,
	}
	if own.RetentionDays > 0 {
		settings.retentionDays = own.RetentionDays
	}
	if own.IntervalDays > 0 {
		settings.intervalDays = own.IntervalDays
	}
	return settings
}

// isDue reports whether the organization's interval, in days, has passed since its last cleanup, and records now as
// that cleanup when it has. An organization seen for the first time is recorded and waits one interval.
func isDue(lastRun map[string]time.Time, orgID string, intervalDays int, now time.Time) bool {
	last, seen := lastRun[orgID]
	if !seen {
		lastRun[orgID] = now
		return false
	}
	// A check can land a little before the interval is up, so a minute of slack avoids a wait of one more.
	if now.Sub(last).Hours()+1.0/60 < float64(intervalDays)*24 {
		return false
	}
	lastRun[orgID] = now
	return true
}

// runTemporaryProfileCleanup deletes the expired temporary profiles of every organization whose interval
// has passed and returns the number deleted. lastRun holds when each organization was last cleaned. One
// organization's failure does not stop the others.
func runTemporaryProfileCleanup(ctx context.Context, source temporaryProfileSource, lastRun map[string]time.Time,
	now time.Time) int {
	logger := log.GetLogger()

	orgIDs, err := source.OrgIDs(ctx)
	if err != nil {
		logger.Error("Temporary profile cleanup could not list the organizations", log.Error(err))
		return 0
	}
	stored, err := source.Settings(ctx)
	if err != nil {
		// Without the organizations' settings the defaults could override a shorter retention, so skip.
		logger.Error("Temporary profile cleanup could not read the settings", log.Error(err))
		return 0
	}
	primaries, err := source.Primaries(ctx)
	if err != nil {
		logger.Error("Temporary profile cleanup could not read the organization tree", log.Error(err))
		return 0
	}

	total := 0
	for _, orgID := range orgIDs {
		if ctx.Err() != nil {
			break
		}
		settings := settingsFor(orgID, stored, primaries)
		if !isDue(lastRun, orgID, settings.intervalDays, now) {
			continue
		}
		if settings.retentionDays > maxMeaningfulRetentionDays {
			// No profile is that old, and the cutoff would fall outside what the databases store.
			continue
		}
		cutoff := now.UTC().AddDate(0, 0, -settings.retentionDays)

		deletedForOrg := 0
		for batch := 0; batch < maxTemporaryCleanupBatchesPerOrg && ctx.Err() == nil; batch++ {
			deleted, err := source.DeleteExpired(ctx, orgID, cutoff, constants.TemporaryProfileCleanupBatchSize)
			if err != nil {
				logger.Error(fmt.Sprintf("Temporary profile cleanup failed for organization %s", orgID),
					log.Error(err))
				break
			}
			deletedForOrg += deleted
			if deleted < constants.TemporaryProfileCleanupBatchSize {
				break
			}
		}
		if deletedForOrg > 0 {
			logger.Info(fmt.Sprintf(
				"Temporary profile cleanup: deleted %d profiles of organization %s not updated for %d days",
				deletedForOrg, orgID, settings.retentionDays))
		}
		total += deletedForOrg
	}
	return total
}
