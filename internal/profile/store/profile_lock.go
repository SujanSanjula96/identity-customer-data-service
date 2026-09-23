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
	"errors"
	"fmt"
	"sort"

	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
)

// maxLockAttempts bounds how often WithProfileLocked starts again when the
// profile moves to another master before its lock is taken.
const maxLockAttempts = 3

// errProfileMoved reports a profile whose master changed between the read that
// chose the locks and the lock itself.
var errProfileMoved = errors.New("the profile moved to another master before it was locked")

// RunInTransaction runs fn in one database transaction. Every store call made
// under the context fn receives joins that transaction, so either all of their
// writes are in the database or none of them is.
func RunInTransaction(ctx context.Context, fn func(ctx context.Context) error) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return err
	}
	return dbClient.RunInTransaction(ctx, fn)
}

// WithProfilesLocked runs fn in one transaction that holds a lock on each of
// the profiles. Another caller that locks any of them waits until the
// transaction ends, so it reads what fn wrote instead of overwriting it.
//
// Every caller takes its locks in profile identifier order, so two callers
// cannot each hold a lock the other waits for. A profile that does not exist
// takes no lock.
func WithProfilesLocked(ctx context.Context, profileIds []string, fn func(ctx context.Context) error) error {

	return RunInTransaction(ctx, func(ctx context.Context) error {
		if err := lockProfiles(ctx, profileIds); err != nil {
			return err
		}
		return fn(ctx)
	})
}

// WithProfileLocked runs fn in one transaction that holds the lock on the
// profile and, when the profile is merged into a master, on that master too.
// A write to a profile lands on its master, so this is the lock set that a
// read-modify-write of the profile needs.
func WithProfileLocked(ctx context.Context, profileId string, fn func(ctx context.Context) error) error {

	for attempt := 1; attempt <= maxLockAttempts; attempt++ {
		err := RunInTransaction(ctx, func(ctx context.Context) error {
			masterId, err := masterOf(ctx, profileId)
			if err != nil {
				return err
			}
			if err := lockProfiles(ctx, []string{profileId, masterId}); err != nil {
				return err
			}
			lockedMasterId, err := masterOf(ctx, profileId)
			if err != nil {
				return err
			}
			if lockedMasterId != masterId {
				return errProfileMoved
			}
			return fn(ctx)
		})
		if !errors.Is(err, errProfileMoved) {
			return err
		}
	}
	return fmt.Errorf("profile %s: %w on each of %d attempts", profileId, errProfileMoved, maxLockAttempts)
}

// masterOf returns the identifier of the master the profile is merged into, or
// "" when the profile is a master itself or does not exist.
func masterOf(ctx context.Context, profileId string) (string, error) {

	profile, err := GetProfile(ctx, profileId)
	if err != nil || profile == nil {
		return "", err
	}
	return profile.ProfileStatus.ReferenceProfileId, nil
}

// lockProfiles locks the row of each profile, in identifier order, until the
// transaction that ctx carries ends.
func lockProfiles(ctx context.Context, profileIds []string) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return err
	}

	seen := make(map[string]bool, len(profileIds))
	ordered := make([]string, 0, len(profileIds))
	for _, profileId := range profileIds {
		if profileId != "" && !seen[profileId] {
			seen[profileId] = true
			ordered = append(ordered, profileId)
		}
	}
	sort.Strings(ordered)

	for _, profileId := range ordered {
		if _, err := dbClient.ExecuteQueryContext(ctx, scripts.LockProfile, profileId); err != nil {
			return fmt.Errorf("failed to lock profile %s: %w", profileId, err)
		}
	}
	return nil
}
