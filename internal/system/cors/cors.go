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

// Package cors decides which browser origins may call the service. The allowed set is
// per organisation, so it is resolved from the organisation in the request path rather
// than from a single deployment-wide list.
package cors

import (
	"context"
	"net/http"
	"strings"
)

// OriginsLookup returns the origins an organisation allows. An empty result means the
// organisation has configured none, and the deployment-wide fallback applies.
type OriginsLookup func(ctx context.Context, orgHandle string) []string

// Middleware answers cross-origin requests for UI/SDK clients. For a request addressed to
// an organisation it matches the Origin header against that organisation's configured
// origins; for one that carries no organisation, such as the health check, and for an
// organisation that has configured none, it falls back to deploymentOrigins.
func Middleware(next http.Handler, lookup OriginsLookup, deploymentOrigins func() []string) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// The allowed set depends on the Origin header and on the organisation in the
		// path, so a cache must not serve one origin's response to another.
		w.Header().Add("Vary", "Origin")

		if origin != "" && isOriginAllowed(r, origin, lookup, deploymentOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
			if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
				w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
			} else {
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			}
			w.Header().Set("Access-Control-Expose-Headers", "Content-Length")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isOriginAllowed resolves the origins that apply to this request and reports whether
// origin is among them.
func isOriginAllowed(r *http.Request, origin string, lookup OriginsLookup,
	deploymentOrigins func() []string) bool {

	var allowed []string

	if orgHandle := ExtractOrgHandleFromPath(r.URL.Path); orgHandle != "" && lookup != nil {
		allowed = lookup(r.Context(), orgHandle)
	}

	// No organisation in the path, or none configured for it: the deployment-wide list
	// applies, which is what every deployment relied on before origins became per
	// organisation.
	if len(allowed) == 0 && deploymentOrigins != nil {
		allowed = deploymentOrigins()
	}

	normalizedOrigin := normalizeOrigin(origin)
	for _, candidate := range allowed {
		if normalizeOrigin(candidate) == normalizedOrigin {
			return true
		}
	}
	return false
}

// ExtractOrgHandleFromPath returns the organisation segment of a /t/{tenant}/... path,
// matching how the request is dispatched further down. A sub organisation is addressed as
// /t/{primary_handle}/o/{org_id}/..., so its requests resolve to the primary organisation,
// whose origins it uses. It returns an empty string for any other path, such as the health
// check. The middleware runs before dispatch, so the segment cannot yet be read from the
// request context.
func ExtractOrgHandleFromPath(path string) string {

	remainder, found := strings.CutPrefix(path, "/t/")
	if !found {
		return ""
	}
	handle, _, _ := strings.Cut(remainder, "/")
	return handle
}

// normalizeOrigin lower-cases the scheme and host so that a configured origin compares
// equal to the browser-supplied Origin header, which is already normalized that way.
func normalizeOrigin(origin string) string {

	trimmed := strings.TrimSpace(origin)
	scheme, rest, found := strings.Cut(trimmed, "://")
	if !found {
		return trimmed
	}
	return strings.ToLower(scheme) + "://" + strings.ToLower(rest)
}
