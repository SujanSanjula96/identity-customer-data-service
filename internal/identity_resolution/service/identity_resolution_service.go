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

// Package service implements the identity resolution review task operations: listing pending tasks and
// resolving them by merging or rejecting the pair.
package service

import (
	"context"
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

	status := constants.ReviewStatusRejected
	if approved {
		status = constants.ReviewStatusApproved
	}

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
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.IRReviewTaskResolved.Code,
			Message:     errors2.IRReviewTaskResolved.Message,
			Description: fmt.Sprintf("Review task %s has already been %s", taskID, task.Status),
		}, http.StatusConflict)
	}

	if !approved {
		// Store the rejection pair before marking the task rejected.
		// Keep the evidence the administrator decided against, so a later, materially
		// stronger match can be reconsidered while ordinary edits leave the decision alone.
		if err := irStore.InsertRejectionPair(ctx, orgID, task.IncomingProfileID,
			task.CandidateProfileID, resolvedBy, task.MatchScore, task.ScoreBreakdown); err != nil {
			logger.Error(fmt.Sprintf("Service: failed to store rejection pair for task %s", taskID), log.Error(err))
			return err
		}
		if err := irStore.UpdateReviewTaskStatus(ctx, taskID, status, resolvedBy, notes); err != nil {
			return err
		}
		return nil
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

	// Redirect to master if either profile is a child (may have been merged since the task was created).
	incomingProfile, err = redirectToMasterIfChild(ctx, orgID, incomingProfile)
	if err != nil {
		return err
	}
	candidate, err = redirectToMasterIfChild(ctx, orgID, candidate)
	if err != nil {
		return err
	}

	// After redirect both might now point to the same master — already merged.
	if incomingProfile.ProfileID == candidate.ProfileID {
		return nil
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

	survivingMaster, mergeErr := workers.MergeMatchedProfiles(ctx, orgID, *candidate, *incomingProfile, mergeReason)
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

	// Cascade cancel only after the merge has actually happened. Canceling first meant a
	// failed merge left the sibling tasks cancelled while this one stayed pending, and a
	// cancelled pair cannot be re-proposed while a row for it already exists.
	cancelledIncomingIDs, cancelErr := irStore.CancelRelatedReviewTasks(ctx, taskID,
		task.IncomingProfileID, task.CandidateProfileID, constants.CanceledBySystem)
	if cancelErr != nil {
		logger.Warn(fmt.Sprintf("Service: cascade cancel failed for task %s", taskID), log.Error(cancelErr))
	}

	// Re-enqueue the affected profiles so they are re-evaluated against the new master.
	for _, incomingID := range cancelledIncomingIDs {
		affected, loadErr := profileStore.GetProfile(ctx, incomingID, orgID)
		if loadErr != nil || affected == nil {
			logger.Warn(
				fmt.Sprintf("Service: skipping re-evaluation for '%s' — profile not found or error", incomingID),
			)
			continue
		}

		if affected.ProfileStatus != nil && affected.ProfileStatus.ReferenceProfileID != "" {
			master, masterErr := profileStore.GetProfile(ctx, affected.ProfileStatus.ReferenceProfileID, orgID)
			if masterErr == nil && master != nil {
				workers.EnqueueProfileForProcessing(*master)
			}
			continue
		}

		workers.EnqueueProfileForProcessing(*affected)
	}

	// Merge succeeded — commit task status.
	if err := irStore.UpdateReviewTaskStatus(ctx, taskID, status, resolvedBy, notes); err != nil {
		logger.Error(fmt.Sprintf("Service: merge succeeded but failed to update task %s status to %s",
			taskID, status), log.Error(err))
		return err
	}

	return nil
}

func redirectToMasterIfChild(ctx context.Context, orgID string, p *profileModel.Profile) (*profileModel.Profile,
	error) {
	if p.ProfileStatus == nil || p.ProfileStatus.ReferenceProfileID == "" {
		return p, nil
	}
	masterID := p.ProfileStatus.ReferenceProfileID

	master, err := profileStore.GetProfile(ctx, masterID, orgID)
	if err != nil || master == nil {
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRMergeFailed.Code,
			Message:     errors2.IRMergeFailed.Message,
			Description: fmt.Sprintf("Failed to load master profile '%s' for redirect", masterID),
		}, err)
	}
	return master, nil
}
