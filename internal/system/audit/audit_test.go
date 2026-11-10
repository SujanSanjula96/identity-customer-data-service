/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
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

package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wso2/identity-customer-data-service/internal/system/httpmw"
	"github.com/wso2/identity-customer-data-service/internal/system/reqctx"
)

// captureOutput sends the audit records to out and returns a function that restores the earlier
// state.
func captureOutput(out io.Writer, readEvents bool) (restore func()) {
	current.mu.Lock()
	defer current.mu.Unlock()
	prevOut, prevCloser, prevEnabled, prevRead := current.out, current.closer, current.enabled, current.readEvents
	current.out, current.closer, current.enabled, current.readEvents = out, nil, true, readEvents
	return func() {
		current.mu.Lock()
		defer current.mu.Unlock()
		current.out, current.closer, current.enabled, current.readEvents = prevOut, prevCloser, prevEnabled, prevRead
	}
}

// serve sends one request through the request context middleware and a route that Wrap audits.
// It returns the audit records that the request wrote.
func serve(t *testing.T, readEvents bool, method, pattern, target string, event Event,
	handler http.HandlerFunc) []map[string]any {

	t.Helper()
	var out bytes.Buffer
	defer captureOutput(&out, readEvents)()

	mux := http.NewServeMux()
	mux.HandleFunc(method+" "+pattern, Wrap(event, handler))
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set(httpmw.CorrelationIDHeader, "corr-1")
	request.Header.Set("User-Agent", "test-agent")
	httpmw.RequestContext(mux).ServeHTTP(httptest.NewRecorder(), request)

	var records []map[string]any
	decoder := json.NewDecoder(&out)
	for decoder.More() {
		var record map[string]any
		require.NoError(t, decoder.Decode(&record))
		records = append(records, record)
	}
	return records
}

func authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info := reqctx.From(r.Context())
		info.SetOrg("carbon.super", "")
		info.SetActor(reqctx.Actor{Type: reqctx.ActorUser, ID: "user-1", ClientID: "console"})
		next(w, r)
	}
}

func TestWrapRecordsSucceededWrite(t *testing.T) {
	records := serve(t, false, "DELETE", "/profiles/{profileId}", "/profiles/p-1?token=secret", ProfileDelete,
		authenticated(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	require.Len(t, records, 1)
	record := records[0]
	assert.Equal(t, "audit", record["log_type"])
	assert.EqualValues(t, SchemaVersion, record["schema_version"])
	assert.NotEmpty(t, record["id"])
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`, record["time"])
	assert.Equal(t, "profile.delete", record["event"])
	assert.Equal(t, "delete", record["action"])
	assert.Equal(t, "corr-1", record["correlation_id"])
	assert.Equal(t, map[string]any{"handle": "carbon.super"}, record["org"])
	assert.Equal(t, map[string]any{"type": "user", "id": "user-1", "client_id": "console"}, record["actor"])
	assert.Equal(t, map[string]any{"type": "profile", "id": "p-1"}, record["target"])
	assert.Equal(t, map[string]any{"result": "succeeded", "status": float64(204)}, record["outcome"])
	assert.Equal(t, "test-agent", record["client"].(map[string]any)["user_agent"])
	assert.NotContains(t, record, "details")
}

func TestWrapRecordsHandlerDetails(t *testing.T) {
	records := serve(t, false, "POST", "/profile-schema/{scope}", "/profile-schema/traits",
		ProfileSchemaAttributeCreate,
		authenticated(func(w http.ResponseWriter, r *http.Request) {
			SetTargetID(r.Context(), "attr-9")
			AddDetail(r.Context(), "attribute_count", 3)
			w.WriteHeader(http.StatusCreated)
		}))

	require.Len(t, records, 1)
	assert.Equal(t, map[string]any{"type": "profile_schema_attribute", "id": "attr-9"}, records[0]["target"])
	assert.Equal(t, map[string]any{"scope": "traits", "attribute_count": float64(3)}, records[0]["details"])
}

func TestWrapRecordsDeniedAndFailedAttempts(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		errorCode string
		outcome   map[string]any
	}{
		{"unauthenticated", http.StatusUnauthorized, "CDS-11001",
			map[string]any{"result": "denied", "reason": "unauthenticated", "status": float64(401)}},
		{"forbidden", http.StatusForbidden, "CDS-11002",
			map[string]any{"result": "denied", "reason": "forbidden", "status": float64(403)}},
		{"client error with a code", http.StatusNotFound, "CDS-14004",
			map[string]any{"result": "failed", "reason": "CDS-14004", "status": float64(404)}},
		{"client error without a code", http.StatusBadRequest, "",
			map[string]any{"result": "failed", "reason": "client_error", "status": float64(400)}},
		{"server error without a code", http.StatusInternalServerError, "",
			map[string]any{"result": "failed", "reason": "server_error", "status": float64(500)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records := serve(t, false, "PATCH", "/profiles/{profileId}", "/profiles/p-1", ProfileUpdate,
				func(w http.ResponseWriter, _ *http.Request) {
					if test.errorCode != "" {
						w.(interface{ RecordErrorCode(string) }).RecordErrorCode(test.errorCode)
					}
					w.WriteHeader(test.status)
				})

			require.Len(t, records, 1)
			assert.Equal(t, test.outcome, records[0]["outcome"])
			assert.Equal(t, map[string]any{"type": "anonymous"}, records[0]["actor"])
		})
	}
}

func TestReadEventRules(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	denied := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }

	assert.Empty(t, serve(t, false, "GET", "/profiles/{profileId}", "/profiles/p-1", ProfileRead, ok),
		"a read that succeeded is not recorded by default")
	assert.Len(t, serve(t, true, "GET", "/profiles/{profileId}", "/profiles/p-1", ProfileRead, ok), 1,
		"a read that succeeded is recorded when read events are on")
	assert.Len(t, serve(t, false, "GET", "/profiles/{profileId}", "/profiles/p-1", ProfileRead, denied), 1,
		"a denied read is always recorded")
}

func TestLogRecordsSystemOperation(t *testing.T) {
	var out bytes.Buffer
	defer captureOutput(&out, false)()

	Log(context.Background(), Entry{
		Event:    ProfileMerge,
		TargetID: "p-master",
		Outcome:  Succeeded(),
		OrgID:    "org-1",
		Details:  map[string]any{"merged_profile_ids": []string{"p-1", "p-2"}, "reason": "email"},
	})

	var record map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &record))
	assert.Equal(t, "profile.merge", record["event"])
	assert.Equal(t, map[string]any{"type": "system", "id": SystemActorID}, record["actor"])
	assert.Equal(t, map[string]any{"id": "org-1"}, record["org"])
	assert.Equal(t, map[string]any{"result": "succeeded"}, record["outcome"])
	assert.NotContains(t, record, "client")
	assert.NotContains(t, record, "correlation_id")
}

func TestDisabledAuditWritesNothing(t *testing.T) {
	var out bytes.Buffer
	defer captureOutput(&out, false)()
	current.mu.Lock()
	current.enabled = false
	current.mu.Unlock()

	Log(context.Background(), Entry{Event: ProfileMerge, Outcome: Succeeded()})

	assert.Empty(t, out.String())
}

func TestEventNamesFollowTheNamingRule(t *testing.T) {
	assert.Equal(t, "profile_consent.update", ProfileConsentUpdate.Name())
	assert.True(t, ProfileList.IsRead())
	assert.False(t, ProfileSync.IsRead())
}
