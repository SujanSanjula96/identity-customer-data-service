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

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/orgkey"
)

// GetProfilesForOrgAfter returns up to limit profiles whose id sorts after afterProfileID.
// Pass an empty afterProfileID to start. See IRGetProfilesForOrgAfter for why this is keyed
// rather than offset-based.
func GetProfilesForOrgAfter(ctx context.Context, orgHandle string, afterProfileID string, limit int) ([]model.ProfileData, error) {
	logger := log.GetLogger()

	// The profiles table keys by org key; the identity resolution tables keep the handle. An
	// organization that CDS does not know gets an empty key, which matches no row.
	orgKey, _, err := orgkey.ForRead(ctx, orgHandle)
	if err != nil {
		return nil, err
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_SEARCH_FAILED.Code,
			Message:     errors2.IR_SEARCH_FAILED.Message,
			Description: "Failed to connect to database for paginated profile lookup.",
		}, err)
	}
	defer dbClient.Close()

	query := scripts.IRGetProfilesForOrgAfter
	results, err := dbClient.ExecuteQueryContext(ctx, query, orgKey, afterProfileID, limit)
	if err != nil {
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_SEARCH_FAILED.Code,
			Message:     errors2.IR_SEARCH_FAILED.Message,
			Description: fmt.Sprintf("Failed to load profiles after '%s' for org: %s", afterProfileID, orgHandle),
		}, err)
	}

	profiles := make([]model.ProfileData, 0, len(results))
	for _, row := range results {
		pd, err := scanProfileData(row)
		if err != nil {
			logger.Warn("Store: skipping profile due to scan error", log.Error(err))
			continue
		}
		profiles = append(profiles, pd)
	}

	return profiles, nil
}

func scanProfileData(row map[string]interface{}) (model.ProfileData, error) {
	pd := model.ProfileData{
		Attributes: make(map[string]interface{}),
	}

	if v, ok := row["profile_id"]; ok && v != nil {
		pd.ProfileID = fmt.Sprintf("%v", v)
	}
	if v, ok := row["user_id"]; ok && v != nil {
		pd.UserID = fmt.Sprintf("%v", v)
	}
	if v, ok := row["org_handle"]; ok && v != nil {
		pd.OrgHandle = fmt.Sprintf("%v", v)
	}
	if v, ok := row["reference_profile_id"]; ok && v != nil {
		pd.ReferenceProfileID = fmt.Sprintf("%v", v)
	}

	if traitsRaw, ok := row["traits"]; ok && traitsRaw != nil {
		var traits map[string]interface{}
		if b, ok := traitsRaw.([]byte); ok {
			if err := json.Unmarshal(b, &traits); err == nil {
				model.FlattenMap("traits", traits, pd.Attributes)
			}
		}
	}

	if idAttrsRaw, ok := row["identity_attributes"]; ok && idAttrsRaw != nil {
		var idAttrs map[string]interface{}
		if b, ok := idAttrsRaw.([]byte); ok {
			if err := json.Unmarshal(b, &idAttrs); err == nil {
				model.FlattenMap("identity_attributes", idAttrs, pd.Attributes)
			}
		}
	}

	return pd, nil
}

func InsertReviewTask(ctx context.Context, task model.ReviewTask) error {
	logger := log.GetLogger()

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_REVIEW_TASK_FAILED.Code,
			Message:     errors2.IR_REVIEW_TASK_FAILED.Message,
			Description: "Failed to connect to database.",
		}, err)
	}
	defer dbClient.Close()

	// Mirror check: if the reverse pair already exists as PENDING,
	// update its score/breakdown to reflect the latest data instead of creating a duplicate.
	mirrorQuery := scripts.IRMirrorReviewTaskExists
	mirrorRows, err := dbClient.ExecuteQueryContext(ctx, mirrorQuery,
		task.CandidateProfileID, task.IncomingProfileID, constants.ReviewStatusPending)
	if err == nil && len(mirrorRows) > 0 {
		if cnt, ok := mirrorRows[0]["count"]; ok {
			var count int
			switch c := cnt.(type) {
			case int64:
				count = int(c)
			case float64:
				count = int(c)
			}
			if count > 0 {
				// Mirror task exists. Flip it so the latest profile
				breakdownJSON, _ := json.Marshal(task.ScoreBreakdown)
				updateQuery := scripts.IRUpdateMirrorReviewTask
				_, updateErr := dbClient.ExecuteQueryContext(ctx, updateQuery,
					task.IncomingProfileID, task.CandidateProfileID,
					task.MatchScore, string(breakdownJSON),
					task.CandidateProfileID, task.IncomingProfileID, constants.ReviewStatusPending)
				if updateErr != nil {
					logger.Warn(fmt.Sprintf("Store: failed to flip mirror task '%s' ↔ '%s'",
						task.IncomingProfileID, task.CandidateProfileID), log.Error(updateErr))
				}
				return nil
			}
		}
	}

	breakdownJSON, _ := json.Marshal(task.ScoreBreakdown)

	if task.ID == "" {
		task.ID = uuid.New().String()
	}

	query := scripts.IRInsertReviewTask
	_, err = dbClient.ExecuteQueryContext(ctx, query,
		task.ID, task.OrgHandle, task.IncomingProfileID, task.CandidateProfileID,
		task.MatchScore, task.Status, string(breakdownJSON))
	if err != nil {
		logger.Error("Store: failed to insert review task", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:    errors2.IR_REVIEW_TASK_FAILED.Code,
			Message: errors2.IR_REVIEW_TASK_FAILED.Message,
			Description: fmt.Sprintf("Failed to create review task for profiles %s → %s",
				task.IncomingProfileID, task.CandidateProfileID),
		}, err)
	}

	return nil
}

// CancelRelatedReviewTasks cancels all PENDING review tasks that reference either of the given profile IDs.
// Returns the incoming profile IDs of the cancelled tasks so they can be re-evaluated.
func CancelRelatedReviewTasks(ctx context.Context, excludeTaskID, IncomingProfileID, CandidateProfileID, cancelledBy string) ([]string, error) {
	logger := log.GetLogger()

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		logger.Error("Store: failed to get DB client for cascade cancel", log.Error(err))
		return nil, err
	}
	defer dbClient.Close()

	// Step 1: Find incoming profile IDs that will be affected before cancelling.
	findQuery := scripts.IRFindRelatedPendingReviewTasks
	rows, err := dbClient.ExecuteQueryContext(ctx, findQuery,
		excludeTaskID, constants.ReviewStatusPending,
		IncomingProfileID, CandidateProfileID)
	if err != nil {
		logger.Warn("Store: failed to query related tasks for re-evaluation", log.Error(err))
		// Non-fatal for the find step — proceed with cancel anyway.
	}

	var affectedIncomingIDs []string
	for _, row := range rows {
		if v, ok := row["incoming_profile_id"]; ok && v != nil {
			id := fmt.Sprintf("%v", v)
			// Don't re-evaluate profiles that were just merged (incoming or candidate of the resolved task).
			if id != IncomingProfileID && id != CandidateProfileID {
				affectedIncomingIDs = append(affectedIncomingIDs, id)
			}
		}
	}

	// Step 2: Cancel the tasks.
	cancelQuery := scripts.IRCancelRelatedReviewTasks
	_, err = dbClient.ExecuteQueryContext(ctx, cancelQuery,
		constants.ReviewStatusCancelled, cancelledBy,
		fmt.Sprintf("Auto-cancelled: related task %s was resolved", excludeTaskID),
		excludeTaskID, constants.ReviewStatusPending,
		IncomingProfileID, CandidateProfileID)
	if err != nil {
		logger.Error("Store: failed to cancel related review tasks", log.Error(err))
		return nil, err
	}

	return affectedIncomingIDs, nil
}

func GetReviewTaskByID(ctx context.Context, taskID string) (*model.ReviewTask, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_REVIEW_TASK_FAILED.Code,
			Message:     errors2.IR_REVIEW_TASK_FAILED.Message,
			Description: "Failed to connect to database.",
		}, err)
	}
	defer dbClient.Close()

	query := scripts.IRGetReviewTaskByID
	results, err := dbClient.ExecuteQueryContext(ctx, query, taskID)
	if err != nil {
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_REVIEW_TASK_FAILED.Code,
			Message:     errors2.IR_REVIEW_TASK_FAILED.Message,
			Description: fmt.Sprintf("Failed to load review task %s", taskID),
		}, err)
	}

	if len(results) == 0 {
		return nil, nil
	}

	task := scanReviewTask(results[0])
	return &task, nil
}

func GetPendingReviewTasks(ctx context.Context, orgHandle string, pageSize int) ([]model.ReviewTask, int, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, 0, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_REVIEW_TASK_FAILED.Code,
			Message:     errors2.IR_REVIEW_TASK_FAILED.Message,
			Description: "Failed to connect to database.",
		}, err)
	}
	defer dbClient.Close()

	countQuery := scripts.IRCountPendingReviewTasks
	countRows, err := dbClient.ExecuteQueryContext(ctx, countQuery, orgHandle, constants.ReviewStatusPending)
	if err != nil {
		return nil, 0, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_REVIEW_TASK_FAILED.Code,
			Message:     errors2.IR_REVIEW_TASK_FAILED.Message,
			Description: fmt.Sprintf("Failed to count review tasks for org: %s", orgHandle),
		}, err)
	}
	totalCount := 0
	if len(countRows) > 0 {
		if v, ok := countRows[0]["count"]; ok {
			switch c := v.(type) {
			case int64:
				totalCount = int(c)
			case float64:
				totalCount = int(c)
			}
		}
	}

	query := scripts.IRGetPendingReviewTasks
	results, err := dbClient.ExecuteQueryContext(ctx, query, orgHandle, constants.ReviewStatusPending, pageSize)
	if err != nil {
		return nil, 0, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_REVIEW_TASK_FAILED.Code,
			Message:     errors2.IR_REVIEW_TASK_FAILED.Message,
			Description: fmt.Sprintf("Failed to load review tasks for org: %s", orgHandle),
		}, err)
	}

	tasks := make([]model.ReviewTask, 0, len(results))
	for _, row := range results {
		task := scanReviewTask(row)
		tasks = append(tasks, task)
	}

	return tasks, totalCount, nil
}

func GetPendingReviewTasksByProfile(ctx context.Context, orgHandle, profileID string, pageSize int) ([]model.ReviewTask, int, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, 0, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_REVIEW_TASK_FAILED.Code,
			Message:     errors2.IR_REVIEW_TASK_FAILED.Message,
			Description: "Failed to connect to database.",
		}, err)
	}
	defer dbClient.Close()

	countQuery := scripts.IRCountPendingReviewTasksByProfile
	countRows, err := dbClient.ExecuteQueryContext(ctx, countQuery, orgHandle, constants.ReviewStatusPending, profileID)
	if err != nil {
		return nil, 0, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_REVIEW_TASK_FAILED.Code,
			Message:     errors2.IR_REVIEW_TASK_FAILED.Message,
			Description: fmt.Sprintf("Failed to count review tasks for profile: %s", profileID),
		}, err)
	}
	totalCount := 0
	if len(countRows) > 0 {
		if v, ok := countRows[0]["count"]; ok {
			switch c := v.(type) {
			case int64:
				totalCount = int(c)
			case float64:
				totalCount = int(c)
			}
		}
	}

	query := scripts.IRGetPendingReviewTasksByProfile
	results, err := dbClient.ExecuteQueryContext(ctx, query, orgHandle, constants.ReviewStatusPending, profileID, pageSize)
	if err != nil {
		return nil, 0, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_REVIEW_TASK_FAILED.Code,
			Message:     errors2.IR_REVIEW_TASK_FAILED.Message,
			Description: fmt.Sprintf("Failed to load review tasks for profile: %s", profileID),
		}, err)
	}

	tasks := make([]model.ReviewTask, 0, len(results))
	for _, row := range results {
		task := scanReviewTask(row)
		tasks = append(tasks, task)
	}

	return tasks, totalCount, nil
}

func UpdateReviewTaskStatus(ctx context.Context, taskID string, status string, resolvedBy string, notes string) error {
	logger := log.GetLogger()

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_REVIEW_TASK_FAILED.Code,
			Message:     errors2.IR_REVIEW_TASK_FAILED.Message,
			Description: "Failed to connect to database.",
		}, err)
	}
	defer dbClient.Close()

	query := scripts.IRUpdateReviewTaskStatus
	_, err = dbClient.ExecuteQueryContext(ctx, query, status, resolvedBy, notes, taskID)
	if err != nil {
		logger.Error("Store: failed to update review task", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_REVIEW_TASK_FAILED.Code,
			Message:     errors2.IR_REVIEW_TASK_FAILED.Message,
			Description: fmt.Sprintf("Failed to update review task %s to status %s", taskID, status),
		}, err)
	}

	return nil
}

// scanReviewTask converts a DB row to a ReviewTask.
func scanReviewTask(row map[string]interface{}) model.ReviewTask {
	task := model.ReviewTask{}

	if v, ok := row["id"]; ok && v != nil {
		task.ID = fmt.Sprintf("%v", v)
	}
	if v, ok := row["org_handle"]; ok && v != nil {
		task.OrgHandle = fmt.Sprintf("%v", v)
	}
	if v, ok := row["incoming_profile_id"]; ok && v != nil {
		task.IncomingProfileID = fmt.Sprintf("%v", v)
	}
	if v, ok := row["candidate_profile_id"]; ok && v != nil {
		task.CandidateProfileID = fmt.Sprintf("%v", v)
	}
	if v, ok := row["match_score"]; ok && v != nil {
		switch f := v.(type) {
		case float64:
			task.MatchScore = f
		case []byte:
			if parsed, err := strconv.ParseFloat(string(f), 64); err == nil {
				task.MatchScore = parsed
			}
		case string:
			if parsed, err := strconv.ParseFloat(f, 64); err == nil {
				task.MatchScore = parsed
			}
		}
	}
	if v, ok := row["status"]; ok && v != nil {
		task.Status = fmt.Sprintf("%v", v)
	}
	// PostgreSQL returns the JSONB column as bytes and SQLite returns its TEXT column as a
	// string. Reading only the first dropped the breakdown on the inbuilt datasource, and with
	// it the evidence a rejection is recorded against.
	var breakdown map[string]float64
	switch raw := row["score_breakdown"].(type) {
	case []byte:
		if err := json.Unmarshal(raw, &breakdown); err == nil {
			task.ScoreBreakdown = breakdown
		}
	case string:
		if err := json.Unmarshal([]byte(raw), &breakdown); err == nil {
			task.ScoreBreakdown = breakdown
		}
	}
	if v, ok := row["created_at"]; ok && v != nil {
		if t, ok := v.(time.Time); ok {
			task.CreatedAt = t.UTC().Format(time.RFC3339)
		} else {
			task.CreatedAt = fmt.Sprintf("%v", v)
		}
	}
	if v, ok := row["resolved_at"]; ok && v != nil {
		if t, ok := v.(time.Time); ok {
			task.ResolvedAt = t.UTC().Format(time.RFC3339)
		} else {
			task.ResolvedAt = fmt.Sprintf("%v", v)
		}
	}
	if v, ok := row["resolved_by"]; ok && v != nil {
		task.ResolvedBy = fmt.Sprintf("%v", v)
	}
	if v, ok := row["resolution_notes"]; ok && v != nil {
		task.Notes = fmt.Sprintf("%v", v)
	}

	return task
}

// InsertRejectionPair stores a rejection pair in canonical order.
// InsertRejectionPair records that an administrator decided two profiles are different
// people, along with the evidence they decided against.
//
// The pair is stored with the lower id first so one row means one pair, whichever direction
// the review task happened to run in — the unique constraint is on the ordered columns, and
// without normalising here the same pair could occupy two rows.
func InsertRejectionPair(ctx context.Context, orgHandle, profileIDA, profileIDB, rejectedBy string,
	matchScore float64, breakdown map[string]float64) error {

	logger := log.GetLogger()

	first, second := profileIDA, profileIDB
	if first > second {
		first, second = second, first
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_REVIEW_TASK_FAILED.Code,
			Message:     errors2.IR_REVIEW_TASK_FAILED.Message,
			Description: "Failed to connect to database for rejection pair insertion.",
		}, err)
	}
	defer dbClient.Close()

	breakdownJSON, marshalErr := json.Marshal(breakdown)
	if marshalErr != nil {
		breakdownJSON = []byte("{}")
	}

	query := scripts.IRInsertRejectionPair
	if _, err := dbClient.ExecuteQueryContext(ctx, query, uuid.New().String(), orgHandle,
		first, second, matchScore, string(breakdownJSON), rejectedBy); err != nil {
		logger.Error(fmt.Sprintf("Store: failed to record rejection for '%s' and '%s'",
			first, second), log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_REVIEW_TASK_FAILED.Code,
			Message:     errors2.IR_REVIEW_TASK_FAILED.Message,
			Description: fmt.Sprintf("Failed to record rejection for profiles %s and %s", first, second),
		}, err)
	}

	return nil
}

// DeleteRejectionPairsForProfile removes all rejection pairs involving the given profile so that
// a re-evaluation triggered by a profile update can match previously rejected candidates.
func DeleteRejectionPairsForProfile(ctx context.Context, orgHandle, profileID string) error {
	logger := log.GetLogger()

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return err
	}
	defer dbClient.Close()

	query := scripts.IRDeleteRejectionPairsForProfile
	_, err = dbClient.ExecuteQueryContext(ctx, query, orgHandle, profileID)
	if err != nil {
		logger.Warn(fmt.Sprintf("Store: failed to delete rejection pairs for profile '%s'", profileID), log.Error(err))
		return err
	}

	return nil
}

// GetRejectedProfileIDs returns the set of profile IDs that have been rejected against the given profileID.
// GetRejectionsForProfile returns every rejection involving the profile, keyed by the
// profile on the other side of the pair, with the evidence each decision was made against.
func GetRejectionsForProfile(ctx context.Context, orgHandle, profileID string) (map[string]model.Rejection, error) {

	logger := log.GetLogger()

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, err
	}
	defer dbClient.Close()

	query := scripts.IRGetRejectionsForProfile
	results, err := dbClient.ExecuteQueryContext(ctx, query, orgHandle, profileID)
	if err != nil {
		logger.Warn(fmt.Sprintf("Store: failed to load rejections for '%s'", profileID), log.Error(err))
		return nil, err
	}

	rejections := make(map[string]model.Rejection, len(results))
	for _, row := range results {
		first := fmt.Sprintf("%v", row["profile_id_1"])
		second := fmt.Sprintf("%v", row["profile_id_2"])

		other := second
		if second == profileID {
			other = first
		}
		if other == profileID {
			continue
		}

		rejection := model.Rejection{OtherProfileID: other}

		switch score := row["match_score"].(type) {
		case float64:
			rejection.MatchScore = score
		case []byte:
			if parsed, parseErr := strconv.ParseFloat(string(score), 64); parseErr == nil {
				rejection.MatchScore = parsed
			}
		case string:
			if parsed, parseErr := strconv.ParseFloat(score, 64); parseErr == nil {
				rejection.MatchScore = parsed
			}
		}

		switch raw := row["score_breakdown"].(type) {
		case []byte:
			_ = json.Unmarshal(raw, &rejection.ScoreBreakdown)
		case string:
			_ = json.Unmarshal([]byte(raw), &rejection.ScoreBreakdown)
		}

		rejections[other] = rejection
	}

	return rejections, nil
}

// RepointRejectionPairs moves every rejection involving fromProfileID onto toProfileID.
//
// A rejection is keyed by profile ID, but a profile stops being the addressable entity once
// it is merged into a master. Without this the admin's "these are different people"
// decision is silently orphaned and the same pair is proposed again through the master.
// Rows that would name the master on both sides are left alone rather than made
// self-referential.
func RepointRejectionPairs(ctx context.Context, orgHandle, fromProfileID, toProfileID string) error {
	logger := log.GetLogger()

	if fromProfileID == "" || toProfileID == "" || fromProfileID == toProfileID {
		return nil
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_REVIEW_TASK_FAILED.Code,
			Message:     errors2.IR_REVIEW_TASK_FAILED.Message,
			Description: "Failed to connect to database for rejection pair repointing.",
		}, err)
	}
	defer dbClient.Close()

	query := scripts.IRRepointRejectionPairs
	if _, err := dbClient.ExecuteQueryContext(ctx, query, orgHandle, fromProfileID, toProfileID); err != nil {
		logger.Warn(fmt.Sprintf("Store: failed to repoint rejection pairs from '%s' to '%s'",
			fromProfileID, toProfileID), log.Error(err))
		return err
	}

	return nil
}
