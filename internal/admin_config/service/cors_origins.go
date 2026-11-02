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
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	"github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// corsOriginsRefreshInterval bounds how long the origins snapshot is served before it is
// reloaded. An update reloads it in-process at once, so this is also the window in which
// another replica may still answer with the previous list.
const corsOriginsRefreshInterval = 30 * time.Second

// corsOrigins holds the configured origins of every organisation. The CORS middleware runs
// before authentication on every request, preflight included, so a lookup must not reach
// the database: the whole set is loaded in one read and reloaded on an interval, and a
// handle that is not in it, including one made up by an unauthenticated caller, costs no
// read at all.
var corsOrigins = &corsOriginsSnapshot{
	load:     store.GetAllCORSAllowedOrigins,
	interval: corsOriginsRefreshInterval,
}

type corsOriginsSnapshot struct {
	load     func(ctx context.Context) (map[string][]string, error)
	interval time.Duration

	current atomic.Pointer[loadedCORSOrigins]
	// generation counts updates, so a reload that read the database before an update
	// committed is not taken as fresh.
	generation atomic.Uint64
	reloadMu   sync.Mutex
}

type loadedCORSOrigins struct {
	byOrg    map[string][]string
	loadedAt time.Time
}

// get returns the origins the organisation has configured, or nil if it has none.
func (s *corsOriginsSnapshot) get(ctx context.Context, orgHandle string) []string {

	loaded := s.current.Load()
	if !s.isFresh(loaded) {
		loaded = s.reload(ctx, loaded)
	}
	return loaded.byOrg[orgHandle]
}

func (s *corsOriginsSnapshot) isFresh(loaded *loadedCORSOrigins) bool {
	return loaded != nil && time.Since(loaded.loadedAt) < s.interval
}

// reload reads the origins of every organisation. One caller reloads at a time, and the
// others keep answering from the stale snapshot rather than queue on the database. Only a
// caller with no snapshot at all, before the first load, waits.
func (s *corsOriginsSnapshot) reload(ctx context.Context, stale *loadedCORSOrigins) *loadedCORSOrigins {

	if stale == nil {
		s.reloadMu.Lock()
	} else if !s.reloadMu.TryLock() {
		return stale
	}
	defer s.reloadMu.Unlock()

	// Another caller may have reloaded while this one waited for the lock.
	if latest := s.current.Load(); s.isFresh(latest) {
		return latest
	}

	generation := s.generation.Load()
	// The read serves every request, not only the one that triggered it, so that request
	// going away must not cancel it.
	byOrg, err := s.load(context.WithoutCancel(ctx))
	if err != nil {
		// Keep answering with the previous origins, and wait out the interval before trying
		// again so that an unavailable database is not read on every request. With none
		// loaded yet, every organisation falls back to the deployment-wide origins.
		log.GetLogger().Warn("Failed to load the CORS allowed origins of organizations. "+
			"Serving the previously loaded origins.", log.Error(err))
		byOrg = map[string][]string{}
		if stale != nil {
			byOrg = stale.byOrg
		}
	}

	loaded := &loadedCORSOrigins{byOrg: byOrg, loadedAt: time.Now()}
	if s.generation.Load() != generation {
		// An update landed during the read, so the next lookup reloads again.
		loaded.loadedAt = time.Time{}
	}
	s.current.Store(loaded)
	return loaded
}

// invalidate makes the next lookup reload, so that this process serves an update at once.
func (s *corsOriginsSnapshot) invalidate() {

	s.generation.Add(1)
	if loaded := s.current.Load(); loaded != nil {
		s.current.Store(&loadedCORSOrigins{byOrg: loaded.byOrg})
	}
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

	return normalized, nil
}

func newCORSOriginError(description string) error {

	return errors.NewClientError(errors.ErrorMessage{
		Code:        errors.UPDATE_CONFIG_BAD_REQUEST.Code,
		Message:     errors.UPDATE_CONFIG_BAD_REQUEST.Message,
		Description: description,
	}, http.StatusBadRequest)
}
