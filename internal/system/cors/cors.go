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

// Package cors decides which browser origins may call the service. The deployment-wide list
// holds the browser apps every organisation uses, such as the Console, and applies to every
// request. An organisation adds the origins of its own apps, which apply to its requests only,
// so they are resolved from the organisation in the request path.
package cors

import (
	"context"
	"net/http"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// Org is the organisation a request is addressed to, as its path names it. A sub organisation
// is addressed as /t/{primary_handle}/o/{org_id}/..., so it is named by the handle of its
// primary organisation and its own ID.
type Org struct {
	// Handle is the organisation segment of the path, which for a sub organisation is the
	// handle of its primary organisation.
	Handle string
	// SubOrgId is the ID of the sub organisation the path addresses, empty for a primary
	// organisation. It comes from an unauthenticated request, so a lookup must treat it as a
	// key to match and nothing more.
	SubOrgId string
}

// OriginsLookup returns the origins an organisation has added to the deployment-wide list. An
// error means they could not be read, and only the deployment-wide list applies.
type OriginsLookup func(ctx context.Context, org Org) ([]string, error)

// Middleware answers cross-origin requests for UI/SDK clients. It allows the origins in
// deploymentOrigins on every request and, for a request addressed to an organisation, the
// origins that organisation has added as well.
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

	normalizedOrigin := normalizeOrigin(origin)

	// The deployment-wide list applies to every request, so an origin in it, such as the
	// Console's, needs no lookup of the organisation.
	if deploymentOrigins != nil && containsOrigin(deploymentOrigins(), normalizedOrigin) {
		return true
	}

	org := ExtractOrgFromPath(r.URL.Path)
	if org.Handle == "" || lookup == nil {
		return false
	}
	origins, err := lookup(r.Context(), org)
	if err != nil {
		// The deployment-wide list still applies, so the apps every organisation uses keep
		// working while the origins of this one cannot be read.
		log.GetLogger().Debug("Failed to read the CORS allowed origins of the organization. "+
			"Allowing the deployment-wide origins only.", log.String("orgHandle", org.Handle), log.Error(err))
		return false
	}
	return containsOrigin(origins, normalizedOrigin)
}

// containsOrigin reports whether normalizedOrigin is among origins.
func containsOrigin(origins []string, normalizedOrigin string) bool {

	for _, candidate := range origins {
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
		subOrgId, _, _ := strings.Cut(subOrgPath, "/")
		return Org{Handle: handle, SubOrgId: subOrgId}
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
