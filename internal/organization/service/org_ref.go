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
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/organization/model"
	"github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/system/cache"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

// unknownTTL bounds how often the identity provider is asked for a handle that it does not know.
const unknownTTL = time.Minute

var (
	// unknownHandles holds the handles that the identity provider does not know.
	unknownHandles = cache.NewCache(unknownTTL)
	// storeLocks makes sure that one request at a time stores the organization of a handle.
	storeLocks sync.Map
)

// ResolveOrganization returns the org ID and the org handle of the organization. The APIs, the queue
// messages and the identity provider events carry the org handle: the edge of each flow (a request
// handler or a worker job) calls this once and passes the organization on.
//
// When the handle has no row, it reads the primary organization from the identity provider and
// stores it, as GetOrStoreOrganization does. It returns a not found error when the identity provider
// does not know the handle. Call it after the request is authenticated.
func ResolveOrganization(ctx context.Context, orgHandle string) (model.Ref, error) {

	if ref, found, err := store.LookupOrganizationRef(ctx, orgHandle); err != nil || found {
		return ref, err
	}
	if _, ok := unknownHandles.Get(orgHandle); ok {
		return model.Ref{}, organizationNotFound(orgHandle)
	}

	value, _ := storeLocks.LoadOrStore(orgHandle, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	// Another request can store the organization while this one waits.
	if ref, found, err := store.LookupOrganizationRef(ctx, orgHandle); err != nil || found {
		return ref, err
	}
	primary, err := EnsurePrimary(ctx, orgHandle)
	if isNotFound(err) {
		unknownHandles.Set(orgHandle, true)
		return model.Ref{}, organizationNotFound(orgHandle)
	}
	if err != nil {
		return model.Ref{}, err
	}
	return primary.Ref(), nil
}

// LookupOrganization returns the org ID and the org handle of the stored organization. found is
// false when CDS has no row for the handle. It does not call the identity provider, so an event
// about an organization that CDS does not know stores nothing.
func LookupOrganization(ctx context.Context, orgHandle string) (ref model.Ref, found bool, err error) {

	return store.LookupOrganizationRef(ctx, orgHandle)
}

func organizationNotFound(orgHandle string) error {

	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.ORGANIZATION_NOT_FOUND.Code,
		Message:     errors2.ORGANIZATION_NOT_FOUND.Message,
		Description: fmt.Sprintf("CDS does not know the organization '%s'.", orgHandle),
	}, http.StatusNotFound)
}

func isNotFound(err error) bool {

	var clientError *errors2.ClientError
	return errors.As(err, &clientError) && clientError.StatusCode == http.StatusNotFound
}
