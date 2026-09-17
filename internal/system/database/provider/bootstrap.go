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

package provider

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wso2/identity-customer-data-service/dbscripts"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// maxConfigurableSeconds is the largest value a seconds setting may take. One
// year is far above any sensible deadline or connection lifetime, so a larger
// value is a typo. It is also well below the point where the conversion to a
// time.Duration overflows, which would turn the value into a negative duration
// and then into a default.
const maxConfigurableSeconds = 365 * 24 * 60 * 60

// numericSetting is one configuration value and the key an operator sees.
type numericSetting struct {
	key   string
	value int
}

// validateNumericSettings reports every numeric datasource setting CDS cannot
// use.
//
// Zero keeps its meaning: the setting is not configured, so its default
// applies. A negative value is a typo. To turn a typo into the default would
// hide it, and the instance would then run with a capacity nobody chose.
//
// Only the settings that apply to the configured type are read, so PostgreSQL
// values are ignored on the inbuilt database. The Helm chart renders the
// PostgreSQL block whatever the type is.
func validateNumericSettings(ds config.DataSourceConfig) error {

	dbType := database.ResolveType(ds.Type)

	// The deadlines apply to both types, because both bound their pool.
	durations := []numericSetting{
		{"datasource.query_timeout_seconds", ds.QueryTimeoutSeconds},
		{"datasource.tx_timeout_seconds", ds.TxTimeoutSeconds},
	}
	var counts []numericSetting

	if dbType == database.TypeSQLite {
		counts = append(counts,
			numericSetting{"datasource.sqlite.max_open_conns", ds.SQLite.MaxOpenConns})
	} else {
		counts = append(counts,
			numericSetting{"datasource.postgres.max_open_conns", ds.Postgres.MaxOpenConns},
			numericSetting{"datasource.postgres.max_idle_conns", ds.Postgres.MaxIdleConns})
		durations = append(durations,
			numericSetting{"datasource.postgres.conn_max_lifetime_seconds", ds.Postgres.ConnMaxLifetimeSeconds},
			numericSetting{"datasource.postgres.conn_max_idle_time_seconds", ds.Postgres.ConnMaxIdleTimeSeconds},
			numericSetting{"datasource.postgres.connect_timeout_seconds", ds.Postgres.ConnectTimeoutSeconds})
	}

	var problems []string
	for _, group := range [][]numericSetting{counts, durations} {
		for _, setting := range group {
			if setting.value < 0 {
				problems = append(problems, fmt.Sprintf("%s is %d, which is below zero",
					setting.key, setting.value))
			}
		}
	}
	for _, setting := range durations {
		if setting.value > maxConfigurableSeconds {
			problems = append(problems, fmt.Sprintf("%s is %d, which is above the limit of %d seconds",
				setting.key, setting.value, maxConfigurableSeconds))
		}
	}

	// An idle limit above the open limit reserves connections the pool can
	// never hold, so the two settings contradict each other.
	//
	// The comparison is against the limit the pool really uses. An open limit
	// the operator left empty becomes the default, not zero, so an idle limit
	// of 30 with no open limit is rejected rather than lowered to 25 in
	// silence.
	if dbType != database.TypeSQLite && ds.Postgres.MaxIdleConns > 0 {
		openLimit, openSource := ds.Postgres.MaxOpenConns, "datasource.postgres.max_open_conns"
		if openLimit <= 0 {
			openLimit = database.DefaultPostgresMaxOpenConns
			openSource = "the default datasource.postgres.max_open_conns"
		}
		if ds.Postgres.MaxIdleConns > openLimit {
			problems = append(problems, fmt.Sprintf(
				"datasource.postgres.max_idle_conns is %d, which is above %s of %d",
				ds.Postgres.MaxIdleConns, openSource, openLimit))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid datasource settings: %s", strings.Join(problems, "; "))
	}

	return nil
}

// ValidateDataSource reports whether the datasource configuration is one CDS
// can run on. The server refuses to start when it is not.
func ValidateDataSource(ds config.DataSourceConfig) error {

	dbType := database.ResolveType(ds.Type)
	if !database.IsSupportedType(dbType) {
		return fmt.Errorf("unsupported datasource.type %q: supported types are %s",
			ds.Type, strings.Join(database.SupportedTypes, ", "))
	}

	if err := validateNumericSettings(ds); err != nil {
		return err
	}

	// The inbuilt database needs no connection settings.
	if dbType == database.TypeSQLite {
		return nil
	}

	var missing []string
	for _, setting := range []struct {
		name  string
		value string
	}{
		{"hostname", ds.Hostname},
		{"username", ds.Username},
		{"password", ds.Password},
		{"name", ds.Name},
	} {
		if setting.value == "" {
			missing = append(missing, "datasource."+setting.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("datasource.type is %q but these settings are missing: %s",
			dbType, strings.Join(missing, ", "))
	}

	return nil
}

// EnsureDatabase prepares the configured datasource for use.
//
// For the inbuilt datasource it creates the database file and applies the
// schema. For PostgreSQL it opens the shared connection pool, which verifies
// that the server answers within the connect timeout, so a wrong setting fails
// at start rather than on the first request. The PostgreSQL schema itself is
// applied by the operator. It is safe to call more than once.
func EnsureDatabase() error {

	runtimeConfig := config.GetCDSRuntime().Config
	if database.ResolveType(runtimeConfig.DataSource.Type) != database.TypeSQLite {
		// Opening the pool verifies that the server answers, within the
		// configured connect timeout, so a wrong setting or an unreachable
		// host fails the start rather than the first request.
		if _, err := getPostgresDB(); err != nil {
			return err
		}
		return nil
	}

	// Opening the handle creates the file and applies the schema.
	if _, err := getSQLiteDB(); err != nil {
		return err
	}

	path, err := resolveSQLitePath(runtimeConfig.DataSource.SQLite.Path)
	if err != nil {
		return err
	}
	log.GetLogger().Info(fmt.Sprintf("Inbuilt database initialized at %s", path))

	return nil
}

// ensureSQLiteDir creates the directory that holds the inbuilt database file.
func ensureSQLiteDir(configuredPath string) error {

	path, err := resolveSQLitePath(configuredPath)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("failed to create the inbuilt database directory %s: %v", dir, err)
	}

	return nil
}

// initializeSQLiteSchema applies the embedded schema. The script is idempotent,
// so applying it to an existing database is a no-op.
func initializeSQLiteSchema(db *sql.DB) error {

	// A transaction keeps a partial failure from leaving a half-built database.
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin the inbuilt database schema transaction: %v", err)
	}

	if _, err := tx.Exec(dbscripts.SQLiteSchema); err != nil {
		_ = tx.Rollback()

		// Fall back to statement-by-statement execution if the driver rejects
		// a multi-statement script.
		if fallbackErr := applySchemaStatements(db, dbscripts.SQLiteSchema); fallbackErr != nil {
			return fmt.Errorf("failed to apply the inbuilt database schema: %v (%v)", err, fallbackErr)
		}
		return nil
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit the inbuilt database schema: %v", err)
	}

	return nil
}

// applySchemaStatements executes a DDL script one statement at a time.
// Comments are stripped first: a semicolon inside one would split the script.
func applySchemaStatements(db *sql.DB, script string) error {

	for _, statement := range strings.Split(stripSQLComments(script), ";") {
		trimmed := strings.TrimSpace(statement)
		if trimmed == "" {
			continue
		}
		if _, err := db.Exec(trimmed); err != nil {
			return fmt.Errorf("failed to execute %q: %v", truncate(trimmed, 80), err)
		}
	}

	return nil
}

// stripSQLComments removes whole-line `--` comments from a statement.
func stripSQLComments(statement string) string {

	var builder strings.Builder
	for _, line := range strings.Split(statement, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		builder.WriteString(line)
		builder.WriteString("\n")
	}

	return builder.String()
}

// truncate shortens a string for error messages.
func truncate(value string, max int) string {

	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}
