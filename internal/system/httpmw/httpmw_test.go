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

package httpmw

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/log/rollingfile"
	"github.com/wso2/identity-customer-data-service/internal/system/reqctx"
)

func TestRequestContextCorrelationID(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		value    string
		accepted bool
	}{
		{"valid ID from the client", CorrelationIDHeader, "abc-123_x.y", true},
		{"valid ID in the alternative header", "X-Request-ID", "req-7", true},
		{"no ID", "", "", false},
		{"unsafe characters", CorrelationIDHeader, "bad id\"}", false},
		{"too long", CorrelationIDHeader, string(make([]byte, 65)), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var seen string
			handler := RequestContext(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen = reqctx.From(r.Context()).CorrelationID()
			}))
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			if test.header != "" {
				request.Header.Set(test.header, test.value)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			require.NotEmpty(t, seen)
			assert.Equal(t, seen, response.Header().Get(CorrelationIDHeader), "the response returns the ID")
			if test.accepted {
				assert.Equal(t, test.value, seen)
			} else {
				assert.NotEqual(t, test.value, seen)
				assert.Len(t, seen, 36, "CDS creates a UUID")
			}
		})
	}
}

func accessRecords(t *testing.T, handler http.HandlerFunc, target string) []map[string]any {
	t.Helper()
	path := filepath.Join(t.TempDir(), "http_access.log")
	logger, err := log.New(log.TypeAccess, log.Options{
		Format: log.FormatJSON,
		Output: log.OutputOptions{File: &rollingfile.Config{Path: path}},
	})
	require.NoError(t, err)
	defer func() { _ = logger.Close() }()

	request := httptest.NewRequest(http.MethodPost, target, nil)
	request.Header.Set(CorrelationIDHeader, "corr-1")
	request.RemoteAddr = "192.0.2.10:4711"
	RequestContext(AccessLog(logger, handler)).ServeHTTP(httptest.NewRecorder(), request)

	file, err := os.Open(path) // #nosec G304 -- the test reads a file in its temporary directory.
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	var records []map[string]any
	decoder := json.NewDecoder(file)
	for decoder.More() {
		var record map[string]any
		require.NoError(t, decoder.Decode(&record))
		records = append(records, record)
	}
	return records
}

func TestAccessLogRecord(t *testing.T) {
	records := accessRecords(t, func(w http.ResponseWriter, r *http.Request) {
		reqctx.From(r.Context()).SetOrg("carbon.super", "")
		// A later stage can change the path. The access log reports the path of the request.
		r.URL.Path = "/changed"
		w.(interface{ RecordErrorCode(string) }).RecordErrorCode("CDS-14004")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("12345"))
	}, "/t/carbon.super/cds/api/v1/profiles?filter=email+eq+a@b.c")

	require.Len(t, records, 1)
	record := records[0]
	assert.Equal(t, log.TypeAccess, record[log.KeyLogType])
	assert.Equal(t, "POST", record["method"])
	assert.Equal(t, "/t/carbon.super/cds/api/v1/profiles", record["path"], "the query string is not logged")
	assert.EqualValues(t, http.StatusNotFound, record["status"])
	assert.EqualValues(t, 5, record["bytes"])
	assert.Equal(t, "192.0.2.10", record["client_ip"])
	assert.Equal(t, "CDS-14004", record["error_code"])
	assert.Equal(t, "corr-1", record[log.KeyCorrelationID])
	assert.Equal(t, "carbon.super", record[log.KeyOrg])
	assert.Contains(t, record, "duration_ms")
}

func TestAccessLogSkipsHealthyProbes(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	failed := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }

	assert.Empty(t, accessRecords(t, ok, "/cds/api/v1/health"))
	assert.Empty(t, accessRecords(t, ok, "/cds/api/v1/ready"))
	assert.Len(t, accessRecords(t, failed, "/cds/api/v1/ready"), 1, "a failed probe is logged")
}
