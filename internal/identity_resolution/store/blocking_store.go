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
	"fmt"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	"github.com/wso2/identity-customer-data-service/internal/system/cache"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

func UpsertBlockingKeys(ctx context.Context, profileID, orgHandle string, keys []model.BlockingKey) error {
	logger := log.GetLogger()
	if len(keys) == 0 {
		return nil
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		logger.Error("BlockingStore: failed to get DB client", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_BLOCKING_KEYS_FAILED.Code,
			Message:     errors2.IR_BLOCKING_KEYS_FAILED.Message,
			Description: "Failed to connect to database for blocking key indexing.",
		}, err)
	}
	defer dbClient.Close()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		logger.Error("BlockingStore: failed to begin transaction", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_BLOCKING_KEYS_FAILED.Code,
			Message:     errors2.IR_BLOCKING_KEYS_FAILED.Message,
			Description: fmt.Sprintf("Failed to begin transaction for blocking key upsert: %s", profileID),
		}, err)
	}

	deleteQuery := scripts.DeleteBlockingKeysSQL
	if _, err = tx.ExecContext(ctx, deleteQuery, orgHandle, profileID); err != nil {
		logger.Error("BlockingStore: failed to delete existing blocking keys", log.Error(err))
		if rbErr := tx.Rollback(); rbErr != nil {
			logger.Error("BlockingStore: failed to rollback transaction", log.Error(rbErr))
		}
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_BLOCKING_KEYS_FAILED.Code,
			Message:     errors2.IR_BLOCKING_KEYS_FAILED.Message,
			Description: fmt.Sprintf("Failed to delete existing blocking keys for profile: %s", profileID),
		}, err)
	}

	var valueClauses []string
	var args []interface{}
	argIdx := 1

	for _, key := range keys {
		valueClauses = append(valueClauses,
			fmt.Sprintf("($%d, $%d, $%d, $%d)", argIdx, argIdx+1, argIdx+2, argIdx+3))
		args = append(args, profileID, orgHandle, key.AttributeName, key.KeyValue)
		argIdx += blockingKeyColumns
	}

	insertQuery := scripts.IRInsertBlockingKeys.Format(strings.Join(valueClauses, ", "))

	if _, err = tx.ExecContext(ctx, insertQuery, args...); err != nil {
		logger.Error("BlockingStore: failed to insert blocking keys", log.Error(err))
		if rbErr := tx.Rollback(); rbErr != nil {
			logger.Error("BlockingStore: failed to rollback transaction", log.Error(rbErr))
		}
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_BLOCKING_KEYS_FAILED.Code,
			Message:     errors2.IR_BLOCKING_KEYS_FAILED.Message,
			Description: fmt.Sprintf("Failed to insert blocking keys for profile: %s", profileID),
		}, err)
	}

	if err = tx.Commit(); err != nil {
		logger.Error("BlockingStore: failed to commit transaction", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_BLOCKING_KEYS_FAILED.Code,
			Message:     errors2.IR_BLOCKING_KEYS_FAILED.Message,
			Description: fmt.Sprintf("Failed to commit blocking key upsert for profile: %s", profileID),
		}, err)
	}

	return nil
}

func DeleteBlockingKeys(ctx context.Context, orgHandle, profileID string) error {
	logger := log.GetLogger()

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_BLOCKING_KEYS_FAILED.Code,
			Message:     errors2.IR_BLOCKING_KEYS_FAILED.Message,
			Description: "Failed to connect to database for blocking key deletion.",
		}, err)
	}
	defer dbClient.Close()

	deleteQuery := scripts.DeleteBlockingKeysSQL
	_, err = dbClient.ExecuteQueryContext(ctx, deleteQuery, orgHandle, profileID)
	if err != nil {
		logger.Error("BlockingStore: failed to delete blocking keys", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_BLOCKING_KEYS_FAILED.Code,
			Message:     errors2.IR_BLOCKING_KEYS_FAILED.Message,
			Description: fmt.Sprintf("Failed to delete blocking keys for profile: %s", profileID),
		}, err)
	}

	return nil
}

// DeleteBlockingKeysByAttribute removes all blocking keys for a specific attribute across all profiles in an org.
func DeleteBlockingKeysByAttribute(ctx context.Context, orgHandle, attributeName string) error {
	logger := log.GetLogger()

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_BLOCKING_KEYS_FAILED.Code,
			Message:     errors2.IR_BLOCKING_KEYS_FAILED.Message,
			Description: "Failed to connect to database for attribute blocking key deletion.",
		}, err)
	}
	defer dbClient.Close()

	query := scripts.DeleteBlockingKeysByAttributeSQL
	_, err = dbClient.ExecuteQueryContext(ctx, query, orgHandle, attributeName)
	if err != nil {
		logger.Error(fmt.Sprintf("BlockingStore: failed to delete blocking keys for attribute '%s'", attributeName),
			log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_BLOCKING_KEYS_FAILED.Code,
			Message:     errors2.IR_BLOCKING_KEYS_FAILED.Message,
			Description: fmt.Sprintf("Failed to delete blocking keys for attribute: %s", attributeName),
		}, err)
	}

	return nil
}

// InsertBlockingKeys inserts blocking keys for a profile without deleting existing keys.
// Uses ON CONFLICT DO NOTHING to skip duplicates.
func InsertBlockingKeys(ctx context.Context, profileID, orgHandle string, keys []model.BlockingKey) error {
	logger := log.GetLogger()
	if len(keys) == 0 {
		return nil
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_BLOCKING_KEYS_FAILED.Code,
			Message:     errors2.IR_BLOCKING_KEYS_FAILED.Message,
			Description: "Failed to connect to database for blocking key insertion.",
		}, err)
	}
	defer dbClient.Close()

	var valueClauses []string
	var args []interface{}
	argIdx := 1

	for _, key := range keys {
		valueClauses = append(valueClauses,
			fmt.Sprintf("($%d, $%d, $%d, $%d)", argIdx, argIdx+1, argIdx+2, argIdx+3))
		args = append(args, profileID, orgHandle, key.AttributeName, key.KeyValue)
		argIdx += blockingKeyColumns
	}

	insertQuery := scripts.IRInsertBlockingKeys.Format(strings.Join(valueClauses, ", "))

	_, err = dbClient.ExecuteQueryContext(ctx, insertQuery, args...)
	if err != nil {
		logger.Error("BlockingStore: failed to insert blocking keys", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_BLOCKING_KEYS_FAILED.Code,
			Message:     errors2.IR_BLOCKING_KEYS_FAILED.Message,
			Description: fmt.Sprintf("Failed to insert blocking keys for profile: %s", profileID),
		}, err)
	}

	return nil
}

// blockingKeyRow is one pending index entry, flattened out of the per-profile map so the
// insert can be chunked by row count.
type blockingKeyRow struct {
	profileID string
	key       model.BlockingKey
}

// blockingKeyColumns is how many bind parameters each row consumes.
const blockingKeyColumns = 4

// maxBlockingKeyRowsPerInsert caps how many rows go into one statement.
//
// The PostgreSQL wire protocol allows at most 65535 bind parameters per statement, so at
// four parameters per row the hard ceiling is 16383. A page of 500 profiles crosses that
// once profiles average roughly 26 keys each — which ordinary data reaches: four email
// addresses under a fuzzy rule is 20 keys before a fuzzy name adds 7 more. Past the limit
// the whole statement is rejected, and because the backfill logs the failure and moves on,
// those profiles are silently left out of the index. Chunking well below the ceiling keeps
// the statement valid whatever the page holds.
const maxBlockingKeyRowsPerInsert = 5000

// InsertBlockingKeysBatch inserts blocking keys for many profiles, chunked so no single
// statement exceeds the driver's bind-parameter limit.
// Uses ON CONFLICT DO NOTHING to skip duplicates.
func InsertBlockingKeysBatch(ctx context.Context, orgHandle string, perProfileKeys map[string][]model.BlockingKey) error {
	logger := log.GetLogger()

	rows := flattenBlockingKeyRows(perProfileKeys)
	if len(rows) == 0 {
		return nil
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_BLOCKING_KEYS_FAILED.Code,
			Message:     errors2.IR_BLOCKING_KEYS_FAILED.Message,
			Description: "Failed to connect to database for batch blocking key insertion.",
		}, err)
	}
	defer dbClient.Close()

	for start := 0; start < len(rows); start += maxBlockingKeyRowsPerInsert {
		end := start + maxBlockingKeyRowsPerInsert
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[start:end]

		valueClauses := make([]string, 0, len(chunk))
		args := make([]interface{}, 0, len(chunk)*blockingKeyColumns)
		argIdx := 1

		for _, row := range chunk {
			valueClauses = append(valueClauses,
				fmt.Sprintf("($%d, $%d, $%d, $%d)", argIdx, argIdx+1, argIdx+2, argIdx+3))
			args = append(args, row.profileID, orgHandle,
				row.key.AttributeName, row.key.KeyValue)
			argIdx += blockingKeyColumns
		}

		insertQuery := scripts.IRInsertBlockingKeys.Format(strings.Join(valueClauses, ", "))
		if _, err := dbClient.ExecuteQueryContext(ctx, insertQuery, args...); err != nil {
			logger.Error(fmt.Sprintf(
				"BlockingStore: batch insert failed for org '%s' at rows %d-%d of %d",
				orgHandle, start, end, len(rows)), log.Error(err))
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:    errors2.IR_BLOCKING_KEYS_FAILED.Code,
				Message: errors2.IR_BLOCKING_KEYS_FAILED.Message,
				Description: fmt.Sprintf("Failed to batch insert blocking keys for org: %s",
					orgHandle),
			}, err)
		}
	}

	return nil
}

// flattenBlockingKeyRows turns the per-profile map into a flat, chunkable slice.
func flattenBlockingKeyRows(perProfileKeys map[string][]model.BlockingKey) []blockingKeyRow {

	total := 0
	for _, keys := range perProfileKeys {
		total += len(keys)
	}
	if total == 0 {
		return nil
	}

	rows := make([]blockingKeyRow, 0, total)
	for profileID, keys := range perProfileKeys {
		for _, key := range keys {
			rows = append(rows, blockingKeyRow{profileID: profileID, key: key})
		}
	}
	return rows
}

func FindCandidateIDsByKeys(ctx context.Context,
	orgHandle, attributeName string,
	keyValues []string,
	excludeProfileID string,
	maxResults int,
) ([]string, error) {
	logger := log.GetLogger()

	if len(keyValues) == 0 {
		return []string{}, nil
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_BLOCKING_KEYS_FAILED.Code,
			Message:     errors2.IR_BLOCKING_KEYS_FAILED.Message,
			Description: "Failed to connect to database for blocking key lookup.",
		}, err)
	}
	defer dbClient.Close()

	args := []interface{}{orgHandle, attributeName}
	var inClauses []string
	argIdx := 3

	for _, kv := range keyValues {
		inClauses = append(inClauses, fmt.Sprintf("$%d", argIdx))
		args = append(args, kv)
		argIdx++
	}

	args = append(args, excludeProfileID)
	excludeArgIdx := argIdx
	argIdx++

	args = append(args, maxResults+1)
	limitArgIdx := argIdx

	query := scripts.IRFindCandidateIDsByKeys.Format(strings.Join(inClauses, ", "),
		excludeArgIdx,
		limitArgIdx,
	)

	results, err := dbClient.ExecuteQueryContext(ctx, query, args...)
	if err != nil {
		logger.Error("BlockingStore: failed to query blocking keys", log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_BLOCKING_KEYS_FAILED.Code,
			Message:     errors2.IR_BLOCKING_KEYS_FAILED.Message,
			Description: fmt.Sprintf("Failed to query blocking keys for attribute: %s", attributeName),
		}, err)
	}

	// The bucket is too crowded to be evidence of anything — a value this common says
	// nothing about identity, so no candidate from it is worth scoring. Skipping is
	// deliberate, but it costs recall, so make it visible rather than silent.
	if len(results) > maxResults {
		logger.Warn(fmt.Sprintf(
			"BlockingStore: attribute '%s' matched more than %d profiles in org '%s' — skipping this key group",
			attributeName, maxResults, orgHandle))
		return nil, nil
	}

	profileIDs := make([]string, 0, len(results))
	for _, row := range results {
		if id, ok := row["profile_id"].(string); ok && id != "" {
			profileIDs = append(profileIDs, id)
		}
	}

	return profileIDs, nil
}

func GetProfilesByIDs(ctx context.Context, profileIDs []string) ([]model.ProfileData, error) {
	logger := log.GetLogger()

	if len(profileIDs) == 0 {
		return []model.ProfileData{}, nil
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_SEARCH_FAILED.Code,
			Message:     errors2.IR_SEARCH_FAILED.Message,
			Description: "Failed to connect to database for profile batch lookup.",
		}, err)
	}
	defer dbClient.Close()

	var inClauses []string
	var args []interface{}
	for i, id := range profileIDs {
		inClauses = append(inClauses, fmt.Sprintf("$%d", i+1))
		args = append(args, id)
	}

	query := scripts.IRGetProfilesByIDs.Format(strings.Join(inClauses, ", "))

	results, err := dbClient.ExecuteQueryContext(ctx, query, args...)
	if err != nil {
		logger.Error("BlockingStore: failed to batch load profiles", log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_SEARCH_FAILED.Code,
			Message:     errors2.IR_SEARCH_FAILED.Message,
			Description: "Failed to batch load candidate profiles.",
		}, err)
	}

	profiles := make([]model.ProfileData, 0, len(results))
	for _, row := range results {
		pd, err := scanProfileData(row)
		if err != nil {
			logger.Warn("BlockingStore: skipping profile due to scan error", log.Error(err))
			continue
		}
		profiles = append(profiles, pd)
	}

	return profiles, nil
}

// rarityCache memoises value frequencies. Frequencies move slowly and are only used to
// pick a band, so a short TTL keeps the scoring path off the database for hot values
// without letting a genuinely spreading value stay classified as rare.
var rarityCache = cache.NewCache(constants.RarityLookupCacheTTL)

// CountProfilesByBlockingKey reports how many profiles in the org already carry the given
// exact blocking key — that is, how common the value is within the tenant.
func CountProfilesByBlockingKey(ctx context.Context, orgHandle, attributeName, keyValue string) (int, error) {
	logger := log.GetLogger()

	cacheKey := orgHandle + "|" + attributeName + "|" + keyValue
	if cached, found := rarityCache.Get(cacheKey); found {
		if count, ok := cached.(int); ok {
			return count, nil
		}
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return 0, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IR_BLOCKING_KEYS_FAILED.Code,
			Message:     errors2.IR_BLOCKING_KEYS_FAILED.Message,
			Description: "Failed to connect to database for value frequency lookup.",
		}, err)
	}
	defer dbClient.Close()

	query := scripts.IRCountProfilesByBlockingKey
	results, err := dbClient.ExecuteQueryContext(ctx, query, orgHandle, attributeName, keyValue)
	if err != nil {
		logger.Warn(fmt.Sprintf("BlockingStore: frequency lookup failed for attribute '%s'", attributeName),
			log.Error(err))
		return 0, err
	}

	count := 0
	if len(results) > 0 {
		switch v := results[0]["profile_count"].(type) {
		case int64:
			count = int(v)
		case int:
			count = v
		case float64:
			count = int(v)
		}
	}

	rarityCache.Set(cacheKey, count)
	return count, nil
}
