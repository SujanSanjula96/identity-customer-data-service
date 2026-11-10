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

// Package httpmw holds the HTTP middleware that every request passes: the request context
// middleware, which sets the correlation ID, and the access log middleware.
package httpmw

import "net/http"

// Recorder wraps a http.ResponseWriter and records the status, the size, and the error code of
// the response.
type Recorder struct {
	http.ResponseWriter
	status      int
	size        int
	errorCode   string
	wroteHeader bool
}

// NewRecorder wraps w. When w is a Recorder already, it returns w, so that one request has one
// Recorder.
func NewRecorder(w http.ResponseWriter) *Recorder {
	if rec, ok := w.(*Recorder); ok {
		return rec
	}
	return &Recorder{ResponseWriter: w, status: http.StatusOK}
}

// WriteHeader records the status code.
func (r *Recorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(status)
}

// Write records the size of the response.
func (r *Recorder) Write(p []byte) (int, error) {
	r.wroteHeader = true
	n, err := r.ResponseWriter.Write(p)
	r.size += n
	return n, err
}

// Flush sends buffered data to the client, when the wrapped writer supports it.
func (r *Recorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap returns the wrapped writer, for http.ResponseController.
func (r *Recorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// RecordErrorCode records the CDS error code of an error response. The error helper calls it, so
// that the access log and the audit log can report why a request failed.
func (r *Recorder) RecordErrorCode(code string) {
	r.errorCode = code
}

// Status returns the status code of the response.
func (r *Recorder) Status() int {
	return r.status
}

// Size returns the number of body bytes written.
func (r *Recorder) Size() int {
	return r.size
}

// ErrorCode returns the CDS error code of the response, or empty when there is none.
func (r *Recorder) ErrorCode() string {
	return r.errorCode
}
