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

// Package service implements the identity resolution review task operations: listing pending tasks and
// resolving them by merging or rejecting the pair.
package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	irStore "github.com/wso2/identity-customer-data-service/internal/identity_resolution/store"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/pagination"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"
	urModel "github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	urService "github.com/wso2/identity-customer-data-service/internal/unification_rules/service"
)

// IdentityResolutionServiceInterface lists and resolves identity resolution review tasks.
type IdentityResolutionServiceInterface interface {
	GetPendingReviewTasks(ctx context.Context, orgID string, pageSize int) (*model.ReviewTaskListResponse, error)

	GetPendingReviewTasksByProfile(ctx context.Context, orgID string, profileID string,
		pageSize int) (*model.ReviewTaskListResponse, error)

	ResolveReviewTask(ctx context.Context, orgID string, taskID string, approved bool,
		resolvedBy string, notes string) error
}

// IdentityResolutionService implements IdentityResolutionServiceInterface.
type IdentityResolutionService struct{}

// GetIdentityResolutionService returns a new IdentityResolutionService.
func GetIdentityResolutionService() IdentityResolutionServiceInterface {
	return &IdentityResolutionService{}
}

// GetPendingReviewTasks returns a page of the organization's pending review tasks with their total count.
func (s *IdentityResolutionService) GetPendingReviewTasks(
	ctx context.Context,
	orgID string,
	pageSize int,
) (*model.ReviewTaskListResponse, error) {
	tasks, totalCount, err := irStore.GetPendingReviewTasks(ctx, orgID, pageSize)
	if err != nil {
		return nil, err
	}

	return &model.ReviewTaskListResponse{
		Pagination: pagination.Pagination{
			Count:    totalCount,
			PageSize: pageSize,
		},
		Tasks: tasks,
	}, nil
}

// GetPendingReviewTasksByProfile returns a page of the pending review tasks raised for the profile, with the
// total count of pending tasks that involve it.
func (s *IdentityResolutionService) GetPendingReviewTasksByProfile(
	ctx context.Context,
	orgID string,
	profileID string,
	pageSize int,
) (*model.ReviewTaskListResponse, error) {
	tasks, totalCount, err := irStore.GetPendingReviewTasksByProfile(ctx, orgID, profileID, pageSize)
	if err != nil {
		return nil, err
	}

	return &model.ReviewTaskListResponse{
		Pagination: pagination.Pagination{
			Count:    totalCount,
			PageSize: pageSize,
		},
		Tasks: tasks,
	}, nil
}

// ResolveReviewTask approves or rejects a review task of the organization, recording who resolved it and why.
func (s *IdentityResolutionService) ResolveReviewTask(ctx context.Context, orgID string, taskID string,
	approved bool, resolvedBy string, notes string) error {
	logger := log.GetLogger()

	task, err := irStore.GetReviewTaskByID(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.IRReviewTaskNotFound.Code,
			Message:     errors2.IRReviewTaskNotFound.Message,
			Description: fmt.Sprintf("No review task found with ID %s", taskID),
		}, http.StatusNotFound)
	}
	// The task is fetched by ID alone, so ownership has to be checked here. Reported as
	// not-found rather than forbidden: confirming that someone else's task ID exists is
	// itself a leak.
	if task.OrgID != orgID {
		logger.Warn(fmt.Sprintf("Service: org '%s' attempted to resolve task '%s' owned by org '%s'",
			orgID, taskID, task.OrgID))
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.IRReviewTaskNotFound.Code,
			Message:     errors2.IRReviewTaskNotFound.Message,
			Description: fmt.Sprintf("No review task found with ID %s", taskID),
		}, http.StatusNotFound)
	}

	if task.Status != constants.ReviewStatusPending {
		return reviewTaskResolvedError(taskID, task.Status)
	}

	if !approved {
		return rejectReviewTask(ctx, orgID, task, resolvedBy, notes)
	}

	incomingProfile, err := profileStore.GetProfile(ctx, task.IncomingProfileID, orgID)
	if err != nil {
		logger.Error("Service: failed to load incoming profile for review merge", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRMergeFailed.Code,
			Message:     errors2.IRMergeFailed.Message,
			Description: fmt.Sprintf("Failed to load incoming profile: %s", task.IncomingProfileID),
		}, err)
	}
	if incomingProfile == nil {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ProfileNotFound.Code,
			Message:     errors2.ProfileNotFound.Message,
			Description: fmt.Sprintf("incoming profile %s no longer exists", task.IncomingProfileID),
		}, http.StatusNotFound)
	}

	candidate, err := profileStore.GetProfile(ctx, task.CandidateProfileID, orgID)
	if err != nil {
		logger.Error("Service: failed to load candidate profile for review merge", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRMergeFailed.Code,
			Message:     errors2.IRMergeFailed.Message,
			Description: fmt.Sprintf("Failed to load candidate profile: %s", task.CandidateProfileID),
		}, err)
	}
	if candidate == nil {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ProfileNotFound.Code,
			Message:     errors2.ProfileNotFound.Message,
			Description: fmt.Sprintf("Candidate profile %s no longer exists", task.CandidateProfileID),
		}, http.StatusNotFound)
	}

	// The administrator approved evidence about the two profiles as they were when the task was
	// raised. A profile merged since then belongs to a master whose data they did not see.
	if isMergedProfile(incomingProfile) || isMergedProfile(candidate) {
		return withdrawReviewTask(ctx, orgID, task)
	}

	if incomingProfile.UserID != "" && candidate.UserID != "" && incomingProfile.UserID != candidate.UserID {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.IRCannotMerge.Code,
			Message:     errors2.IRCannotMerge.Message,
			Description: "Two permanent profiles with different user IDs cannot be merged.",
		}, http.StatusConflict)
	}

	// Run the merge BEFORE updating task status. If MergeMatchedProfiles surfaces
	// an error, the task must stay PENDING so the caller can retry.
	// Record which rule proposed the pair, not a generic "review_merge". The administrator
	// approved a specific piece of evidence, and the child reference is the only place a
	// support question — "why were these combined?" — can be answered from the profile
	// alone. Derived from the task's breakdown because its score was capped on the way to
	// review and no longer equals any single rule's score.
	mergeReason := constants.MergeReasonReviewMerge
	// Not coerced by the server's fuzzy setting, unlike the evaluation path: these rules are
	// read to name the rule that drove a match already made, and reading a fuzzy rule as
	// exact here would attribute the merge to a different rule than the one responsible.
	if rawRules, rulesErr := urService.GetUnificationRuleService().GetUnificationRules(ctx, orgID); rulesErr == nil {
		rules := urModel.ActiveSortedByPriority(rawRules, constants.AttributeTypePrimitiveExact,
			constants.UnificationMethodDeterministic, constants.DefaultMatchStrength,
			constants.DefaultMismatchStrength)
		thresholds := model.LoadThresholds(ctx, orgID)
		if ruleName, found := urModel.PrimaryRuleName(task.ScoreBreakdown, rules, thresholds.ManualReview,
			thresholds.DeterministicMatchDecisive); found {
			mergeReason = ruleName
		}
	}

	// The merge marks the task approved in its transaction, cancels the other pending tasks of both
	// profiles, and queues their profiles to be evaluated again. A failed merge leaves the task pending
	// for the caller to retry.
	survivingMaster, mergeErr := workers.MergeReviewedProfiles(ctx, orgID,
		workers.ReviewApproval{TaskID: taskID, ResolvedBy: resolvedBy, Notes: notes},
		*candidate, *incomingProfile, mergeReason)
	var notPending *irStore.ReviewTaskNotPendingError
	if errors.As(mergeErr, &notPending) {
		// Another administrator resolved the task first.
		return reviewTaskResolvedError(taskID, notPending.Status)
	}
	if errors.Is(mergeErr, workers.ErrMergeStale) {
		// A unification job merged one of the two between reading them above and the merge.
		return withdrawReviewTask(ctx, orgID, task)
	}
	if mergeErr != nil {
		logger.Error(fmt.Sprintf("Service: review merge failed for task %s — '%s' and '%s'",
			taskID, incomingProfile.ProfileID, candidate.ProfileID), log.Error(mergeErr))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:    errors2.IRMergeFailed.Code,
			Message: errors2.IRMergeFailed.Message,
			Description: fmt.Sprintf("Failed to merge profiles '%s' and '%s' for review task %s",
				incomingProfile.ProfileID, candidate.ProfileID, taskID),
		}, mergeErr)
	}
	if survivingMaster == nil {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.IRCannotMerge.Code,
			Message:     errors2.IRCannotMerge.Message,
			Description: "The two profiles in this review task cannot be merged.",
		}, http.StatusConflict)
	}

	return nil
}

// rejectReviewTask records that the two profiles of a review task are different people, and marks the
// task rejected.
//
// The profile references are locked, then the task, in the order a merge takes them, so a merge of
// either profile and an approval of the task wait for the rejection or run before it. A task one of
// whose profiles was merged since it was raised is withdrawn rather than rejected: a rejection of the
// merged profile would never be read again, and the master holds data the administrator did not see.
func rejectReviewTask(ctx context.Context, orgID string, task *model.ReviewTask, resolvedBy, notes string) error {
	logger := log.GetLogger()

	err := profileStore.RunInTransaction(ctx, func(txCtx context.Context) error {
		if err := profileStore.LockReferenceProfiles(txCtx, orgID, task.IncomingProfileID,
			task.CandidateProfileID); err != nil {
			return err
		}
		if err := irStore.LockPendingReviewTask(txCtx, task.ID); err != nil {
			return err
		}
		// Keep the evidence the administrator decided against, so a later, materially
		// stronger match can be reconsidered while ordinary edits leave the decision alone.
		if err := irStore.InsertRejectionPair(txCtx, orgID, task.IncomingProfileID,
			task.CandidateProfileID, resolvedBy, task.MatchScore, task.ScoreBreakdown); err != nil {
			logger.Error(fmt.Sprintf("Service: failed to store rejection pair for task %s", task.ID),
				log.Error(err))
			return err
		}
		return irStore.UpdateReviewTaskStatus(txCtx, task.ID, constants.ReviewStatusRejected, resolvedBy, notes)
	})
	var notPending *irStore.ReviewTaskNotPendingError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &notPending):
		return reviewTaskResolvedError(task.ID, notPending.Status)
	case errors.Is(err, profileStore.ErrProfileMerged):
		return withdrawReviewTask(ctx, orgID, task)
	case errors.Is(err, profileStore.ErrProfileReferenceNotFound):
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ProfileNotFound.Code,
			Message:     errors2.ProfileNotFound.Message,
			Description: fmt.Sprintf("A profile of review task %s no longer exists", task.ID),
		}, http.StatusNotFound)
	default:
		return err
	}
}

// reviewTaskResolvedError is the 409 for resolving a review task that is no longer pending.
func reviewTaskResolvedError(taskID, status string) error {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.IRReviewTaskResolved.Code,
		Message:     errors2.IRReviewTaskResolved.Message,
		Description: fmt.Sprintf("Review task %s has already been %s", taskID, status),
	}, http.StatusConflict)
}

// isMergedProfile reports whether a profile has been merged into another profile, which is its master.
func isMergedProfile(p *profileModel.Profile) bool {
	return p.ProfileStatus != nil && p.ProfileStatus.ReferenceProfileID != ""
}

// withdrawReviewTask cancels a review task one of whose profiles has been merged with another profile
// since the task was raised, and returns a 409 that says so.
//
// The task is not applied to the masters the profiles now belong to: they hold data the administrator
// did not see, and the evidence may no longer hold. Both profiles are queued to be evaluated again,
// each as the master it belongs to, which merges them, raises a new task, or leaves them apart.
func withdrawReviewTask(ctx context.Context, orgID string, task *model.ReviewTask) error {
	err := profileStore.RunInTransaction(ctx, func(txCtx context.Context) error {
		if err := irStore.LockPendingReviewTask(txCtx, task.ID); err != nil {
			return err
		}
		return irStore.UpdateReviewTaskStatus(txCtx, task.ID, constants.ReviewStatusCancelled,
			constants.CanceledBySystem, "Withdrawn: a profile of the pair was merged with another profile "+
				"after the task was raised")
	})
	var notPending *irStore.ReviewTaskNotPendingError
	if errors.As(err, &notPending) {
		// The merge that made the task out of date cancelled it, or another administrator resolved it.
		return reviewTaskResolvedError(task.ID, notPending.Status)
	}
	if err != nil {
		return err
	}
	workers.RequeueAsMasters(ctx, orgID, task.IncomingProfileID, task.CandidateProfileID)

	return errors2.NewClientError(errors2.ErrorMessage{
		Code:    errors2.IRCannotMerge.Code,
		Message: errors2.IRCannotMerge.Message,
		Description: "One of the two profiles was merged with another profile after the task was raised, " +
			"so the task was withdrawn. The profiles are evaluated again, and a new task is raised if the " +
			"match still needs a review.",
	}, http.StatusConflict)
}
