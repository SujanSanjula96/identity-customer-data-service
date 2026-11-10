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

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	"github.com/wso2/identity-customer-data-service/internal/system/cache"
	"github.com/wso2/identity-customer-data-service/internal/system/errors"
)

// corsOriginsCacheTTL bounds how long the origins of an organisation are served from memory.
// An update drops the entry of this process at once, so this is the window in which another
// replica may still answer with the previous list.
const corsOriginsCacheTTL = 30 * time.Second

// unknownCORSHandleTTL bounds how long a handle that CDS has no organization for is answered
// without a read. A preflight is answered before the request is dispatched or authenticated,
// so without it every made-up handle would cost a read.
const unknownCORSHandleTTL = time.Minute

// maxStoredCORSOriginsLength is the width of cds_config.value, which holds the origins as one
// JSON array. A longer list is refused rather than left for the database to reject.
const maxStoredCORSOriginsLength = 500

// corsOrigins caches the configured origins of each organisation that receives requests. The
// CORS middleware consults it on every request, preflight included, so an organisation is read
// once per TTL, and one that has configured none is cached as such.
var corsOrigins = &corsOriginsCache{
	load:    store.GetCORSAllowedOrigins,
	entries: cache.NewCache(corsOriginsCacheTTL),
}

type corsOriginsCache struct {
	load    func(ctx context.Context, orgId string) ([]string, error)
	entries *cache.Cache
	// generation counts updates, so that a read that started before an update committed is
	// not cached over it.
	generation atomic.Uint64
}

// corsOrgIds resolves the handle in the path of a request to an org ID for the CORS
// middleware, remembering for a while the handles that CDS has no organization for.
var corsOrgIds = &corsOrgIdResolver{
	lookup:  orgService.LookupOrganization,
	unknown: cache.NewCache(unknownCORSHandleTTL),
}

type corsOrgIdResolver struct {
	lookup  func(ctx context.Context, orgHandle string) (orgModel.Ref, bool, error)
	unknown *cache.Cache
}

// resolve returns the org ID of the handle. found is false when CDS has no organization for it.
// It neither stores the organization nor asks the identity provider, as the request is not
// authenticated yet.
func (r *corsOrgIdResolver) resolve(ctx context.Context, orgHandle string) (string, bool, error) {

	if _, unknown := r.unknown.Get(orgHandle); unknown {
		return "", false, nil
	}
	ref, found, err := r.lookup(ctx, orgHandle)
	if err != nil {
		return "", false, err
	}
	if !found {
		r.unknown.Set(orgHandle, true)
		return "", false, nil
	}
	return ref.ID, true, nil
}

// forget drops the handle from the unknown ones, so that an organization that has just been
// stored is resolved at once.
func (r *corsOrgIdResolver) forget(orgHandle string) {
	r.unknown.Delete(orgHandle)
}

// get returns the origins the organisation has configured, or nil if it has none.
func (c *corsOriginsCache) get(ctx context.Context, orgId string) ([]string, error) {

	if cached, ok := c.entries.Get(orgId); ok {
		if origins, ok := cached.([]string); ok {
			return origins, nil
		}
	}
	generation := c.generation.Load()
	origins, err := c.load(ctx, orgId)
	if err != nil {
		return nil, err
	}
	if c.generation.Load() == generation {
		c.entries.Set(orgId, origins)
	}
	return origins, nil
}

// invalidate drops the cached origins of the organisation, so that this process serves an
// update at once.
func (c *corsOriginsCache) invalidate(orgId string) {

	c.generation.Add(1)
	c.entries.Delete(orgId)
}

// normalizeOrigin lower-cases the scheme and host of an origin so that comparison against
// a browser-supplied Origin header, which is already normalized that way, is reliable.
// The port is left as given.
func normalizeOrigin(origin string) string {

	trimmed := strings.TrimSpace(origin)
	scheme, rest, found := strings.Cut(trimmed, "://")
	if !found {
		return trimmed
	}
	host, remainder, hasRemainder := strings.Cut(rest, "/")
	normalized := strings.ToLower(scheme) + "://" + strings.ToLower(host)
	if hasRemainder {
		return normalized + "/" + remainder
	}
	return normalized
}

// validateCORSOrigins checks that every entry is an absolute scheme://host[:port] origin
// carrying no path, no query, no fragment and no credentials, and returns the normalized
// list with duplicates removed. A wildcard is refused: the middleware answers with
// Access-Control-Allow-Credentials, which a browser rejects alongside "*", so accepting
// one here would only produce an origin that never matches.
func validateCORSOrigins(origins []string) ([]string, error) {

	normalized := make([]string, 0, len(origins))
	seen := make(map[string]bool, len(origins))

	for _, origin := range origins {
		candidate := strings.TrimSpace(origin)

		if candidate == "" {
			return nil, newCORSOriginError("An allowed origin may not be empty.")
		}
		// A browser matches Origin exactly, so neither "*" nor a wildcard host such as
		// "https://*.acme.com" can ever match. Refuse both rather than store an origin
		// that silently never applies.
		if strings.Contains(candidate, "*") {
			return nil, newCORSOriginError(fmt.Sprintf(
				"Origin '%s' may not contain a wildcard. List each origin explicitly.", candidate))
		}
		if strings.HasSuffix(candidate, "/") {
			return nil, newCORSOriginError(fmt.Sprintf(
				"Origin '%s' must not end with a trailing slash.", candidate))
		}

		parsed, err := url.Parse(candidate)
		if err != nil {
			return nil, newCORSOriginError(fmt.Sprintf("Origin '%s' is not a valid URL.", candidate))
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, newCORSOriginError(fmt.Sprintf(
				"Origin '%s' must use the http or https scheme.", candidate))
		}
		if parsed.Host == "" {
			return nil, newCORSOriginError(fmt.Sprintf(
				"Origin '%s' must be an absolute origin of the form scheme://host[:port].", candidate))
		}
		if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return nil, newCORSOriginError(fmt.Sprintf(
				"Origin '%s' must not carry a path, query, fragment or credentials.", candidate))
		}

		candidate = normalizeOrigin(candidate)
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		normalized = append(normalized, candidate)
	}

	if stored, err := json.Marshal(normalized); err == nil && len(stored) > maxStoredCORSOriginsLength {
		return nil, newCORSOriginError(fmt.Sprintf(
			"The allowed origins take %d characters once stored, over the limit of %d. Remove some origins.",
			len(stored), maxStoredCORSOriginsLength))
	}

	return normalized, nil
}

func newCORSOriginError(description string) error {

	return errors.NewClientError(errors.ErrorMessage{
		Code:        errors.UPDATE_CONFIG_BAD_REQUEST.Code,
		Message:     errors.UPDATE_CONFIG_BAD_REQUEST.Message,
		Description: description,
	}, http.StatusBadRequest)
}
