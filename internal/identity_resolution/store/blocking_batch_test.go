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
	"fmt"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// postgresBindParameterLimit is the ceiling the wire protocol imposes on one statement.
const postgresBindParameterLimit = 65535

// TestBatchChunkStaysUnderTheParameterLimit is the guard for a silent-data-loss bug.
//
// The backfill assembles a whole page of profiles into one INSERT at five bind parameters
// per row. A 500-profile page crosses the protocol's 65535-parameter ceiling once profiles
// average about 26 keys each, which ordinary data reaches — four email addresses under a
// fuzzy rule is 20 keys before a fuzzy name adds 7 more. The statement is then rejected
// outright, and since the backfill logs and continues, those profiles never enter the index.
func TestBatchChunkStaysUnderTheParameterLimit(t *testing.T) {
	if maxBlockingKeyRowsPerInsert*blockingKeyColumns >= postgresBindParameterLimit {
		t.Errorf("a full chunk binds %d parameters, at or over the %d limit",
			maxBlockingKeyRowsPerInsert*blockingKeyColumns, postgresBindParameterLimit)
	}
}

// TestFlattenCoversEveryKey checks the chunkable slice loses nothing from the map.
func TestFlattenCoversEveryKey(t *testing.T) {
	perProfile := map[string][]model.BlockingKey{
		"p1": {{AttributeName: "a", KeyValue: "1"}, {AttributeName: "a", KeyValue: "2"}},
		"p2": {{AttributeName: "b", KeyValue: "3"}},
		"p3": {},
	}

	rows := flattenBlockingKeyRows(perProfile)
	if len(rows) != 3 {
		t.Fatalf("flattened %d rows, want 3", len(rows))
	}

	seen := map[string]int{}
	for _, row := range rows {
		seen[row.profileID]++
		if row.key.KeyValue == "" {
			t.Error("a flattened row lost its key value")
		}
	}
	if seen["p1"] != 2 || seen["p2"] != 1 {
		t.Errorf("rows per profile = %v, want p1:2 p2:1", seen)
	}
	if _, present := seen["p3"]; present {
		t.Error("a profile with no keys should contribute no rows")
	}
}

func TestFlattenHandlesEmptyInput(t *testing.T) {
	if rows := flattenBlockingKeyRows(nil); rows != nil {
		t.Errorf("nil map should flatten to nothing, got %d rows", len(rows))
	}
	if rows := flattenBlockingKeyRows(map[string][]model.BlockingKey{"p": {}}); rows != nil {
		t.Errorf("a map of empty slices should flatten to nothing, got %d rows", len(rows))
	}
}

// TestRealisticPageExceedsOneChunk demonstrates the situation the chunking exists for: a
// backfill page of profiles carrying multi-valued attributes under fuzzy rules produces
// more rows than one statement may bind.
func TestRealisticPageExceedsOneChunk(t *testing.T) {
	// A page of profiles, each with four email addresses under a fuzzy rule (5 keys each)
	// and a fuzzy name (7 keys) — 27 keys per profile.
	const keysPerProfile = 27
	perProfile := make(map[string][]model.BlockingKey, constants.GetProfilesPageSize)
	for p := 0; p < constants.GetProfilesPageSize; p++ {
		keys := make([]model.BlockingKey, keysPerProfile)
		for k := range keys {
			keys[k] = model.BlockingKey{AttributeName: "traits.email", KeyValue: fmt.Sprintf("v%d-%d", p, k)}
		}
		perProfile[fmt.Sprintf("profile-%d", p)] = keys
	}

	rows := flattenBlockingKeyRows(perProfile)
	if rows == nil {
		t.Fatal("expected rows")
	}

	if len(rows)*blockingKeyColumns <= postgresBindParameterLimit {
		t.Skip("this page no longer exceeds the limit; the scenario needs revisiting")
	}

	chunks := (len(rows) + maxBlockingKeyRowsPerInsert - 1) / maxBlockingKeyRowsPerInsert
	if chunks < 2 {
		t.Errorf("%d rows should be split across more than one statement, got %d", len(rows), chunks)
	}
	for start := 0; start < len(rows); start += maxBlockingKeyRowsPerInsert {
		end := start + maxBlockingKeyRowsPerInsert
		if end > len(rows) {
			end = len(rows)
		}
		if (end-start)*blockingKeyColumns >= postgresBindParameterLimit {
			t.Errorf("chunk %d-%d binds too many parameters", start, end)
		}
	}
}
