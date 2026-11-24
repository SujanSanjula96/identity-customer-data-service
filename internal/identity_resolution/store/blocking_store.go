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
	"sort"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	"github.com/wso2/identity-customer-data-service/internal/system/cache"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	dbmodel "github.com/wso2/identity-customer-data-service/internal/system/database/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/rows"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// UpsertBlockingKeys replaces the profile's blocking keys with keys in one transaction. It does nothing when
// keys is empty, leaving the existing keys in place.
//
// It reads the stored keys of the profile, and deletes and inserts only the difference. When the keys did not
// change, it writes nothing. An update that changes one attribute thus does not rewrite the keys of the others.
func UpsertBlockingKeys(ctx context.Context, profileID, orgID string, keys []model.BlockingKey) error {
	logger := log.GetLogger()
	if len(keys) == 0 {
		return nil
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		logger.Error("BlockingStore: failed to get DB client", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRBlockingKeysFailed.Code,
			Message:     errors2.IRBlockingKeysFailed.Message,
			Description: "Failed to connect to database for blocking key indexing.",
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		logger.Error("BlockingStore: failed to begin transaction", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRBlockingKeysFailed.Code,
			Message:     errors2.IRBlockingKeysFailed.Message,
			Description: fmt.Sprintf("Failed to begin transaction for blocking key upsert: %s", profileID),
		}, err)
	}
	fail := func(description string, err error) error {
		if rbErr := tx.Rollback(); rbErr != nil {
			logger.Error("BlockingStore: failed to rollback transaction", log.Error(rbErr))
		}
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRBlockingKeysFailed.Code,
			Message:     errors2.IRBlockingKeysFailed.Message,
			Description: description,
		}, err)
	}

	stored, err := readBlockingKeys(ctx, tx, orgID, profileID)
	if err != nil {
		logger.Error("BlockingStore: failed to read existing blocking keys", log.Error(err))
		return fail(fmt.Sprintf("Failed to read existing blocking keys for profile: %s", profileID), err)
	}

	toDelete, toInsert := diffBlockingKeys(stored, keys)

	for _, del := range buildBlockingKeyDeletes(profileID, orgID, toDelete) {
		if _, err = tx.ExecContext(ctx, del.query, del.args...); err != nil {
			logger.Error("BlockingStore: failed to delete existing blocking keys", log.Error(err))
			return fail(fmt.Sprintf("Failed to delete existing blocking keys for profile: %s", profileID), err)
		}
	}

	for _, insert := range buildBlockingKeyInserts(tx.DBType(), profileID, orgID, toInsert) {
		if _, err = tx.ExecContext(ctx, insert.query, insert.args...); err != nil {
			logger.Error("BlockingStore: failed to insert blocking keys", log.Error(err))
			return fail(fmt.Sprintf("Failed to insert blocking keys for profile: %s", profileID), err)
		}
	}

	if err = tx.Commit(); err != nil {
		logger.Error("BlockingStore: failed to commit transaction", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRBlockingKeysFailed.Code,
			Message:     errors2.IRBlockingKeysFailed.Message,
			Description: fmt.Sprintf("Failed to commit blocking key upsert for profile: %s", profileID),
		}, err)
	}

	return nil
}

// blockingKeyID is the stored identity of a blocking key: the attribute and the key value.
type blockingKeyID struct {
	attributeName string
	keyValue      string
}

// readBlockingKeys returns the stored blocking keys of the profile.
func readBlockingKeys(ctx context.Context, tx *dbmodel.Tx, orgID, profileID string) ([]blockingKeyID, error) {
	result, err := tx.QueryContext(ctx, scripts.IRGetBlockingKeysOfProfile, orgID, profileID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = result.Close() }()

	var stored []blockingKeyID
	for result.Next() {
		var key blockingKeyID
		if err := result.Scan(&key.attributeName, &key.keyValue); err != nil {
			return nil, err
		}
		stored = append(stored, key)
	}
	return stored, result.Err()
}

// diffBlockingKeys returns the stored keys that are not in keys, and the keys that are not stored. A key that
// occurs more than one time in keys is inserted one time.
func diffBlockingKeys(stored []blockingKeyID, keys []model.BlockingKey) ([]blockingKeyID, []model.BlockingKey) {
	storedSet := make(map[blockingKeyID]struct{}, len(stored))
	for _, key := range stored {
		storedSet[key] = struct{}{}
	}

	wanted := make(map[blockingKeyID]struct{}, len(keys))
	var toInsert []model.BlockingKey
	for _, key := range keys {
		id := blockingKeyID{attributeName: key.AttributeName, keyValue: key.KeyValue}
		if _, dup := wanted[id]; dup {
			continue
		}
		wanted[id] = struct{}{}
		if _, ok := storedSet[id]; !ok {
			toInsert = append(toInsert, key)
		}
	}

	var toDelete []blockingKeyID
	for _, key := range stored {
		if _, ok := wanted[key]; !ok {
			toDelete = append(toDelete, key)
		}
	}
	return toDelete, toInsert
}

// maxBlockingKeyDeletesPerStatement caps how many keys one delete names, so that a statement stays below the
// bind parameter limit of every datasource.
const maxBlockingKeyDeletesPerStatement = 500

// buildBlockingKeyDeletes splits the keys to delete into DELETE statements.
func buildBlockingKeyDeletes(profileID, orgID string, keys []blockingKeyID) []blockingKeyInsert {
	var deletes []blockingKeyInsert
	for start := 0; start < len(keys); start += maxBlockingKeyDeletesPerStatement {
		chunk := keys[start:min(start+maxBlockingKeyDeletesPerStatement, len(keys))]
		conditions := make([]string, 0, len(chunk))
		args := make([]interface{}, 0, 2+2*len(chunk))
		args = append(args, orgID, profileID)
		for _, key := range chunk {
			conditions = append(conditions,
				fmt.Sprintf("(attribute_name = $%d AND key_value = $%d)", len(args)+1, len(args)+2))
			args = append(args, key.attributeName, key.keyValue)
		}
		deletes = append(deletes, blockingKeyInsert{
			query: scripts.IRDeleteBlockingKeysOfProfile.Format(strings.Join(conditions, " OR ")),
			args:  args,
		})
	}
	return deletes
}

// DeleteBlockingKeys removes all blocking keys of the profile.
func DeleteBlockingKeys(ctx context.Context, orgID, profileID string) error {
	logger := log.GetLogger()

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRBlockingKeysFailed.Code,
			Message:     errors2.IRBlockingKeysFailed.Message,
			Description: "Failed to connect to database for blocking key deletion.",
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	deleteQuery := scripts.DeleteBlockingKeysSQL
	_, err = dbClient.ExecuteQueryContext(ctx, deleteQuery, orgID, profileID)
	if err != nil {
		logger.Error("BlockingStore: failed to delete blocking keys", log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRBlockingKeysFailed.Code,
			Message:     errors2.IRBlockingKeysFailed.Message,
			Description: fmt.Sprintf("Failed to delete blocking keys for profile: %s", profileID),
		}, err)
	}

	return nil
}

// DeleteBlockingKeysByAttribute removes all blocking keys for a specific attribute across all profiles in an org.
func DeleteBlockingKeysByAttribute(ctx context.Context, orgID, attributeName string) error {
	logger := log.GetLogger()

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRBlockingKeysFailed.Code,
			Message:     errors2.IRBlockingKeysFailed.Message,
			Description: "Failed to connect to database for attribute blocking key deletion.",
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.DeleteBlockingKeysByAttributeSQL
	_, err = dbClient.ExecuteQueryContext(ctx, query, orgID, attributeName)
	if err != nil {
		logger.Error(fmt.Sprintf("BlockingStore: failed to delete blocking keys for attribute '%s'", attributeName),
			log.Error(err))
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRBlockingKeysFailed.Code,
			Message:     errors2.IRBlockingKeysFailed.Message,
			Description: fmt.Sprintf("Failed to delete blocking keys for attribute: %s", attributeName),
		}, err)
	}

	return nil
}

// InsertBlockingKeys inserts blocking keys for a profile without deleting existing keys.
// Uses ON CONFLICT DO NOTHING to skip duplicates.
func InsertBlockingKeys(ctx context.Context, profileID, orgID string, keys []model.BlockingKey) error {
	logger := log.GetLogger()
	if len(keys) == 0 {
		return nil
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRBlockingKeysFailed.Code,
			Message:     errors2.IRBlockingKeysFailed.Message,
			Description: "Failed to connect to database for blocking key insertion.",
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	for _, insert := range buildBlockingKeyInserts(dbClient.DBType(), profileID, orgID, keys) {
		if _, err = dbClient.ExecuteQueryContext(ctx, insert.query, insert.args...); err != nil {
			logger.Error("BlockingStore: failed to insert blocking keys", log.Error(err))
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.IRBlockingKeysFailed.Code,
				Message:     errors2.IRBlockingKeysFailed.Message,
				Description: fmt.Sprintf("Failed to insert blocking keys for profile: %s", profileID),
			}, err)
		}
	}

	return nil
}

// blockingKeyInsert is one INSERT statement of a profile's blocking keys and its arguments.
type blockingKeyInsert struct {
	query dbmodel.DBQuery
	args  []interface{}
}

// buildBlockingKeyInserts splits the keys of one profile into INSERT statements of as many rows
// as dbType accepts in one statement: a single row on Oracle, which has no multi-row VALUES
// before 23ai, and all of them on the other datasources.
func buildBlockingKeyInserts(dbType, profileID, orgID string, keys []model.BlockingKey) []blockingKeyInsert {
	rowsPerInsert := min(maxBlockingKeyRowsPerInsert, database.MaxRowsPerStatement(dbType, blockingKeyColumns))

	var inserts []blockingKeyInsert
	for start := 0; start < len(keys); start += rowsPerInsert {
		chunk := keys[start:min(start+rowsPerInsert, len(keys))]
		valueClauses := make([]string, 0, len(chunk))
		args := make([]interface{}, 0, len(chunk)*blockingKeyColumns)
		argIdx := 1
		for _, key := range chunk {
			valueClauses = append(valueClauses,
				fmt.Sprintf("($%d, $%d, $%d, $%d)", argIdx, argIdx+1, argIdx+2, argIdx+3))
			args = append(args, profileID, orgID, key.AttributeName, key.KeyValue)
			argIdx += blockingKeyColumns
		}
		inserts = append(inserts, blockingKeyInsert{
			query: scripts.IRInsertBlockingKeys.Format(strings.Join(valueClauses, ", ")),
			args:  args,
		})
	}
	return inserts
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
const maxBlockingKeyRowsPerInsert = 5000

// InsertBlockingKeysBatch inserts blocking keys for many profiles, chunked so no single
// statement exceeds the driver's bind-parameter limit.
func InsertBlockingKeysBatch(ctx context.Context, orgID string, perProfileKeys map[string][]model.BlockingKey) error {
	logger := log.GetLogger()

	rows := flattenBlockingKeyRows(perProfileKeys)
	if len(rows) == 0 {
		return nil
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRBlockingKeysFailed.Code,
			Message:     errors2.IRBlockingKeysFailed.Message,
			Description: "Failed to connect to database for batch blocking key insertion.",
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	rowsPerInsert := min(maxBlockingKeyRowsPerInsert,
		database.MaxRowsPerStatement(dbClient.DBType(), blockingKeyColumns))
	for start := 0; start < len(rows); start += rowsPerInsert {
		end := start + rowsPerInsert
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
			args = append(args, row.profileID, orgID,
				row.key.AttributeName, row.key.KeyValue)
			argIdx += blockingKeyColumns
		}

		insertQuery := scripts.IRInsertBlockingKeys.Format(strings.Join(valueClauses, ", "))
		if _, err := dbClient.ExecuteQueryContext(ctx, insertQuery, args...); err != nil {
			logger.Error(fmt.Sprintf(
				"BlockingStore: batch insert failed for org '%s' at rows %d-%d of %d",
				orgID, start, end, len(rows)), log.Error(err))
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:    errors2.IRBlockingKeysFailed.Code,
				Message: errors2.IRBlockingKeysFailed.Message,
				Description: fmt.Sprintf("Failed to batch insert blocking keys for org: %s",
					orgID),
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

// crowdedKeys remembers the key groups and the single keys that matched more than the candidate limit. A value
// that so many profiles share almost never becomes rare again, so the lookup for it is skipped until the entry
// expires. The cache is local to the node: each node learns the crowded keys on its own.
var crowdedKeys = cache.NewCache(constants.CrowdedKeyCacheTTL)

// maxCandidateQueryRounds bounds how many times one lookup widens its limit. See FindCandidateIDsByKeys.
const maxCandidateQueryRounds = 4

func crowdedKeyCacheKey(orgID, attributeName string, maxResults int, keyValues ...string) string {
	return fmt.Sprintf("%s|%s|%d|%s", orgID, attributeName, maxResults, strings.Join(keyValues, "\x1f"))
}

// isCrowded reports whether the group, or one of its keys, matched more than maxResults profiles in the
// current TTL.
func isCrowded(orgID, attributeName string, maxResults int, keyValues []string) bool {
	if _, found := crowdedKeys.Get(crowdedKeyCacheKey(orgID, attributeName, maxResults, keyValues...)); found {
		return true
	}
	if len(keyValues) == 1 {
		return false
	}
	for _, kv := range keyValues {
		if _, found := crowdedKeys.Get(crowdedKeyCacheKey(orgID, attributeName, maxResults, kv)); found {
			return true
		}
	}
	return false
}

// uniqueSorted returns the distinct values in a stable order, so that the same group has the same cache key.
func uniqueSorted(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// FindCandidateIDsByKeys returns the IDs of the organization's profiles indexed under any of keyValues for
// attributeName, leaving out excludeProfileID. When more than maxResults profiles match, the group is too
// crowded to be evidence and the result is nil.
//
// The query has no DISTINCT and asks for len(keys) × (maxResults+1) rows, so the index scan stops early
// however large a key is. A (key, profile) pair is unique in the table, so a full result always holds more
// than maxResults distinct profiles. If a result is full and has maxResults or fewer distinct profiles, the
// query runs again with a larger limit. A crowded group, and each key that is crowded by itself, is
// remembered for constants.CrowdedKeyCacheTTL and is logged once in that time.
func FindCandidateIDsByKeys(ctx context.Context,
	orgID, attributeName string,
	keyValues []string,
	excludeProfileID string,
	maxResults int,
) ([]string, error) {
	logger := log.GetLogger()

	keyValues = uniqueSorted(keyValues)
	if len(keyValues) == 0 {
		return []string{}, nil
	}
	if isCrowded(orgID, attributeName, maxResults, keyValues) {
		logger.Debug(fmt.Sprintf("BlockingStore: attribute '%s' key group is crowded in org '%s', skipped",
			attributeName, orgID))
		return nil, nil
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRBlockingKeysFailed.Code,
			Message:     errors2.IRBlockingKeysFailed.Message,
			Description: "Failed to connect to database for blocking key lookup.",
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	args := []interface{}{orgID, attributeName}
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

	limit := len(keyValues) * (maxResults + 1)
	args = append(args, limit)
	limitArgIdx := len(args) - 1

	query := scripts.IRFindCandidateIDsByKeys.Format(strings.Join(inClauses, ", "),
		excludeArgIdx,
		argIdx,
	)

	var profileIDs []string
	var perKey map[string]map[string]struct{}
	for round := 1; ; round++ {
		results, err := dbClient.ExecuteQueryContext(ctx, query, args...)
		if err != nil {
			logger.Error("BlockingStore: failed to query blocking keys", log.Error(err))
			return nil, errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.IRBlockingKeysFailed.Code,
				Message:     errors2.IRBlockingKeysFailed.Message,
				Description: fmt.Sprintf("Failed to query blocking keys for attribute: %s", attributeName),
			}, err)
		}

		profileIDs, perKey = distinctCandidates(results)
		if len(profileIDs) > maxResults || len(results) < limit || round >= maxCandidateQueryRounds {
			break
		}
		// The rows filled the limit but hold maxResults or fewer distinct profiles. This needs duplicate
		// (key, profile) rows, which the primary key does not permit, so it is rare. Widen the limit.
		limit *= 4
		args[limitArgIdx] = limit
	}

	// The bucket is too crowded to be evidence of anything — a value this common says
	// nothing about identity, so no candidate from it is worth scoring.
	if len(profileIDs) > maxResults {
		crowdedKeys.Set(crowdedKeyCacheKey(orgID, attributeName, maxResults, keyValues...), true)
		for kv, profiles := range perKey {
			if len(profiles) > maxResults && len(keyValues) > 1 {
				crowdedKeys.Set(crowdedKeyCacheKey(orgID, attributeName, maxResults, kv), true)
			}
		}
		logger.Warn(fmt.Sprintf(
			"BlockingStore: attribute '%s' matched more than %d profiles in org '%s' — skipping this key group "+
				"for %s", attributeName, maxResults, orgID, constants.CrowdedKeyCacheTTL))
		return nil, nil
	}

	return profileIDs, nil
}

// distinctCandidates removes the duplicate profile IDs from the candidate rows, in the order of the rows. It
// also groups the distinct profiles by key value.
func distinctCandidates(results []map[string]interface{}) ([]string, map[string]map[string]struct{}) {
	profileIDs := make([]string, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	perKey := make(map[string]map[string]struct{})
	for _, row := range results {
		id := rows.String(row, "profile_id")
		if id == "" {
			continue
		}
		if kv := rows.String(row, "key_value"); kv != "" {
			if perKey[kv] == nil {
				perKey[kv] = make(map[string]struct{})
			}
			perKey[kv][id] = struct{}{}
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		profileIDs = append(profileIDs, id)
	}
	return profileIDs, perKey
}

// GetProfilesByIDs loads the profiles with the given IDs for scoring. A row that cannot be read is logged and
// left out.
func GetProfilesByIDs(ctx context.Context, profileIDs []string) ([]model.ProfileData, error) {
	logger := log.GetLogger()

	if len(profileIDs) == 0 {
		return []model.ProfileData{}, nil
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRSearchFailed.Code,
			Message:     errors2.IRSearchFailed.Message,
			Description: "Failed to connect to database for profile batch lookup.",
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

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
			Code:        errors2.IRSearchFailed.Code,
			Message:     errors2.IRSearchFailed.Message,
			Description: "Failed to batch load candidate profiles.",
		}, err)
	}

	profiles := make([]model.ProfileData, 0, len(results))
	for _, row := range results {
		pd := scanProfileData(row)
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
func CountProfilesByBlockingKey(ctx context.Context, orgID, attributeName, keyValue string) (int, error) {
	logger := log.GetLogger()

	cacheKey := orgID + "|" + attributeName + "|" + keyValue
	if cached, found := rarityCache.Get(cacheKey); found {
		if count, ok := cached.(int); ok {
			return count, nil
		}
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return 0, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRBlockingKeysFailed.Code,
			Message:     errors2.IRBlockingKeysFailed.Message,
			Description: "Failed to connect to database for value frequency lookup.",
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.IRCountProfilesByBlockingKey
	results, err := dbClient.ExecuteQueryContext(ctx, query, orgID, attributeName, keyValue)
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
