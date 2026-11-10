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

// Org is the organisation a request is addressed to, as far as its path tells. A sub
// organisation is addressed as /t/{primary_handle}/o/{org_id}/..., so it is known here by the
// handle of its primary organisation and its own ID only: resolving the ID to a handle takes a
// read, which the middleware cannot afford before authentication.
type Org struct {
	// Handle is the organisation segment of the path, which for a sub organisation is the
	// handle of its primary organisation.
	Handle string
	// SubOrgID is the ID of the sub organisation the path addresses, empty for a primary
	// organisation. It comes from an unauthenticated request, so a lookup must treat it as a
	// key to match and nothing more.
	SubOrgID string
}

// OriginsLookup returns the origins an organisation allows. An empty result means the
// organisation has configured none, and the deployment-wide fallback applies.
type OriginsLookup func(ctx context.Context, org Org) []string

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

	if org := ExtractOrgFromPath(r.URL.Path); org.Handle != "" && lookup != nil {
		allowed = lookup(r.Context(), org)
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

// ExtractOrgFromPath returns the organisation a /t/{tenant}/... or
// /t/{primary_handle}/o/{org_id}/... path addresses, matching how the request is dispatched
// further down. It returns an empty Org for any other path, such as the health check. The
// middleware runs before dispatch, so the organisation cannot yet be read from the request
// context.
func ExtractOrgFromPath(path string) Org {

	remainder, found := strings.CutPrefix(path, "/t/")
	if !found {
		return Org{}
	}
	handle, rest, _ := strings.Cut(remainder, "/")
	if subOrgPath, isSubOrg := strings.CutPrefix(rest, "o/"); isSubOrg {
		subOrgID, _, _ := strings.Cut(subOrgPath, "/")
		return Org{Handle: handle, SubOrgID: subOrgID}
	}
	return Org{Handle: handle}
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
