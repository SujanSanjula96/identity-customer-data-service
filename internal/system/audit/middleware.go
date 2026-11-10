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
	"context"
	"net/http"
	"sync"

	"github.com/wso2/identity-customer-data-service/internal/system/httpmw"
	"github.com/wso2/identity-customer-data-service/internal/system/reqctx"
)

// scope collects what a handler adds to the audit record of its request.
type scope struct {
	mu       sync.Mutex
	targetID string
	details  map[string]any
}

type scopeKey struct{}

// Wrap returns a handler that runs next and then writes the audit record of event. The record is
// written for every outcome, so a denied or failed attempt is also in the audit log.
func Wrap(event Event, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc := &scope{}
		rec := httpmw.NewRecorder(w)
		r = r.WithContext(context.WithValue(r.Context(), scopeKey{}, sc))

		next(rec, r)

		info := reqctx.From(r.Context())
		handle, orgID := info.Org()

		sc.mu.Lock()
		targetID, details := sc.targetID, sc.details
		sc.mu.Unlock()
		if targetID == "" && event.IDParam != "" {
			targetID = r.PathValue(event.IDParam)
		}
		for _, param := range event.DetailParams {
			if value := r.PathValue(param); value != "" {
				if details == nil {
					details = make(map[string]any, len(event.DetailParams))
				}
				details[detailKey(param)] = value
			}
		}
		write(info, event, info.Actor(), handle, orgID, targetID, outcomeOf(rec.Status(), rec.ErrorCode()), details)
	}
}

// SetTargetID sets the ID of the target of the request. A handler calls it when the path does not
// hold the ID, for example after it creates a resource.
func SetTargetID(ctx context.Context, id string) {
	if sc, ok := ctx.Value(scopeKey{}).(*scope); ok {
		sc.mu.Lock()
		sc.targetID = id
		sc.mu.Unlock()
	}
}

// AddDetail adds a detail to the audit record of the request. The value must not be personal
// data. The key is in snake_case.
func AddDetail(ctx context.Context, key string, value any) {
	if sc, ok := ctx.Value(scopeKey{}).(*scope); ok {
		sc.mu.Lock()
		if sc.details == nil {
			sc.details = make(map[string]any)
		}
		sc.details[key] = value
		sc.mu.Unlock()
	}
}

// detailKeys maps a path parameter to its detail key.
var detailKeys = map[string]string{
	"scope":  "scope",
	"attrID": "attribute_id",
	"ruleId": "rule_id",
}

func detailKey(param string) string {
	if key, ok := detailKeys[param]; ok {
		return key
	}
	return param
}
