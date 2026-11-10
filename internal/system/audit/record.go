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

import "net/http"

// SchemaVersion is the version of the audit record. It changes only when a field is removed or
// when the meaning of a field changes. A new optional field does not change it.
const SchemaVersion = 1

// Result is how an attempt ended.
type Result string

const (
	// ResultSucceeded is an attempt that completed.
	ResultSucceeded Result = "succeeded"
	// ResultDenied is an attempt that authentication or authorization refused.
	ResultDenied Result = "denied"
	// ResultFailed is an attempt that was permitted but did not complete.
	ResultFailed Result = "failed"
)

// Outcome reasons that do not come from a CDS error code.
const (
	ReasonUnauthenticated = "unauthenticated"
	ReasonForbidden       = "forbidden"
	ReasonClientError     = "client_error"
	ReasonServerError     = "server_error"
)

// Outcome is how an attempt ended. Reason is a stable code, never free text. It is empty when the
// attempt succeeded.
type Outcome struct {
	Result Result `json:"result"`
	Reason string `json:"reason,omitempty"`
	// Status is the HTTP status of the response. It is zero for an operation with no request.
	Status int `json:"status,omitempty"`
}

// Succeeded is the outcome of an attempt that completed.
func Succeeded() Outcome {
	return Outcome{Result: ResultSucceeded}
}

// Failed is the outcome of an attempt that did not complete, with a stable reason code.
func Failed(reason string) Outcome {
	return Outcome{Result: ResultFailed, Reason: reason}
}

// outcomeOf derives the outcome of a request from its response.
func outcomeOf(status int, errorCode string) Outcome {
	switch {
	case status < http.StatusBadRequest:
		return Outcome{Result: ResultSucceeded, Status: status}
	case status == http.StatusUnauthorized:
		return Outcome{Result: ResultDenied, Reason: ReasonUnauthenticated, Status: status}
	case status == http.StatusForbidden:
		return Outcome{Result: ResultDenied, Reason: ReasonForbidden, Status: status}
	case errorCode != "":
		return Outcome{Result: ResultFailed, Reason: errorCode, Status: status}
	case status >= http.StatusInternalServerError:
		return Outcome{Result: ResultFailed, Reason: ReasonServerError, Status: status}
	default:
		return Outcome{Result: ResultFailed, Reason: ReasonClientError, Status: status}
	}
}

// record is the JSON shape of one audit line. log_type is the first field, so a consumer can
// select audit lines from a shared stream.
type record struct {
	LogType       string         `json:"log_type"`
	SchemaVersion int            `json:"schema_version"`
	ID            string         `json:"id"`
	Time          string         `json:"time"`
	Event         string         `json:"event"`
	Action        string         `json:"action"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	Org           *orgRef        `json:"org,omitempty"`
	Actor         actorRef       `json:"actor"`
	Target        targetRef      `json:"target"`
	Outcome       Outcome        `json:"outcome"`
	Client        *clientRef     `json:"client,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
}

type orgRef struct {
	Handle string `json:"handle,omitempty"`
	ID     string `json:"id,omitempty"`
}

type actorRef struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	ClientID string `json:"client_id,omitempty"`
}

type targetRef struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

type clientRef struct {
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
}
