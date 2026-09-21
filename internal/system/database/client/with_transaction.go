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

package client

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
)

// TransactionFunc runs the statements of one transaction. Returning an error
// rolls the transaction back; returning nil commits it.
type TransactionFunc func(tx *model.Tx) error

// WithTransaction runs fn inside one transaction and completes that
// transaction exactly once, whichever way fn ends.
//
// It is a package function rather than a method on DBClientInterface, so that
// the interface stays as it is and every mock of it keeps working.
//
// The errors it returns are plain wrapped Go errors. A store converts them to
// the error its own API promises. When fn fails and the rollback fails too,
// both errors are returned joined, so errors.Is and errors.As still find the
// one fn produced.
func WithTransaction(ctx context.Context, dbClient DBClientInterface, fn TransactionFunc) error {

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	if err := runTransactionFunc(tx, fn); err != nil {
		if rollbackErr := rollback(tx); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// runTransactionFunc calls fn and turns a panic inside it into a rollback and
// the same panic again.
//
// A panic is a defect, not a failure the caller asked about, so it is not
// converted to an error. The transaction is still released first, because the
// connection it holds belongs to a pool the rest of the process shares. A
// rollback that fails during a panic cannot be reported, because there is no
// caller left to report it to.
func runTransactionFunc(tx *model.Tx, fn TransactionFunc) (err error) {

	defer func() {
		if panicValue := recover(); panicValue != nil {
			_ = tx.Rollback()
			panic(panicValue)
		}
	}()

	return fn(tx)
}

// rollback reports a rollback that failed for a real reason. A transaction
// that already ended returns sql.ErrTxDone, which means the connection is
// back in the pool and there is nothing to report.
func rollback(tx *model.Tx) error {

	err := tx.Rollback()
	if err == nil || errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return fmt.Errorf("failed to roll back transaction: %w", err)
}
