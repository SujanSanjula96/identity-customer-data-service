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
	"errors"
	"fmt"
	"sort"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	dbModel "github.com/wso2/identity-customer-data-service/internal/system/database/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
)

// RunInTransaction runs work in one transaction, which commits when work returns nil and rolls
// back when it returns an error or panics.
//
// Every store call that work makes with the context it is given joins the transaction, including
// a store that begins a transaction of its own. The work then holds a single connection, and its
// writes are kept or discarded together.
func RunInTransaction(ctx context.Context, work func(ctx context.Context) error) (err error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return fmt.Errorf("failed to get the database client for a transaction: %w", err)
	}
	defer func() { _ = dbClient.Close() }()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin a transaction: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := work(dbModel.ContextWithTx(ctx, tx)); err != nil {
		return err
	}
	committed = true
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit the transaction: %w", err)
	}
	return nil
}

// ErrProfileReferenceNotFound says a profile to lock has no reference row in the organization.
var ErrProfileReferenceNotFound = errors.New("the profile has no reference row in the organization")

// LockProfileReferences locks the reference rows of the profiles until the transaction in ctx
// ends. It must run under RunInTransaction. The rows are locked one at a time in profile ID order,
// so two transactions that lock an overlapping set wait for each other rather than deadlock.
func LockProfileReferences(ctx context.Context, orgID string, profileIDs ...string) error {
	_, err := lockProfileReferences(ctx, orgID, profileIDs)
	return err
}

// ErrProfileMerged says a locked profile has been merged into another profile, its master.
var ErrProfileMerged = errors.New("the profile has been merged into another profile")

// LockReferenceProfiles locks the reference rows of the profiles as LockProfileReferences does, and
// returns ErrProfileMerged when one of them is no longer a reference profile. The rows stay locked,
// so no merge changes that until the transaction ends.
func LockReferenceProfiles(ctx context.Context, orgID string, profileIDs ...string) error {
	statuses, err := lockProfileReferences(ctx, orgID, profileIDs)
	if err != nil {
		return err
	}
	for _, profileID := range profileIDs {
		if statuses[profileID] != constants.ReferenceProfile {
			return fmt.Errorf("profile %s: %w", profileID, ErrProfileMerged)
		}
	}
	return nil
}

// lockProfileReferences does the work of LockProfileReferences, and returns the status of each
// profile it locked.
func lockProfileReferences(ctx context.Context, orgID string, profileIDs []string) (map[string]string, error) {
	if dbModel.TxFromContext(ctx) == nil {
		return nil, errors.New("locking profile references needs a transaction")
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, fmt.Errorf("failed to get the database client to lock profile references: %w", err)
	}
	defer func() { _ = dbClient.Close() }()

	ordered := append([]string(nil), profileIDs...)
	sort.Strings(ordered)
	statuses := make(map[string]string, len(ordered))
	for _, profileID := range ordered {
		results, err := dbClient.ExecuteQueryContext(ctx, scripts.LockProfileReference, profileID, orgID)
		if err != nil {
			return nil, fmt.Errorf("failed to lock the reference of profile %s: %w", profileID, err)
		}
		if len(results) == 0 {
			return nil, fmt.Errorf("profile %s: %w", profileID, ErrProfileReferenceNotFound)
		}
		statuses[profileID] = fmt.Sprintf("%v", results[0]["profile_status"])
	}
	return statuses, nil
}
