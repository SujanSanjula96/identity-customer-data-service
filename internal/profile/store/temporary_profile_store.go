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

package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/rows"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// ListProfileOrgIDs returns the ID of every organization that has a profile.
func ListProfileOrgIDs(ctx context.Context) ([]string, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, temporaryCleanupError("failed to get the database client to list the organizations", err)
	}
	defer func() { _ = dbClient.Close() }()

	results, err := dbClient.ExecuteQueryContext(ctx, scripts.ListProfileOrgIDs)
	if err != nil {
		return nil, temporaryCleanupError("failed to list the organizations that have profiles", err)
	}
	orgIDs := make([]string, 0, len(results))
	for _, row := range results {
		if orgID := rows.String(row, "org_id"); orgID != "" {
			orgIDs = append(orgIDs, orgID)
		}
	}
	return orgIDs, nil
}

// DeleteExpiredTemporaryProfiles deletes up to batchSize temporary profiles of the organization not
// updated since cutoff, and returns how many.
func DeleteExpiredTemporaryProfiles(ctx context.Context, orgID string, cutoff time.Time, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = constants.TemporaryProfileCleanupBatchSize
	}
	if batchSize > constants.MaxTemporaryProfileCleanupBatchSize {
		batchSize = constants.MaxTemporaryProfileCleanupBatchSize
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return 0, temporaryCleanupError("failed to get the database client to delete temporary profiles", err)
	}
	defer func() { _ = dbClient.Close() }()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		return 0, temporaryCleanupError("failed to begin the transaction that deletes temporary profiles", err)
	}

	expired, err := tx.QueryContext(ctx, scripts.ListExpiredTemporaryProfiles, orgID, cutoff.UTC(), batchSize)
	if err != nil {
		_ = tx.Rollback()
		return 0, temporaryCleanupError("failed to list expired temporary profiles", err)
	}
	profileIDs := make([]interface{}, 0, batchSize)
	for expired.Next() {
		var profileID string
		if err := expired.Scan(&profileID); err != nil {
			_ = expired.Close()
			_ = tx.Rollback()
			return 0, temporaryCleanupError("failed to read the expired temporary profiles", err)
		}
		profileIDs = append(profileIDs, profileID)
	}
	if err := expired.Err(); err != nil {
		_ = expired.Close()
		_ = tx.Rollback()
		return 0, temporaryCleanupError("failed to read the expired temporary profiles", err)
	}
	// Close the rows before the next statement.
	if err := expired.Close(); err != nil {
		_ = tx.Rollback()
		return 0, temporaryCleanupError("failed to read the expired temporary profiles", err)
	}

	if len(profileIDs) == 0 {
		return 0, tx.Commit()
	}

	args := append([]interface{}{orgID}, profileIDs...)
	list := bindList(2, len(profileIDs))

	steps := []struct {
		statement model.DBQuery
		failure   string
	}{
		{scripts.IRDeleteRejectionPairsForProfiles.Format(list, list), "the rejections"},
		{scripts.DeleteReviewTasksByCandidateProfileIDs.Format(list), "the review tasks"},
		{scripts.DeleteProfilesByIDs.Format(list), "the profiles"},
		{scripts.DeleteProfileReferencesByProfileIDs.Format(list), "the reference rows"},
	}
	for _, step := range steps {
		if _, err := tx.ExecContext(ctx, step.statement, args...); err != nil {
			_ = tx.Rollback()
			return 0, temporaryCleanupError("failed to delete "+step.failure+" of temporary profiles", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, temporaryCleanupError("failed to commit the deletion of temporary profiles", err)
	}

	log.GetLogger().Debug(fmt.Sprintf("Deleted %d temporary profiles of organization %s not updated since %s",
		len(profileIDs), orgID, cutoff.UTC().Format(time.RFC3339)))
	return len(profileIDs), nil
}

// bindList returns count comma separated placeholders starting at $first.
func bindList(first, count int) string {
	placeholders := make([]string, count)
	for i := range placeholders {
		placeholders[i] = fmt.Sprintf("$%d", first+i)
	}
	return strings.Join(placeholders, ", ")
}

func temporaryCleanupError(description string, err error) error {
	log.GetLogger().Debug(description, log.Error(err))
	return errors2.NewServerError(errors2.ErrorMessage{
		Code:        errors2.DeleteProfile.Code,
		Message:     errors2.DeleteProfile.Message,
		Description: description,
	}, err)
}
