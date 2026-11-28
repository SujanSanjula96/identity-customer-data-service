/*
 * Copyright (c) (2025-2026), Choreo Labs LLC. (https://choreolabs.com).
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

// Package workers runs the background jobs of CDS: profile unification, schema sync and cookie
// cleanup.
package workers

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"

	"github.com/google/uuid"

	irStore "github.com/wso2/identity-customer-data-service/internal/identity_resolution/store"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/system/audit"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/queue"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
)

// activeProfileQueue is the queue implementation used for profile unification.
// It is initialized by StartProfileWorker. All access is guarded by
// profileQueueMu to prevent data races between concurrent Enqueue calls and
// shutdown.
var (
	profileQueueMu     sync.RWMutex
	activeProfileQueue queue.ProfileUnificationQueue
	// profileLifecycle counts the jobs that are at work, so that shutdown
	// waits for them before the database pool closes.
	profileLifecycle *jobLifecycle
)

// It is set at startup via RegisterFuzzyResolveFunc to avoid import cycles.
var fuzzyResolveFunc func(ctx context.Context, orgId string, profile profileModel.Profile) error

// RegisterFuzzyResolveFunc sets the callback for fuzzy identity resolution.
func RegisterFuzzyResolveFunc(fn func(ctx context.Context, orgId string, profile profileModel.Profile) error) {
	fuzzyResolveFunc = fn
}

// reindexAfterMergeFunc is called after a merge to update blocking keys.
var reindexAfterMergeFunc func(
	ctx context.Context, primaryID, secondaryID string, orgId string, merged profileModel.Profile,
)

// RegisterReindexAfterMergeFunc sets the callback for post-merge blocking key reindex.
func RegisterReindexAfterMergeFunc(
	fn func(ctx context.Context, primaryID, secondaryID string, orgId string, merged profileModel.Profile),
) {
	reindexAfterMergeFunc = fn
}

// StartProfileWorker initializes the profile unification queue (using the
// provider configured in the runtime config) and starts the consumer, which
// unifies up to identity_resolution.unification_workers profiles at the same
// time. An error is returned when the queue cannot be created or started; the
// caller should treat this as a fatal startup failure.
func StartProfileWorker() error {
	cfg := config.GetCDSRuntime().Config
	workerCount, err := config.ResolveUnificationWorkers(cfg.IdentityResolution, cfg.DataSource.Type)
	if err != nil {
		return fmt.Errorf("workers: %w", err)
	}
	q, err := queue.NewProfileUnificationQueue(cfg)
	if err != nil {
		return fmt.Errorf("workers: failed to create profile unification queue: %w", err)
	}

	// A queue message has no caller, so the worker is the context boundary for
	// the work one message causes.
	lifecycle := newJobLifecycle()
	jobs := newProfileJobs()

	if err := q.Start(func(profile profileModel.Profile) error {
		return runProfileJob(lifecycle, jobs, profile)
	}, workerCount); err != nil {
		_ = q.Close(context.Background())
		return fmt.Errorf("workers: failed to start profile unification queue: %w", err)
	}
	profileQueueMu.Lock()
	activeProfileQueue = q
	profileLifecycle = lifecycle
	profileQueueMu.Unlock()
	log.GetLogger().Info(fmt.Sprintf("Profile unification started with %d workers", workerCount))
	return nil
}

// runProfileJob unifies the profile of one queue message. It returns an error
// when the job did not run to the end because the worker is stopping, so that
// a broker delivers the message again.
//
// A unification that fails on a transient database error, such as a lock that
// was not granted in time, is tried again after a pause. One that still fails
// after unificationRetryDelays is put at the back of the queue, so a database
// that is down or overloaded for longer delays profiles instead of losing them.
func runProfileJob(lifecycle *jobLifecycle, jobs *profileJobs, profile profileModel.Profile) error {
	key := profile.OrgID + "/" + profile.ProfileID
	if !jobs.claim(key) {
		// This instance is unifying the profile already, and that job unifies
		// it once more when it ends.
		return nil
	}

	interrupted := false
	var failure error
	err := lifecycle.run(func(ctx context.Context) {
		for {
			failure = unifyWithRetries(ctx, profile)
			if ctx.Err() != nil {
				interrupted = true
				jobs.release(key)
				return
			}
			if failure != nil && !database.IsTransient(failure) {
				// Running it again would fail the same way.
				log.GetLogger().Error(fmt.Sprintf("Profile %s was not unified", profile.ProfileID),
					log.Error(failure))
				failure = nil
			}
			if !jobs.again(key) {
				return
			}
		}
	})
	if err != nil {
		jobs.release(key)
		log.GetLogger().Info(fmt.Sprintf(
			"workers: the profile worker is stopping, so profile %s was not unified: %v",
			profile.ProfileID, err))
		return err
	}
	if interrupted {
		return fmt.Errorf("workers: unifying profile %s was interrupted: %w", profile.ProfileID, ErrShutdownIncomplete)
	}
	if failure != nil {
		return requeueFailedProfile(profile, failure)
	}
	return nil
}

// unificationRetryDelays are the pauses before each retry of a unification that failed on a
// transient database error. They add up to about half a minute, besides the time each attempt waits
// for its locks.
var unificationRetryDelays = []time.Duration{
	500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second,
}

// unifyWithRetries unifies the profile of a queue message, and tries again after each of
// unificationRetryDelays while it fails on a transient database error.
func unifyWithRetries(ctx context.Context, profile profileModel.Profile) error {
	for attempt := 0; ; attempt++ {
		// An attempt that failed can have merged the profile before it failed. A retry unifies the
		// master it was merged into, which holds its data, so the rest of the evaluation still runs.
		err := unifyQueuedProfile(ctx, profile, attempt > 0)
		if err == nil || !database.IsTransient(err) || attempt == len(unificationRetryDelays) ||
			ctx.Err() != nil {
			return err
		}
		pause := unificationRetryDelays[attempt]
		// Jitter, so the jobs that one held lock failed do not all retry at the same moment.
		pause += rand.N(pause/2 + 1) //nolint:gosec // jitter needs no secure random numbers
		log.GetLogger().Info(fmt.Sprintf("Unifying profile %s failed on the database, so it is tried again "+
			"in %s: %v", profile.ProfileID, pause.Round(time.Millisecond), err))
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// requeueFailedProfile puts a profile whose unification kept failing on the database at the back of
// the queue. It returns an error when the queue does not take it, so that a broker delivers the
// message again later.
func requeueFailedProfile(profile profileModel.Profile, failure error) error {
	profileQueueMu.RLock()
	q := activeProfileQueue
	profileQueueMu.RUnlock()
	if q == nil {
		return fmt.Errorf("workers: profile %s was not unified, and the queue has stopped: %w",
			profile.ProfileID, failure)
	}
	if err := q.Enqueue(profileModel.Profile{ProfileID: profile.ProfileID, OrgID: profile.OrgID}); err != nil {
		log.GetLogger().Error(fmt.Sprintf("Profile %s was not unified, and could not be queued again",
			profile.ProfileID), log.Error(errors.Join(failure, err)))
		return fmt.Errorf("workers: profile %s was not unified: %w", profile.ProfileID, failure)
	}
	log.GetLogger().Warn(fmt.Sprintf("Unifying profile %s kept failing on the database, so it was queued "+
		"again: %v", profile.ProfileID, failure))
	return nil
}

// unifyQueuedProfile reads the organization and the stored profile of a queue
// message and unifies the profile. With followMerge, a profile that has been
// merged since it was queued has its master unified instead.
func unifyQueuedProfile(ctx context.Context, profile profileModel.Profile, followMerge bool) error {
	// The queue message carries the org ID. Read the org once for the job.
	org, found, err := orgService.LookupOrganizationByID(ctx, profile.OrgID)
	if err != nil {
		return fmt.Errorf("read the organization %s of profile %s: %w", profile.OrgID, profile.ProfileID, err)
	}
	if !found {
		log.GetLogger().Error(fmt.Sprintf("CDD does not know the organization %s of profile %s, so the "+
			"profile was not unified", profile.OrgID, profile.ProfileID))
		return nil
	}
	storedProfile, err := profileStore.GetProfileWithOptions(ctx, profile.ProfileID, org.ID,
		profileStore.GetProfileOptions{IncludeApplicationData: false})
	if err != nil {
		return fmt.Errorf("load profile %s for unification: %w", profile.ProfileID, err)
	}
	if storedProfile != nil && followMerge && storedProfile.ProfileStatus != nil &&
		storedProfile.ProfileStatus.ReferenceProfileID != "" {
		masterID := storedProfile.ProfileStatus.ReferenceProfileID
		storedProfile, err = profileStore.GetProfileWithOptions(ctx, masterID, org.ID,
			profileStore.GetProfileOptions{IncludeApplicationData: false})
		if err != nil {
			return fmt.Errorf("load master %s of profile %s for unification: %w", masterID, profile.ProfileID, err)
		}
	}
	if storedProfile == nil {
		return nil
	}
	return unifyProfiles(ctx, org.ID, *storedProfile)
}

// EnqueueProfileForProcessing adds a profile to the active queue for
// asynchronous unification. It is a no-op when the worker has not been
// started or has been stopped.
//
// The message carries only the profile and organization IDs. The worker reads
// the stored profile when it runs, so the rest would only make the backlog
// larger.
func EnqueueProfileForProcessing(profile profileModel.Profile) {
	profileQueueMu.RLock()
	q := activeProfileQueue
	profileQueueMu.RUnlock()
	if q != nil {
		job := profileModel.Profile{ProfileID: profile.ProfileID, OrgID: profile.OrgID}
		if err := q.Enqueue(job); err != nil {
			log.GetLogger().Error(fmt.Sprintf(
				"workers: failed to enqueue profile %s for unification: %v",
				profile.ProfileID, err))
		}
	}
}

// ProfileWorkerQueue is a thin adapter that allows service-layer code to
// enqueue profiles without taking a direct dependency on the queue package.
type ProfileWorkerQueue struct{}

// Enqueue forwards the profile to the active queue via EnqueueProfileForProcessing.
func (q *ProfileWorkerQueue) Enqueue(profile profileModel.Profile) {
	EnqueueProfileForProcessing(profile)
}

// StopProfileWorker shuts the profile unification queue down inside ctx. It
// nils out the global reference under a write lock first, so no concurrent
// Enqueue can send on a closed queue.
//
// It returns nil when every unification job that was at work returned on its
// own, and ErrShutdownIncomplete when the deadline passed with a job still
// running.
//
// It is safe to call more than once.
func StopProfileWorker(ctx context.Context) error {
	profileQueueMu.Lock()
	q := activeProfileQueue
	lifecycle := profileLifecycle
	activeProfileQueue, profileLifecycle = nil, nil
	profileQueueMu.Unlock()

	if q == nil {
		return nil
	}
	if lifecycle == nil {
		return q.Close(ctx)
	}
	return lifecycle.stop(ctx, q.Close)
}

// unifyProfiles unifies profiles based on the configured unification rules.
func unifyProfiles(ctx context.Context, orgID string, newProfile profileModel.Profile) error {
	logger := log.GetLogger()
	// A queued profile can have become a child while it waited. Leave its
	// hierarchy intact; moving children or joining masters is a separate policy.
	if newProfile.ProfileStatus == nil {
		logger.Warn(fmt.Sprintf("Skipping unification for profile %s because its status is missing",
			newProfile.ProfileID))
		return nil
	}
	if !newProfile.ProfileStatus.IsReferenceProfile {
		logger.Debug(fmt.Sprintf("Skipping unification for non-reference profile %s", newProfile.ProfileID))
		return nil
	}

	// The system user-ID match takes precedence even when no rules are defined.
	candidateID, err := findCandidate(ctx, orgID, newProfile, "", nil)
	if err != nil {
		return fmt.Errorf("find the user-ID unification candidate of profile %s: %w", newProfile.ProfileID, err)
	}
	if candidateID != "" {
		done, err := mergeUnificationCandidate(ctx, orgID, candidateID, newProfile,
			constants.SystemUserIDMatchReason, nil)
		if done || err != nil {
			return err
		}
	}

	logger.Info(fmt.Sprintf("Beginning to evaluate unification for profile: %s", newProfile.ProfileID))

	// Rule-based matching.
	//
	// Deterministic and fuzzy rules are not two engines to choose between — an org
	// configures both side by side, and a single evaluation has to honor each rule's
	// own unification_method while aggregating them into one decision. The resolution
	// pipeline is what does that, so it is the only rule-based path: it applies exact
	// matching to deterministic rules and similarity matching to fuzzy ones, then
	// applies the org's thresholds, auto_merge_enabled setting and review-task routing
	// to the combined score.
	//
	// Candidates come from the blocking index rather than a per-rule profile lookup.
	// A fuzzy rule cannot be served by an attribute query at all — there is no SQL
	// predicate for "similar" — so the index has to exist regardless, and routing
	// deterministic rules through a second path would mean two retrieval
	// implementations that must stay behaviourally identical.
	if fuzzyResolveFunc == nil {
		logger.Error(fmt.Sprintf(
			"unifyProfiles: identity resolution is not wired up — skipping rule-based matching for profile %s",
			newProfile.ProfileID))
		return nil
	}
	return fuzzyResolveFunc(ctx, orgID, newProfile)
}

// findCandidate applies unification-specific selection policy using the
// reference-profile lookups exposed by the profile store.
func findCandidate(ctx context.Context, orgID string, incoming profileModel.Profile, property string,
	values []string) (string, error) {
	if property == "" {
		return profileStore.FindOldestReferenceProfileIDByUserID(
			ctx, orgID, incoming.UserID,
			incoming.ProfileID)
	}
	return profileStore.FindOldestReferenceProfileIDByAttributeValues(
		ctx, orgID, property, values,
		incoming.ProfileID, incoming.UserID)
}

// mergeUnificationCandidate hydrates only the selected pair. It returns false
// when the candidate became stale so the caller can continue evaluating other
// rules. Atomic validation against concurrent writers belongs to the separate
// merge work.
func mergeUnificationCandidate(ctx context.Context, orgID string, candidateID string,
	incoming profileModel.Profile, reason string, rule *model.UnificationRule) (bool, error) {
	logger := log.GetLogger()
	candidate, err := profileStore.GetProfileWithOptions(ctx, candidateID, orgID,
		profileStore.GetProfileOptions{IncludeApplicationData: false})
	if err != nil {
		return true, fmt.Errorf("load unification candidate %s: %w", candidateID, err)
	}
	if candidate == nil || candidate.OrgID != incoming.OrgID ||
		candidate.ProfileStatus == nil || !candidate.ProfileStatus.IsReferenceProfile {
		return false, nil
	}
	if rule == nil {
		if incoming.UserID == "" || candidate.UserID != incoming.UserID {
			return false, nil
		}
	} else if !matchesUnificationCandidate(*candidate, incoming, *rule) {
		return false, nil
	}
	if candidate.UserID != "" && incoming.UserID != "" && candidate.UserID != incoming.UserID {
		logger.Info(fmt.Sprintf("Not merging profiles %s and %s: different user IDs", candidateID, incoming.ProfileID))
		return false, nil
	}
	candidate.ApplicationData, err = profileStore.FetchApplicationData(ctx, candidateID)
	if err != nil {
		return true, fmt.Errorf("load the application data of candidate %s for the merge: %w", candidateID, err)
	}
	incoming.ApplicationData, err = profileStore.FetchApplicationData(ctx, incoming.ProfileID)
	if err != nil {
		return true, fmt.Errorf("load the application data of profile %s for the merge: %w", incoming.ProfileID, err)
	}
	logger.Info(fmt.Sprintf("Profiles %s and %s matched for unification using reason %s",
		candidate.ProfileID, incoming.ProfileID, reason))
	// MergeMatchedProfiles reports the profile that survived as master. A nil master with no
	// error means the pair turned out to be unmergeable, which is not a reason to stop
	// evaluating — the caller should carry on rather than treat it as a completed merge.
	mergedMaster, mergeErr := MergeMatchedProfiles(ctx, orgID, *candidate, incoming, reason)
	if errors.Is(mergeErr, ErrMergeStale) {
		logger.Info(fmt.Sprintf("Profile %s or %s was merged by another job after they were matched, so "+
			"both are unified again", candidate.ProfileID, incoming.ProfileID))
		RequeueAsMasters(ctx, orgID, candidate.ProfileID, incoming.ProfileID)
		return true, nil
	}
	if mergeErr != nil {
		return true, fmt.Errorf("merge profiles %s and %s: %w", candidate.ProfileID, incoming.ProfileID, mergeErr)
	}
	return mergedMaster != nil, nil
}

// MergeMatchedProfiles handles all merge scenarios for two matched profiles.
// It determines the master/child relationship based on permanent (has userId) vs temporary,
// and whether the existing profile already has child references.
//
// It returns the profile that survives as the master. Which of the two inputs that is
// depends on the merge shape: a permanent profile wins over a temporary one, and two
// temporary profiles are both demoted under a newly created neutral master. Callers that
// need to act on the merged entity (audit logs, follow-up review tasks, re-indexing) must
// use the returned master rather than assuming either input survived.
//
// A nil master with a nil error means the pair was not mergeable and nothing was written.
// Returns an error when the merge cannot proceed safely, and ErrMergeStale when another job
// merged one of the two profiles after they were matched.
//
// The merge runs in one transaction. It first locks the reference rows of both profiles and
// reads both again, so it merges what is stored now rather than what the caller matched, and two
// merges that share a profile run one after the other. A merge that fails writes nothing.
//
// In the same transaction, the merge cancels the pending review tasks that name either profile:
// each was raised on the data of a profile before the merge changed it. After the commit, the
// profiles of those tasks are queued to be evaluated again against the merged result, which raises
// a new task where the match still needs a person.
func MergeMatchedProfiles(ctx context.Context, orgID string, existingMasterProfile profileModel.Profile,
	newProfile profileModel.Profile, reason string) (*profileModel.Profile, error) {
	return mergeProfiles(ctx, orgID, nil, existingMasterProfile, newProfile, reason)
}

// ReviewApproval is the approval of a review task that a merge carries out.
type ReviewApproval struct {
	TaskID     string
	ResolvedBy string
	Notes      string
}

// MergeReviewedProfiles merges the two profiles of an approved review task, as MergeMatchedProfiles
// does, and marks the task approved in the same transaction. When the task was resolved or withdrawn
// before the merge locked it, it writes nothing and returns a *irStore.ReviewTaskNotPendingError, so
// two administrators who approve the same task at once merge the profiles once.
//
// A nil master with a nil error means the pair was not mergeable, and the task stays pending.
func MergeReviewedProfiles(ctx context.Context, orgID string, approval ReviewApproval,
	existingMasterProfile profileModel.Profile, newProfile profileModel.Profile, reason string) (
	*profileModel.Profile, error) {
	return mergeProfiles(ctx, orgID, &approval, existingMasterProfile, newProfile, reason)
}

// mergeProfiles does the work of MergeMatchedProfiles and MergeReviewedProfiles. approval, when it
// is not nil, is the review task the merge resolves.
func mergeProfiles(ctx context.Context, orgID string, approval *ReviewApproval,
	existingMasterProfile profileModel.Profile, newProfile profileModel.Profile, reason string) (
	*profileModel.Profile, error) {
	var master *profileModel.Profile
	var withdrawn []string
	err := profileStore.RunInTransaction(ctx, func(txCtx context.Context) error {
		existing, incoming, err := lockMergePair(txCtx, orgID, existingMasterProfile.ProfileID,
			newProfile.ProfileID)
		if err != nil {
			return err
		}
		keepTaskID := ""
		if approval != nil {
			// After the profile references, in the order every merge takes its locks.
			if err := irStore.LockPendingReviewTask(txCtx, approval.TaskID); err != nil {
				return err
			}
			keepTaskID = approval.TaskID
		}
		master, err = mergeMatchedProfiles(txCtx, orgID, existing, incoming, reason)
		if err != nil || master == nil {
			return err
		}
		withdrawn, err = irStore.CancelRelatedReviewTasks(txCtx, keepTaskID, existing.ProfileID,
			incoming.ProfileID, constants.CanceledBySystem)
		if err != nil {
			return fmt.Errorf("cancel the review tasks of '%s' and '%s': %w",
				existing.ProfileID, incoming.ProfileID, err)
		}
		if approval != nil {
			return irStore.UpdateReviewTaskStatus(txCtx, approval.TaskID, constants.ReviewStatusApproved,
				approval.ResolvedBy, approval.Notes)
		}
		return nil
	})
	if err != nil {
		master = nil
	}
	var notPending *irStore.ReviewTaskNotPendingError
	if errors.Is(err, ErrMergeStale) || errors.As(err, &notPending) {
		// Nothing was written. The caller unifies a stale pair again, or reports the resolved task.
		return nil, err
	}
	// One audit record for each merge attempt, also when it fails before the merge starts.
	auditMerge(ctx, orgID, existingMasterProfile.ProfileID, newProfile.ProfileID, reason, master, err)
	if err == nil {
		RequeueAsMasters(ctx, orgID, withdrawn...)
	}
	return master, err
}

// ErrMergeStale says that one of two matched profiles is no longer a reference profile when the
// merge locks it, because another job merged it after the pair was matched. Nothing was written.
// The caller hands the pair to RequeueAsMasters.
var ErrMergeStale = errors.New("a profile of the pair was merged by another job after the pair was matched")

// RequeueAsMasters queues profiles to be unified again, each as the master it now belongs to: both
// profiles of a stale merge, or the profiles of review tasks a merge withdrew. Profiles that belong
// to the same master queue it once.
//
// Either profile of a stale merge can be the one that is still apart. When the incoming profile
// was merged elsewhere, its job cannot unify it again, because a child is skipped, so the
// candidate it matched has to be matched against the new master instead.
func RequeueAsMasters(ctx context.Context, orgID string, profileIDs ...string) {
	queued := map[string]bool{}
	for _, profileID := range profileIDs {
		stored, err := profileStore.GetProfileWithOptions(ctx, profileID, orgID,
			profileStore.GetProfileOptions{IncludeApplicationData: false})
		if err != nil || stored == nil {
			log.GetLogger().Warn(fmt.Sprintf("Profile %s of a stale merge could not be read, so it was not "+
				"queued again", profileID), log.Error(err))
			continue
		}
		target := stored.ProfileID
		if stored.ProfileStatus != nil && stored.ProfileStatus.ReferenceProfileID != "" {
			target = stored.ProfileStatus.ReferenceProfileID
		}
		if queued[target] {
			continue
		}
		queued[target] = true
		EnqueueProfileForProcessing(profileModel.Profile{ProfileID: target, OrgID: orgID})
	}
}

// lockMergePair locks the reference rows of both profiles in the transaction of ctx and reads both
// again. It returns ErrMergeStale when either is gone or is no longer a reference profile.
func lockMergePair(ctx context.Context, orgID, existingID, incomingID string) (
	profileModel.Profile, profileModel.Profile, error) {
	if err := profileStore.LockProfileReferences(ctx, orgID, existingID, incomingID); err != nil {
		if errors.Is(err, profileStore.ErrProfileReferenceNotFound) {
			return profileModel.Profile{}, profileModel.Profile{}, fmt.Errorf("%w: %w", ErrMergeStale, err)
		}
		return profileModel.Profile{}, profileModel.Profile{}, err
	}

	pair := make([]profileModel.Profile, 0, 2)
	for _, profileID := range []string{existingID, incomingID} {
		stored, err := profileStore.GetProfile(ctx, profileID, orgID)
		if err != nil {
			return profileModel.Profile{}, profileModel.Profile{},
				fmt.Errorf("read profile '%s' for the merge: %w", profileID, err)
		}
		if stored == nil || stored.ProfileStatus == nil || !stored.ProfileStatus.IsReferenceProfile {
			return profileModel.Profile{}, profileModel.Profile{},
				fmt.Errorf("%w: profile '%s' is no longer a reference profile", ErrMergeStale, profileID)
		}
		pair = append(pair, *stored)
	}
	return pair[0], pair[1], nil
}

// mergeMatchedProfiles does the work of MergeMatchedProfiles.
func mergeMatchedProfiles(ctx context.Context, orgID string, existingMasterProfile profileModel.Profile,
	newProfile profileModel.Profile, reason string) (*profileModel.Profile, error) {
	logger := log.GetLogger()

	// Fetch references for the existing master.
	refs, err := profileStore.FetchReferencedProfiles(ctx, existingMasterProfile.ProfileID)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to fetch references for profile: %s during unification with profile: %s",
			existingMasterProfile.ProfileID, newProfile.ProfileID), log.Error(err))
		return nil, fmt.Errorf("fetch references for master '%s': %w", existingMasterProfile.ProfileID, err)
	}
	existingMasterProfile.ProfileStatus.References = refs

	// The incoming profile can be a master too, as when a master is evaluated again after a merge.
	newRefs, err := profileStore.FetchReferencedProfiles(ctx, newProfile.ProfileID)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to fetch references for profile: %s during unification with profile: %s",
			newProfile.ProfileID, existingMasterProfile.ProfileID), log.Error(err))
		return nil, fmt.Errorf("fetch references for master '%s': %w", newProfile.ProfileID, err)
	}
	newProfile.ProfileStatus.References = newRefs

	// Merge profile data using schema rules
	schemaRules, err := schemaService.GetEffectiveProfileSchemaAttributes(ctx, orgID)
	if err != nil {
		logger.Error(
			fmt.Sprintf("Failed to fetch profile schema attributes for org %s during unification of profile %s",
				orgID, newProfile.ProfileID),
			log.Error(err),
		)
		return nil, fmt.Errorf("fetch schema rules for org '%s': %w", orgID, err)
	}
	newMasterProfile := MergeProfiles(existingMasterProfile, newProfile, schemaRules)

	hasUserIDExisting := existingMasterProfile.UserID != ""
	hasUserIDNew := newProfile.UserID != ""
	hasExistingChildren := len(existingMasterProfile.ProfileStatus.References) > 0 &&
		existingMasterProfile.ProfileStatus.IsReferenceProfile

	// ── Case: Both permanent with different userIds — do not merge ──
	if hasUserIDExisting && hasUserIDNew && existingMasterProfile.UserID != newProfile.UserID {
		logger.Info(fmt.Sprintf("Not merging profiles %s and %s — different userIds (%s vs %s)",
			existingMasterProfile.ProfileID, newProfile.ProfileID,
			existingMasterProfile.UserID, newProfile.UserID))
		return nil, nil
	}

	// ── Case: perm-temp or temp-perm ──
	if hasUserIDExisting != hasUserIDNew {
		return mergePermanentAndTemporary(
			ctx,
			orgID,
			existingMasterProfile,
			newProfile,
			newMasterProfile,
			reason,
			hasExistingChildren,
		)
	}

	// ── Case: Both permanent with same userId OR both temporary ──
	if !hasExistingChildren && len(newRefs) > 0 {
		// Only the incoming profile is a master. It keeps its children and takes the existing
		// profile as one more, rather than both moving under a new master.
		return mergeSameKindProfiles(
			ctx,
			orgID,
			newProfile,
			existingMasterProfile,
			newMasterProfile,
			reason,
			hasUserIDExisting,
			true,
		)
	}
	return mergeSameKindProfiles(
		ctx,
		orgID,
		existingMasterProfile,
		newProfile,
		newMasterProfile,
		reason,
		hasUserIDExisting,
		hasExistingChildren,
	)
}

// auditMerge records a merge of two profiles in the audit log. The target is the profile that
// survived as the master. A pair that was not mergeable wrote nothing, so it has no record.
func auditMerge(ctx context.Context, orgID, existingProfileID, newProfileID, reason string,
	master *profileModel.Profile, mergeErr error) {
	if master == nil && mergeErr == nil {
		return
	}
	entry := audit.Entry{
		Event:    audit.ProfileMerge,
		TargetID: existingProfileID,
		Outcome:  audit.Succeeded(),
		OrgID:    orgID,
		Details: map[string]any{
			"merged_profile_ids": []string{existingProfileID, newProfileID},
			"reason":             reason,
		},
	}
	if mergeErr != nil {
		entry.Outcome = audit.Failed("merge_failed")
	} else {
		entry.TargetID = master.ProfileID
	}
	audit.Log(ctx, entry)
}

// mergePermanentAndTemporary merges a permanent profile (has userId) with a temporary one.
// The permanent profile always becomes the master.
func mergePermanentAndTemporary(ctx context.Context, orgID string,
	existingMasterProfile profileModel.Profile,
	newProfile profileModel.Profile,
	newMasterProfile profileModel.Profile,
	reason string,
	hasExistingChildren bool,
) (*profileModel.Profile, error) {
	logger := log.GetLogger()

	hasUserIDExisting := existingMasterProfile.UserID != ""

	if hasUserIDExisting {
		// Existing is permanent — it stays as master, new becomes child
		logger.Info(fmt.Sprintf("Stitching temporary profile %s to permanent profile %s",
			newProfile.ProfileID, existingMasterProfile.ProfileID))

		newMasterProfile.ProfileID = existingMasterProfile.ProfileID
		newMasterProfile.UserID = existingMasterProfile.UserID

		if err := moveChildren(ctx, newProfile, newMasterProfile); err != nil {
			return nil, err
		}

		newChild := profileModel.Reference{
			ProfileID: newProfile.ProfileID,
			Reason:    reason,
		}
		children := []profileModel.Reference{newChild}

		if err := profileStore.UpdateProfileReferences(ctx, newMasterProfile, children); err != nil {
			logger.Error(fmt.Sprintf("Failed to add child profile %s to master %s",
				newProfile.ProfileID, newMasterProfile.ProfileID), log.Error(err))
			return nil, fmt.Errorf("attach child '%s' to master '%s': %w",
				newProfile.ProfileID, newMasterProfile.ProfileID, err)
		}
	} else {
		// New is permanent — it becomes master, existing becomes child
		logger.Info(fmt.Sprintf("Stitching temporary profile %s to permanent profile %s",
			existingMasterProfile.ProfileID, newProfile.ProfileID))

		newMasterProfile.ProfileID = newProfile.ProfileID
		newMasterProfile.UserID = newProfile.UserID

		// If the existing master had children, re-parent them to the new master
		if hasExistingChildren {
			if err := profileStore.UpdateProfileReferences(
				ctx, newMasterProfile, existingMasterProfile.ProfileStatus.References,
			); err != nil {
				logger.Error(fmt.Sprintf("Failed to re-parent references from %s to %s",
					existingMasterProfile.ProfileID, newMasterProfile.ProfileID), log.Error(err))
				return nil, fmt.Errorf("re-parent children from '%s' to '%s': %w",
					existingMasterProfile.ProfileID, newMasterProfile.ProfileID, err)
			}
		}

		newChild := profileModel.Reference{
			ProfileID: existingMasterProfile.ProfileID,
			Reason:    reason,
		}
		children := []profileModel.Reference{newChild}

		if err := profileStore.UpdateProfileReferences(ctx, newMasterProfile, children); err != nil {
			logger.Error(fmt.Sprintf("Failed to add child profile %s to master %s",
				existingMasterProfile.ProfileID, newMasterProfile.ProfileID), log.Error(err))
			return nil, fmt.Errorf("attach child '%s' to master '%s': %w",
				existingMasterProfile.ProfileID, newMasterProfile.ProfileID, err)
		}
	}

	if err := persistMergedProfileData(ctx, orgID, newMasterProfile, newProfile.ProfileID); err != nil {
		return nil, err
	}
	return &newMasterProfile, nil
}

// mergeSameKindProfiles merges two profiles of the same kind:
// both permanent (same userId) or both temporary.
func mergeSameKindProfiles(ctx context.Context, orgID string,
	existingMasterProfile profileModel.Profile,
	newProfile profileModel.Profile,
	newMasterProfile profileModel.Profile,
	reason string,
	bothPermanent bool,
	hasExistingChildren bool,
) (*profileModel.Profile, error) {
	logger := log.GetLogger()

	if hasExistingChildren {
		// Existing master already has children — merge new profile into it as another child.
		if bothPermanent {
			logger.Info(fmt.Sprintf("Both profiles are permanent with same userId. Merging %s into existing master %s",
				newProfile.ProfileID, existingMasterProfile.ProfileID))
		} else {
			logger.Info(fmt.Sprintf("Both profiles are temporary. Merging %s into existing master %s",
				newProfile.ProfileID, existingMasterProfile.ProfileID))
		}

		newMasterProfile.ProfileID = existingMasterProfile.ProfileID
		newMasterProfile.UserID = existingMasterProfile.UserID

		if err := moveChildren(ctx, newProfile, newMasterProfile); err != nil {
			return nil, err
		}

		newChild := profileModel.Reference{
			ProfileID: newProfile.ProfileID,
			Reason:    reason,
		}
		children := []profileModel.Reference{newChild}

		if err := profileStore.UpdateProfileReferences(ctx, newMasterProfile, children); err != nil {
			logger.Error(fmt.Sprintf("Failed to add child profile %s to master %s",
				newProfile.ProfileID, newMasterProfile.ProfileID), log.Error(err))
			return nil, fmt.Errorf("attach child '%s' to master '%s': %w",
				newProfile.ProfileID, newMasterProfile.ProfileID, err)
		}
	} else if bothPermanent {
		// Both permanent, same userId, no children — promote existing as master.
		logger.Info(fmt.Sprintf("Both profiles are permanent with same userId. Merging %s into existing master %s",
			newProfile.ProfileID, existingMasterProfile.ProfileID))

		newMasterProfile.ProfileID = existingMasterProfile.ProfileID
		newMasterProfile.UserID = existingMasterProfile.UserID

		if err := moveChildren(ctx, newProfile, newMasterProfile); err != nil {
			return nil, err
		}

		newChild := profileModel.Reference{
			ProfileID: newProfile.ProfileID,
			Reason:    reason,
		}
		children := []profileModel.Reference{newChild}

		if err := profileStore.UpdateProfileReferences(ctx, newMasterProfile, children); err != nil {
			logger.Error(fmt.Sprintf("Failed to add child profile %s to master %s",
				newProfile.ProfileID, newMasterProfile.ProfileID), log.Error(err))
			return nil, fmt.Errorf("attach child '%s' to master '%s': %w",
				newProfile.ProfileID, newMasterProfile.ProfileID, err)
		}
	} else {
		// Both temporary, no children — create a new neutral master referencing both.
		logger.Info(fmt.Sprintf("Both profiles are temporary. Creating new master for %s and %s",
			newProfile.ProfileID, existingMasterProfile.ProfileID))

		newMasterProfile.ProfileID = uuid.New().String()
		newMasterProfile.UserID = ""

		childProfile1 := profileModel.Reference{
			ProfileID: newProfile.ProfileID,
			Reason:    reason,
		}
		childProfile2 := profileModel.Reference{
			ProfileID: existingMasterProfile.ProfileID,
			Reason:    reason,
		}

		newMasterProfile.ProfileStatus = &profileModel.ProfileStatus{
			IsReferenceProfile: true,
			ListProfile:        false,
			References:         []profileModel.Reference{childProfile1, childProfile2},
		}

		// The merge transaction rolls back a partly inserted master.
		if err := profileStore.InsertProfile(ctx, orgID, newMasterProfile); err != nil {
			logger.Error(fmt.Sprintf("Failed to insert new master profile while unifying %s and %s",
				newProfile.ProfileID, existingMasterProfile.ProfileID), log.Error(err))
			return nil, fmt.Errorf("insert new master for '%s' and '%s': %w",
				newProfile.ProfileID, existingMasterProfile.ProfileID, err)
		}

		children := []profileModel.Reference{childProfile1, childProfile2}
		if err := profileStore.UpdateProfileReferences(ctx, newMasterProfile, children); err != nil {
			logger.Error(fmt.Sprintf("Failed to add child profiles to new master %s",
				newMasterProfile.ProfileID), log.Error(err))
			return nil, fmt.Errorf("attach children to new master '%s': %w",
				newMasterProfile.ProfileID, err)
		}
	}

	if err := persistMergedProfileData(ctx, orgID, newMasterProfile, newProfile.ProfileID); err != nil {
		return nil, err
	}
	return &newMasterProfile, nil
}

// moveChildren re-parents the children of a profile that becomes a child itself to the master that
// survives the merge, so that every child points at a reference profile.
func moveChildren(ctx context.Context, demoted profileModel.Profile, master profileModel.Profile) error {
	if demoted.ProfileStatus == nil || len(demoted.ProfileStatus.References) == 0 {
		return nil
	}
	if err := profileStore.UpdateProfileReferences(ctx, master, demoted.ProfileStatus.References); err != nil {
		log.GetLogger().Error(fmt.Sprintf("Failed to re-parent references from %s to %s",
			demoted.ProfileID, master.ProfileID), log.Error(err))
		return fmt.Errorf("re-parent children from '%s' to '%s': %w", demoted.ProfileID, master.ProfileID, err)
	}
	return nil
}

// persistMergedProfileData writes the merged application data, traits, and identity attributes
// to the master profile. Returns an error on the first persistence failure.
func persistMergedProfileData(ctx context.Context, orgID string, masterProfile profileModel.Profile,
	triggerProfileID string) error {
	logger := log.GetLogger()

	for _, appCtx := range masterProfile.ApplicationData {
		if err := profileStore.InsertMergedMasterProfileAppData(
			ctx, masterProfile.ProfileID, orgID, appCtx,
		); err != nil {
			logger.Error(fmt.Sprintf("Failed to update app data for master profile %s while unifying profile %s",
				masterProfile.ProfileID, triggerProfileID), log.Error(err))
			return fmt.Errorf("persist app data for master '%s': %w", masterProfile.ProfileID, err)
		}
	}

	if masterProfile.Traits != nil {
		if err := profileStore.InsertMergedMasterProfileTraitData(
			ctx, masterProfile.ProfileID, orgID, masterProfile.Traits,
		); err != nil {
			logger.Error(fmt.Sprintf("Failed to update traits for master profile %s while unifying profile %s",
				masterProfile.ProfileID, triggerProfileID), log.Error(err))
			return fmt.Errorf("persist traits for master '%s': %w", masterProfile.ProfileID, err)
		}
	}

	if masterProfile.IdentityAttributes != nil {
		if err := profileStore.MergeIdentityDataOfProfiles(
			ctx, masterProfile.ProfileID, orgID, masterProfile.IdentityAttributes,
		); err != nil {
			logger.Error(fmt.Sprintf("Failed to update identity data for master profile %s while unifying profile %s",
				masterProfile.ProfileID, triggerProfileID), log.Error(err))
			return fmt.Errorf("persist identity attributes for master '%s': %w", masterProfile.ProfileID, err)
		}
	}

	if reindexAfterMergeFunc != nil {
		reindexAfterMergeFunc(ctx, masterProfile.ProfileID, triggerProfileID, orgID, masterProfile)
	}
	return nil
}

// MergeProfiles merges two profiles based on schema rules and returns the merged profile.
func MergeProfiles(
	existingProfile profileModel.Profile,
	incomingProfile profileModel.Profile,
	schemaRules []schemaModel.ProfileSchemaAttribute,
) profileModel.Profile {
	logger := log.GetLogger()
	logger.Info("Merging profiles, " + existingProfile.ProfileID + " and " + incomingProfile.ProfileID)

	merged := existingProfile
	ruleMap := buildSchemaRuleMap(schemaRules)
	appRuleMap := buildApplicationSchemaRuleMap(schemaRules)

	merged.Traits = mergeNamespaceMap(existingProfile.Traits, incomingProfile.Traits, "traits", ruleMap)
	merged.IdentityAttributes = mergeIdentityAttributes(existingProfile, incomingProfile, ruleMap)
	merged.ApplicationData = mergeAppData(existingProfile.ApplicationData, incomingProfile.ApplicationData, appRuleMap)

	if incomingProfile.UserID != "" {
		merged.UserID = incomingProfile.UserID
	}
	if existingProfile.UserID != "" {
		merged.UserID = existingProfile.UserID
	}

	merged.OrgID = incomingProfile.OrgID
	merged.CreatedAt = existingProfile.CreatedAt
	merged.UpdatedAt = time.Now().UTC()

	return merged
}

// mergeIdentityAttributes merges the identity attributes of two profiles. The identity provider is the source of
// truth for the identity attributes of a profile linked to a user, so when only one of the profiles is linked, its
// values are kept, whatever the merge strategy, and the anonymous profile only fills the attributes it has no
// value for.
func mergeIdentityAttributes(existingProfile, incomingProfile profileModel.Profile,
	ruleMap map[string]schemaModel.ProfileSchemaAttribute) map[string]interface{} {
	existingLinked := existingProfile.UserID != ""
	incomingLinked := incomingProfile.UserID != ""
	if existingLinked && !incomingLinked {
		return fillMissingAttributes(existingProfile.IdentityAttributes, incomingProfile.IdentityAttributes)
	}
	if incomingLinked && !existingLinked {
		return fillMissingAttributes(incomingProfile.IdentityAttributes, existingProfile.IdentityAttributes)
	}
	return mergeNamespaceMap(existingProfile.IdentityAttributes, incomingProfile.IdentityAttributes,
		"identity_attributes", ruleMap)
}

// fillMissingAttributes returns the preferred attributes, with the values of the other attributes added where the
// preferred ones have none. Nested objects are filled the same way.
func fillMissingAttributes(preferred, other map[string]interface{}) map[string]interface{} {
	if preferred == nil && other == nil {
		return nil
	}
	result := make(map[string]interface{}, len(preferred)+len(other))
	for k, v := range preferred {
		result[k] = v
	}
	for k, otherVal := range other {
		preferredVal, exists := result[k]
		if !exists || isEmptyValue(preferredVal) {
			result[k] = otherVal
			continue
		}
		preferredMap, okP := toStringKeyedMap(preferredVal)
		otherMap, okO := toStringKeyedMap(otherVal)
		if okP && okO {
			result[k] = fillMissingAttributes(preferredMap, otherMap)
		}
	}
	return result
}

func buildSchemaRuleMap(rules []schemaModel.ProfileSchemaAttribute) map[string]schemaModel.ProfileSchemaAttribute {
	ruleMap := make(map[string]schemaModel.ProfileSchemaAttribute, len(rules))
	for _, rule := range rules {
		ruleMap[rule.AttributeName] = rule
	}
	return ruleMap
}

func mergeNamespaceMap(existingMap, incomingMap map[string]interface{}, namespace string,
	ruleMap map[string]schemaModel.ProfileSchemaAttribute) map[string]interface{} {
	if existingMap == nil && incomingMap == nil {
		return nil
	}

	result := make(map[string]interface{})

	for k, v := range existingMap {
		result[k] = v
	}

	for k, incomingVal := range incomingMap {
		path := namespace + "." + k
		existingVal := result[k]
		result[k] = mergeByPath(existingVal, incomingVal, path, ruleMap)
	}

	return result
}

func mergeByPath(existing, incoming interface{}, currentPath string,
	ruleMap map[string]schemaModel.ProfileSchemaAttribute) interface{} {
	if rule, ok := ruleMap[currentPath]; ok {
		if strings.EqualFold(rule.ValueType, constants.ComplexDataType) && !rule.MultiValued {
			existingMap, okE := toStringKeyedMap(existing)
			incomingMap, okI := toStringKeyedMap(incoming)

			if okE && okI {
				result := make(map[string]interface{})

				for k, v := range existingMap {
					result[k] = v
				}

				for k, incomingChild := range incomingMap {
					childPath := currentPath + "." + k
					existingChild := result[k]
					result[k] = mergeByPath(existingChild, incomingChild, childPath, ruleMap)
				}

				return result
			}
		}

		return MergeAttributeValue(existing, incoming, rule.MergeStrategy, rule.ValueType, rule.MultiValued)
	}

	existingMap, okE := toStringKeyedMap(existing)
	incomingMap, okI := toStringKeyedMap(incoming)

	if okE && okI {
		result := make(map[string]interface{})

		for k, v := range existingMap {
			result[k] = v
		}

		for k, incomingChild := range incomingMap {
			childPath := currentPath + "." + k
			existingChild := result[k]
			result[k] = mergeByPath(existingChild, incomingChild, childPath, ruleMap)
		}

		return result
	}

	if isEmptyValue(incoming) {
		return existing
	}

	return incoming
}

// matchesUnificationCandidate uses the same attribute traversal as the new database lookups.
func matchesUnificationCandidate(
	existingProfile profileModel.Profile,
	newProfile profileModel.Profile,
	rule model.UnificationRule,
) bool {
	values := make(map[string]bool)
	for _, value := range unificationRuleValues(existingProfile, rule.PropertyName) {
		values[value] = true
	}
	for _, value := range unificationRuleValues(newProfile, rule.PropertyName) {
		if values[value] {
			return true
		}
	}
	return false
}

// unificationRuleValues extracts distinct strings once per rule without
// serializing a profile. Numeric, boolean, null and object leaves never match.
func unificationRuleValues(profile profileModel.Profile, property string) []string {
	parts := strings.Split(property, ".")
	if len(parts) < 2 {
		return nil
	}
	var root interface{}
	switch parts[0] {
	case "traits":
		root = profile.Traits
	case "identity_attributes":
		root = profile.IdentityAttributes
	default:
		return nil
	}
	values := make([]string, 0)
	collectUnificationStrings(root, parts[1:], &values)
	seen := make(map[string]bool, len(values))
	unique := values[:0]
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			unique = append(unique, value)
		}
	}
	return unique
}

func collectUnificationStrings(value interface{}, path []string, result *[]string) {
	if len(path) == 0 {
		switch v := value.(type) {
		case string:
			*result = append(*result, v)
		case []string:
			*result = append(*result, v...)
		case []interface{}:
			for _, item := range v {
				if str, ok := item.(string); ok {
					*result = append(*result, str)
				}
			}
		}
		return
	}
	switch v := value.(type) {
	case map[string]interface{}:
		collectUnificationStrings(v[path[0]], path[1:], result)
	case []interface{}:
		for _, item := range v {
			if object, ok := item.(map[string]interface{}); ok {
				collectUnificationStrings(object, path, result)
			}
		}
	}
}

// MergeAttributeValue merges an incoming attribute value into the existing one by the merge strategy:
// overwrite, ignore, or combine. An unknown strategy takes the incoming value.
func MergeAttributeValue(
	existing interface{},
	incoming interface{},
	strategy string,
	valueType string,
	multiValued bool,
) interface{} {
	switch strings.ToLower(strategy) {
	case "overwrite":
		if isEmptyValue(incoming) {
			return existing
		}
		return incoming
	case "ignore":
		if existing != nil {
			return existing
		}
		return incoming

	case "combine":
		if !multiValued {
			// For non-multiValued complex fields (e.g. product_interests map), deep-merge the maps.
			existingMap, okE := toStringKeyedMap(existing)
			incomingMap, okI := toStringKeyedMap(incoming)
			if okE && okI {
				return deepMergeMaps(existingMap, incomingMap)
			}
			if incoming == nil {
				return existing
			}
			return incoming
		}
		switch strings.ToLower(valueType) {
		case "text", "string":
			a := toStringSlice(existing)
			b := toStringSlice(incoming)
			return combineUniqueStrings(a, b)

		case "integer":
			a := toIntSlice(existing)
			b := toIntSlice(incoming)
			return combineUniqueInts(a, b)

		case "decimal":
			a := toFloatSlice(existing)
			b := toFloatSlice(incoming)
			return combineUniqueFloats(a, b)

		case "boolean":
			a := toBoolSlice(existing)
			b := toBoolSlice(incoming)
			return combineUniqueBools(a, b)

		case "date_time", "datetime":
			a := toStringSlice(existing)
			b := toStringSlice(incoming)
			return combineUniqueStrings(a, b)

		case "complex":
			a := toInterfaceSlice(existing)
			b := toInterfaceSlice(incoming)

			if len(a) == 0 && len(b) == 0 {
				if incoming == nil {
					return existing
				}
				return incoming
			}
			if len(a) == 0 {
				return b
			}
			if len(b) == 0 {
				return a
			}
			return append(a, b...)

		default:
			return incoming
		}
	default:
		return incoming
	}
}

func toStringSlice(value interface{}) []string {
	switch v := value.(type) {
	case []string:
		return v
	case string:
		return []string{v}
	case []interface{}:
		var result []string
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	default:
		return []string{}
	}
}

func toIntSlice(value interface{}) []int {
	switch v := value.(type) {
	case []int:
		return v
	case []interface{}:
		result := make([]int, 0, len(v))
		for _, item := range v {
			if i, ok := item.(float64); ok {
				result = append(result, int(i))
			} else if i, ok := item.(int); ok {
				result = append(result, i)
			}
		}
		return result
	case int:
		return []int{v}
	case float64:
		return []int{int(v)}
	default:
		return []int{}
	}
}

func toStringKeyedMap(v interface{}) (map[string]interface{}, bool) {
	if v == nil {
		return nil, false
	}
	m, ok := v.(map[string]interface{})
	return m, ok
}

func toInterfaceSlice(value interface{}) []interface{} {
	switch v := value.(type) {
	case []interface{}:
		return v
	case []map[string]interface{}:
		result := make([]interface{}, 0, len(v))
		for _, item := range v {
			result = append(result, item)
		}
		return result
	default:
		return nil
	}
}

func isEmptyValue(v interface{}) bool {
	if v == nil {
		return true
	}

	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val) == ""

	case []interface{}:
		return len(val) == 0

	case []string:
		return len(val) == 0

	case []int:
		return len(val) == 0

	case []float64:
		return len(val) == 0

	case []bool:
		return len(val) == 0

	case map[string]interface{}:
		return len(val) == 0
	}

	return false
}

// deepMergeMaps merges overlay into base, keeping keys from both.
// When the same key exists in both maps:
//   - nested maps are recursed into
//   - leaf values: overlay wins (equivalent to overwrite strategy)
//
// This is intentional: a complex parent with strategy=combine means
// "keep keys from both containers". Within each shared key, sub-attribute
// strategies are expected to be "overwrite" (the common case in this schema).
// If a sub-attribute ever needs strategy=combine, this function should be
// made schema-aware, accepting the rule set and current attribute path.
func deepMergeMaps(base, overlay map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(base))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range overlay {
		if baseVal, exists := result[k]; exists {
			baseMap, okB := toStringKeyedMap(baseVal)
			overlayMap, okO := toStringKeyedMap(v)
			if okB && okO {
				result[k] = deepMergeMaps(baseMap, overlayMap)
				continue
			}
		}
		result[k] = v
	}
	return result
}

func combineUniqueStrings(a, b []string) []string {
	seen := make(map[string]bool)
	var combined []string
	for _, val := range append(a, b...) {
		if !seen[val] {
			seen[val] = true
			combined = append(combined, val)
		}
	}
	return combined
}

func combineUniqueInts(a, b []int) []int {
	seen := make(map[int]bool)
	var combined []int
	for _, val := range append(a, b...) {
		if !seen[val] {
			seen[val] = true
			combined = append(combined, val)
		}
	}
	return combined
}

func toFloatSlice(value interface{}) []float64 {
	switch v := value.(type) {
	case []float64:
		return v
	case []interface{}:
		var result []float64
		for _, item := range v {
			switch i := item.(type) {
			case float64:
				result = append(result, i)
			case int:
				result = append(result, float64(i))
			}
		}
		return result
	case float64:
		return []float64{v}
	case int:
		return []float64{float64(v)}
	default:
		return []float64{}
	}
}

func toBoolSlice(value interface{}) []bool {
	switch v := value.(type) {
	case []bool:
		return v
	case []interface{}:
		var result []bool
		for _, item := range v {
			if b, ok := item.(bool); ok {
				result = append(result, b)
			}
		}
		return result
	case bool:
		return []bool{v}
	default:
		return []bool{}
	}
}

func combineUniqueFloats(a, b []float64) []float64 {
	seen := make(map[float64]bool)
	var combined []float64
	for _, val := range append(a, b...) {
		if !seen[val] {
			seen[val] = true
			combined = append(combined, val)
		}
	}
	return combined
}

func combineUniqueBools(a, b []bool) []bool {
	seen := make(map[bool]bool)
	var combined []bool
	for _, val := range append(a, b...) {
		if !seen[val] {
			seen[val] = true
			combined = append(combined, val)
		}
	}
	return combined
}

func mergeAppData(existingAppData, incomingAppData []profileModel.ApplicationData,
	appRuleMap map[string]map[string]schemaModel.ProfileSchemaAttribute) []profileModel.ApplicationData {
	logger := log.GetLogger()
	mergedMap := make(map[string]profileModel.ApplicationData)

	for _, app := range existingAppData {
		mergedMap[app.AppID] = app
		logger.Info("Merging existing application data for application: " + app.AppID)
	}

	for _, newApp := range incomingAppData {
		existingApp, found := mergedMap[newApp.AppID]
		logger.Info("Merging existing application data for application: " + newApp.AppID)
		if !found {
			mergedMap[newApp.AppID] = newApp
			continue
		}

		appRules := appRuleMap[newApp.AppID]
		existingApp.AppSpecificData = mergeNamespaceMap(
			existingApp.AppSpecificData,
			newApp.AppSpecificData,
			"application_data",
			appRules,
		)

		mergedMap[newApp.AppID] = existingApp
	}

	var mergedList []profileModel.ApplicationData
	for appID, app := range mergedMap {
		logger.Info(fmt.Sprintf("Merged application data for application: %s", appID))
		mergedList = append(mergedList, app)
	}

	logger.Info(fmt.Sprintf("Application data merge completed for %d applications", len(mergedList)))
	return mergedList
}

func buildApplicationSchemaRuleMap(
	rules []schemaModel.ProfileSchemaAttribute,
) map[string]map[string]schemaModel.ProfileSchemaAttribute {
	result := make(map[string]map[string]schemaModel.ProfileSchemaAttribute)

	for _, rule := range rules {
		if !strings.HasPrefix(rule.AttributeName, "application_data.") {
			continue
		}
		appID := rule.ApplicationIdentifier
		if appID == "" {
			continue
		}
		if result[appID] == nil {
			result[appID] = make(map[string]schemaModel.ProfileSchemaAttribute)
		}
		result[appID][rule.AttributeName] = rule
	}

	return result
}
