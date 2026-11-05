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

// Package orgkey gives the stores the org key: the value that the statements of the active
// org_id_migration phase write and filter by. It is the org handle in dual_write and the org ID
// afterwards.
//
// The APIs, the queue messages and the workers carry the org handle. A store resolves it here, so
// every caller (a request, the profile worker, a queue consumer, a background job) gets the same
// resolution without carrying the org ID itself.
package orgkey

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/cache"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// Ref is an organization as the database stores it.
type Ref struct {
	ID     string
	Handle string
}

// Resolver returns the stored organization of the handle. When store is true and the handle has no
// row, it stores the primary organization of the handle first. found is false when the organization
// is not known.
type Resolver func(ctx context.Context, handle string, store bool) (ref Ref, found bool, err error)

const (
	// knownTTL bounds how long a pod uses a mapping after the handle changes on another pod.
	knownTTL = 5 * time.Minute
	// unknownTTL bounds how often a handle that the identity provider does not know is looked up.
	unknownTTL = time.Minute
)

type refContextKey struct{}

var (
	resolverMu sync.RWMutex
	resolver   Resolver

	known   = cache.NewCache(knownTTL)
	unknown = cache.NewCache(unknownTTL)

	// storeLocks makes sure that one lookup stores the organization of a handle at a time.
	storeLocks sync.Map
)

// SetResolver installs the resolver. The server installs it once at start-up.
func SetResolver(r Resolver) {

	resolverMu.Lock()
	defer resolverMu.Unlock()
	resolver = r
	known.Clear()
	unknown.Clear()
}

// WithRef returns a context that carries the organization of the request, so that the stores need
// no lookup for it.
func WithRef(ctx context.Context, ref Ref) context.Context {

	return context.WithValue(ctx, refContextKey{}, ref)
}

// RefFrom returns the organization that WithRef put into the context.
func RefFrom(ctx context.Context) (Ref, bool) {

	ref, ok := ctx.Value(refContextKey{}).(Ref)
	return ref, ok
}

// Phase returns the org_id_migration phase of the configured datasource.
func Phase() database.OrgPhase {

	return database.OrgPhaseFor(provider.NewDBProvider().GetDBType())
}

// ForRead returns the org key to filter by. known is false when the organization has no row: it
// owns no data, and the caller returns an empty result. ForRead never calls the identity provider.
func ForRead(ctx context.Context, handle string) (key string, isKnown bool, err error) {

	if !Phase().ReadsOrgId() {
		return handle, true, nil
	}
	ref, found, err := Resolve(ctx, handle, false)
	if err != nil || !found {
		return "", false, err
	}
	return ref.ID, true, nil
}

// ForWrite returns the org key to write. It stores the organization when it has no row yet.
//
// In dual_write the key is the handle, and the statement reads the org ID from the organizations
// table. A failed lookup there does not fail the write: the row gets no org ID, and the backfill
// fills it in.
func ForWrite(ctx context.Context, handle string) (string, error) {

	ref, found, err := Resolve(ctx, handle, true)
	if !Phase().ReadsOrgId() {
		if err != nil || !found {
			log.GetLogger().Warn(fmt.Sprintf("Could not resolve the organization '%s'. The row is written "+
				"without an org ID.", handle), log.Error(err))
		}
		return handle, nil
	}
	if err != nil {
		return "", err
	}
	if !found {
		return "", notFound(handle)
	}
	return ref.ID, nil
}

// Resolve returns the organization of the handle: from the context, the cache, or the resolver.
// When store is true and the handle has no row, the resolver stores it.
func Resolve(ctx context.Context, handle string, store bool) (Ref, bool, error) {

	if ref, ok := RefFrom(ctx); ok && ref.Handle == handle && ref.ID != "" {
		return ref, true, nil
	}
	if ref, ok := cachedRef(handle); ok {
		return ref, true, nil
	}

	resolverMu.RLock()
	resolve := resolver
	resolverMu.RUnlock()
	if resolve == nil {
		return Ref{}, false, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.ORGANIZATION_STORE.Code,
			Message:     errors2.ORGANIZATION_STORE.Message,
			Description: "No organization resolver is installed.",
		}, nil)
	}

	if !store {
		ref, found, err := resolve(ctx, handle, false)
		if err != nil {
			return Ref{}, false, asServerError(handle, err)
		}
		if found {
			known.Set(handle, ref)
		}
		return ref, found, nil
	}

	if _, ok := unknown.Get(handle); ok {
		return Ref{}, false, nil
	}
	lock, _ := storeLocks.LoadOrStore(handle, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	// Another caller may have stored it while this one waited.
	if ref, ok := cachedRef(handle); ok {
		return ref, true, nil
	}
	ref, found, err := resolve(ctx, handle, true)
	if err != nil {
		return Ref{}, false, asServerError(handle, err)
	}
	if !found {
		unknown.Set(handle, true)
		return Ref{}, false, nil
	}
	known.Set(handle, ref)
	return ref, true, nil
}

// Invalidate drops what this pod cached for the handle. Call it when the organizations row of the
// handle changes.
func Invalidate(handle string) {

	if handle == "" {
		return
	}
	known.Delete(handle)
	unknown.Delete(handle)
}

func cachedRef(handle string) (Ref, bool) {

	value, ok := known.Get(handle)
	if !ok {
		return Ref{}, false
	}
	ref, ok := value.(Ref)
	return ref, ok
}

// asServerError keeps a client or server error of the resolver, and wraps any other error, so that a
// store returns the same kind of error whether its own statement or the lookup failed.
func asServerError(handle string, err error) error {

	var clientError *errors2.ClientError
	var serverError *errors2.ServerError
	if errors.As(err, &clientError) || errors.As(err, &serverError) {
		return err
	}
	return errors2.NewServerError(errors2.ErrorMessage{
		Code:        errors2.ORGANIZATION_STORE.Code,
		Message:     errors2.ORGANIZATION_STORE.Message,
		Description: fmt.Sprintf("Failed to resolve the organization '%s'.", handle),
	}, err)
}

func notFound(handle string) error {

	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.ORGANIZATION_NOT_FOUND.Code,
		Message:     errors2.ORGANIZATION_NOT_FOUND.Message,
		Description: fmt.Sprintf("CDS does not know the organization '%s'.", handle),
	}, http.StatusNotFound)
}
