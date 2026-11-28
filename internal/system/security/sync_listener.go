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

package security

import (
	"context"
	"net/http"
)

type syncListenerKey struct{}

// SyncListener marks the requests that reach next as having come on the sync listener, the one only
// the identity server calls.
func SyncListener(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), syncListenerKey{}, true)))
	})
}

// OnSyncListener reports whether the request came on the sync listener.
func OnSyncListener(r *http.Request) bool {
	onSync, _ := r.Context().Value(syncListenerKey{}).(bool)
	return onSync
}
