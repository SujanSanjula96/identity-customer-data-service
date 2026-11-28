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

package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/sijms/go-ora/v2/network"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

func TestIsTransient(t *testing.T) {
	for name, tc := range map[string]struct {
		err       error
		transient bool
	}{
		"nil":                          {nil, false},
		"other error":                  {errors.New("boom"), false},
		"bad connection":               {driver.ErrBadConn, true},
		"postgres deadlock":            {&pq.Error{Code: "40P01"}, true},
		"postgres serialization":       {&pq.Error{Code: "40001"}, true},
		"postgres lock not available":  {&pq.Error{Code: "55P03"}, true},
		"postgres connection failure":  {&pq.Error{Code: "08006"}, true},
		"postgres unique violation":    {&pq.Error{Code: "23505"}, false},
		"mysql lock wait timeout":      {&mysql.MySQLError{Number: 1205}, true},
		"mysql deadlock":               {&mysql.MySQLError{Number: 1213}, true},
		"mysql duplicate entry":        {&mysql.MySQLError{Number: 1062}, false},
		"mssql deadlock victim":        {mssql.Error{Number: 1205}, true},
		"mssql lock timeout":           {mssql.Error{Number: 1222}, true},
		"mssql duplicate key":          {mssql.Error{Number: 2627}, false},
		"oracle deadlock":              {&network.OracleError{ErrCode: 60}, true},
		"oracle unique constraint":     {&network.OracleError{ErrCode: 1}, false},
		"wrapped by a store":           {fmt.Errorf("query CDS-PRF-26 failed: %w", &pq.Error{Code: "40P01"}), true},
		"wrapped in a server error":    {errors2.NewServerError(errors2.ErrorMessage{}, driver.ErrBadConn), true},
		"server error of another kind": {errors2.NewServerError(errors2.ErrorMessage{}, errors.New("boom")), false},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.transient, IsTransient(tc.err))
		})
	}
}

// TestIsTransientSQLiteBusy covers the error SQLite returns when busy_timeout runs out while another
// connection holds the write lock.
func TestIsTransientSQLiteBusy(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "busy.db") + "?_pragma=busy_timeout(10)"
	holder, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Close() })
	waiter, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = waiter.Close() })

	_, err = holder.Exec(`CREATE TABLE t (id INTEGER)`)
	require.NoError(t, err)
	ctx := context.Background()
	conn, err := holder.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = conn.ExecContext(ctx, `ROLLBACK`) })

	_, err = waiter.Exec(`INSERT INTO t VALUES (1)`)

	require.Error(t, err)
	require.True(t, IsTransient(fmt.Errorf("insert: %w", err)), err.Error())
}
