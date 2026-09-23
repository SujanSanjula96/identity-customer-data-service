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

	"github.com/wso2/identity-customer-data-service/internal/system/database/client"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
)

// queryRunner runs one statement, either on the shared pool or inside a
// transaction. A store function that reads or writes through it serves both,
// so the merge path needs no second copy of any statement.
type queryRunner interface {
	run(ctx context.Context, query model.DBQuery, args ...interface{}) ([]map[string]interface{}, error)
}

// poolRunner runs a statement on the shared pool. Each statement commits on its
// own, which is what a single read or a single write needs.
type poolRunner struct {
	client client.DBClientInterface
}

func (r poolRunner) run(ctx context.Context, query model.DBQuery, args ...interface{}) (
	[]map[string]interface{}, error) {

	return r.client.ExecuteQueryContext(ctx, query, args...)
}

// txRunner runs a statement inside one transaction, so that nothing it writes
// is visible until that transaction commits.
type txRunner struct {
	tx *model.Tx
}

func (r txRunner) run(ctx context.Context, query model.DBQuery, args ...interface{}) (
	[]map[string]interface{}, error) {

	return r.tx.QueryRowsContext(ctx, query, args...)
}

// mergeTxKey identifies the transaction a merge carries in its context.
type mergeTxKey struct{}

// runnerFor returns the runner the context asks for: the transaction of the
// merge that is in progress, or the shared pool when there is none.
func runnerFor(ctx context.Context) (queryRunner, error) {

	if tx, ok := ctx.Value(mergeTxKey{}).(*model.Tx); ok {
		return txRunner{tx: tx}, nil
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return nil, err
	}
	return poolRunner{client: dbClient}, nil
}

// inTransaction runs fn inside one transaction. When the context already
// carries one, fn joins it rather than committing on its own, so a write made
// during a merge lands with the rest of that merge.
func inTransaction(ctx context.Context, fn func(ctx context.Context) error) error {

	return withTransaction(ctx, func(ctx context.Context, _ *model.Tx) error {
		return fn(ctx)
	})
}

// withTransaction is inTransaction for the caller that needs the transaction
// itself, and not only the context that carries it.
func withTransaction(ctx context.Context, fn func(ctx context.Context, tx *model.Tx) error) error {

	if tx, ok := ctx.Value(mergeTxKey{}).(*model.Tx); ok {
		return fn(ctx, tx)
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return err
	}
	defer dbClient.Close()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		return err
	}

	if err := fn(context.WithValue(ctx, mergeTxKey{}, tx), tx); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return fmt.Errorf("%w (the transaction could not be rolled back either: %v)", err, rollbackErr)
		}
		return err
	}

	return tx.Commit()
}

// WithProfileMerge runs fn as one unit of work over the profiles in
// profileIds. Either everything fn writes is in the store, or none of it is.
//
// Two things make that true. Every store call that takes the context fn is
// given runs inside one transaction, which commits when fn returns nil and
// rolls back when fn returns an error. And the profile rows are locked before
// fn starts, in profile identifier order, so two instances that merge the same
// profiles run one after the other rather than at the same time. The second
// one then reads what the first one wrote, instead of overwriting it.
//
// The lock is what the datasource offers. PostgreSQL locks the rows the merge
// touches. The inbuilt database takes its write lock when a transaction begins,
// so a merge there is already alone.
func WithProfileMerge(ctx context.Context, profileIds []string, fn func(ctx context.Context) error) error {

	return withTransaction(ctx, func(ctx context.Context, tx *model.Tx) error {
		if err := lockProfiles(ctx, tx, profileIds); err != nil {
			return err
		}
		return fn(ctx)
	})
}

// lockProfiles takes a lock on each profile row, in identifier order. The order
// is what keeps two instances that lock the same pair from holding one row each
// and waiting for the other.
func lockProfiles(ctx context.Context, tx *model.Tx, profileIds []string) error {

	ordered := make([]string, 0, len(profileIds))
	seen := make(map[string]bool, len(profileIds))
	for _, profileId := range profileIds {
		if profileId == "" || seen[profileId] {
			continue
		}
		seen[profileId] = true
		ordered = append(ordered, profileId)
	}
	if len(ordered) == 0 {
		return nil
	}
	sort.Strings(ordered)

	placeholders := make([]string, len(ordered))
	args := make([]interface{}, len(ordered))
	for i, profileId := range ordered {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = profileId
	}

	query := scripts.LockProfilesById.Format(strings.Join(placeholders, ", "))
	if _, err := tx.QueryRowsContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to lock the profiles %s for merging: %w",
			strings.Join(ordered, ", "), err)
	}

	return nil
}
