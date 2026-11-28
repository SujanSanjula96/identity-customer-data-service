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

// Package model holds the types the database client and the query layer share:
// a statement in every supported dialect, and a transaction that knows which
// dialect it runs against.
package model

import "fmt"

// DBQuery is a statement in every supported dialect.
type DBQuery struct {
	// ID identifies the statement in error messages.
	ID string
	// Query is the PostgreSQL statement, and the fallback for any dialect
	// without an override.
	Query string
	// Overrides maps a datasource type (the database.Type* values) to a
	// statement that replaces Query for that datasource. A missing or empty
	// entry means the PostgreSQL statement works as written there.
	Overrides map[string]string
}

// GetQuery returns the statement for dbType. A populated DBQuery never resolves
// to an empty statement.
func (q DBQuery) GetQuery(dbType string) string {
	if override := q.Overrides[dbType]; override != "" {
		return override
	}
	return q.Query
}

// Format applies fmt.Sprintf to every dialect variant, for statements completed
// at runtime such as a VALUES or IN list.
//
// An absent override stays absent, so the statement keeps falling back to the
// PostgreSQL text.
func (q DBQuery) Format(args ...interface{}) DBQuery {
	return q.mapVariants(func(text string) string {
		return fmt.Sprintf(text, args...)
	})
}

// Append concatenates a fragment onto every dialect variant, for statements
// built by concatenation rather than Sprintf. It preserves the fallback the
// same way Format does.
func (q DBQuery) Append(suffix string) DBQuery {
	return q.mapVariants(func(text string) string {
		return text + suffix
	})
}

// mapVariants returns a copy of q with transform applied to the base statement
// and to every override. The copy never shares its map with q, and an absent
// override stays absent.
func (q DBQuery) mapVariants(transform func(string) string) DBQuery {
	mapped := DBQuery{
		ID:    q.ID,
		Query: transform(q.Query),
	}
	for dbType, override := range q.Overrides {
		if override == "" {
			continue
		}
		if mapped.Overrides == nil {
			mapped.Overrides = make(map[string]string, len(q.Overrides))
		}
		mapped.Overrides[dbType] = transform(override)
	}
	return mapped
}

// WithOverride returns a copy of q that runs sqlText on dbType, for a statement
// whose text depends on its arguments, such as the length of a path. The copy
// does not share its map with q.
func (q DBQuery) WithOverride(dbType, sqlText string) DBQuery {
	overrides := make(map[string]string, len(q.Overrides)+1)
	for key, value := range q.Overrides {
		overrides[key] = value
	}
	overrides[dbType] = sqlText
	q.Overrides = overrides
	return q
}

// WithSQL returns a copy of q whose text is sqlText for every dialect, for the
// few statements a store assembles from dialect-specific fragments. The dialect
// is already applied by then, so there is nothing left to select.
func (q DBQuery) WithSQL(sqlText string) DBQuery {
	return DBQuery{
		ID:    q.ID,
		Query: sqlText,
	}
}
