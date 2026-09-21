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
	"database/sql/driver"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
)

// txRecorder decides how a transaction ends and counts how it ended. A real
// datasource cannot be made to fail a rollback on demand, and the way
// WithTransaction completes a transaction is exactly what has to be tested.
type txRecorder struct {
	beginErr    error
	commitErr   error
	rollbackErr error

	commits   atomic.Int64
	rollbacks atomic.Int64
}

// openRecordedDB returns a pool whose transactions behave as recorder says.
func openRecordedDB(t *testing.T, recorder *txRecorder) DBClientInterface {

	t.Helper()

	name := fmt.Sprintf("cds-recorded-%d", driverCount.Add(1))
	sql.Register(name, &recordedDriver{recorder: recorder})

	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return NewSharedDBClient(db, database.TypeSQLite)
}

// driverCount keeps each test's driver name unique, because sql.Register
// panics on a name it already holds.
var driverCount atomic.Int64

type recordedDriver struct {
	recorder *txRecorder
}

func (d *recordedDriver) Open(string) (driver.Conn, error) {

	return &recordedConn{recorder: d.recorder}, nil
}

type recordedConn struct {
	recorder *txRecorder
}

func (c *recordedConn) Prepare(string) (driver.Stmt, error) {

	return nil, fmt.Errorf("the recorded connection runs no statements")
}

func (c *recordedConn) Close() error {

	return nil
}

func (c *recordedConn) Begin() (driver.Tx, error) {

	if c.recorder.beginErr != nil {
		return nil, c.recorder.beginErr
	}
	return &recordedTx{recorder: c.recorder}, nil
}

func (c *recordedConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {

	return c.Begin()
}

type recordedTx struct {
	recorder *txRecorder
}

func (t *recordedTx) Commit() error {

	t.recorder.commits.Add(1)
	return t.recorder.commitErr
}

func (t *recordedTx) Rollback() error {

	t.recorder.rollbacks.Add(1)
	return t.recorder.rollbackErr
}
