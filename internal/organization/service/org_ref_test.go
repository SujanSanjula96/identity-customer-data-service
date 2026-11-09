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
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/test/setup"
)

// lookupIdP knows the handles of known as primary organizations, and counts the lookups.
//
// The organization lookups cache by handle for the process, so each test uses its own handles.
type lookupIdP struct {
	known   map[string]string
	lookups atomic.Int32
}

func (f *lookupIdP) GetPrimaryOrganization(_ context.Context, handle string) (idp.Organization, error) {

	f.lookups.Add(1)
	id, ok := f.known[handle]
	if !ok {
		return idp.Organization{}, errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.ORGANIZATION_NOT_FOUND.Code,
			Message: errors2.ORGANIZATION_NOT_FOUND.Message,
		}, http.StatusNotFound)
	}
	return idp.Organization{Id: id, Handle: handle, Name: handle}, nil
}

func (f *lookupIdP) ListDescendants(context.Context, string) ([]idp.Organization, error) {
	return nil, nil
}

func (f *lookupIdP) GetOrganization(context.Context, string, string) (*idp.Organization, error) {
	return nil, nil
}

func (f *lookupIdP) IdentityAttributeSourceHandle(_, primaryHandle string) string {
	return primaryHandle
}

func setUpLookup(t *testing.T, known map[string]string) (*lookupIdP, *setup.SQLiteTestDB) {

	t.Helper()
	if err := log.Init("ERROR"); err != nil {
		t.Fatalf("failed to set up the logger: %v", err)
	}
	testDB, err := setup.SetupTestSQLite()
	if err != nil {
		t.Fatalf("failed to set up the test database: %v", err)
	}
	provider.SetTestDB(testDB.DB, database.TypeSQLite)
	fake := &lookupIdP{known: known}
	idp.SetAdapter(fake)
	t.Cleanup(func() {
		idp.SetAdapter(nil)
		provider.SetTestDB(nil, "")
		testDB.Terminate()
	})
	return fake, testDB
}

func requireNotFound(t *testing.T, err error) {

	t.Helper()
	var clientError *errors2.ClientError
	if !errors.As(err, &clientError) || clientError.StatusCode != http.StatusNotFound {
		t.Fatalf("expected a 404, got %v", err)
	}
}

func TestResolveOrganizationStoresThePrimaryOrganization(t *testing.T) {

	fake, _ := setUpLookup(t, map[string]string{"globex": "globex-id"})
	ctx := context.Background()

	// A lookup does not call the identity provider and stores nothing.
	if _, found, err := LookupOrganization(ctx, "globex"); err != nil || found {
		t.Fatalf("expected globex to be unknown before it is resolved, got %v, %v", found, err)
	}
	if fake.lookups.Load() != 0 {
		t.Fatal("LookupOrganization called the identity provider")
	}

	ref, err := ResolveOrganization(ctx, "globex")
	if err != nil || ref.ID != "globex-id" || ref.Handle != "globex" {
		t.Fatalf("ResolveOrganization(globex) = %+v, %v", ref, err)
	}
	ref, found, err := LookupOrganization(ctx, "globex")
	if err != nil || !found || ref.ID != "globex-id" {
		t.Fatalf("LookupOrganization(globex) after the store = %+v, %v, %v", ref, found, err)
	}

	_, err = ResolveOrganization(ctx, "initech")
	requireNotFound(t, err)
}

func TestResolveOrganizationUsesTheStoredRow(t *testing.T) {

	fake, testDB := setUpLookup(t, nil)
	if err := setup.InsertTestOrganization(testDB.DB, "stark-id", "stark"); err != nil {
		t.Fatal(err)
	}

	ref, err := ResolveOrganization(context.Background(), "stark")
	if err != nil || ref.ID != "stark-id" {
		t.Fatalf("ResolveOrganization(stark) = %+v, %v", ref, err)
	}
	if fake.lookups.Load() != 0 {
		t.Fatal("expected no identity provider lookup for a stored organization")
	}
}

func TestLookupsAreCached(t *testing.T) {

	fake, testDB := setUpLookup(t, nil)
	ctx := context.Background()
	if err := setup.InsertTestOrganization(testDB.DB, "wayne-id", "wayne"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := LookupOrganization(ctx, "wayne"); err != nil || !found {
		t.Fatalf("LookupOrganization(wayne) = %v, %v", found, err)
	}
	// The second lookup comes from the cache, so it finds the row after the row is gone.
	if _, err := testDB.DB.Exec(`DELETE FROM organizations WHERE org_handle = 'wayne'`); err != nil {
		t.Fatal(err)
	}
	if ref, found, err := LookupOrganization(ctx, "wayne"); err != nil || !found || ref.ID != "wayne-id" {
		t.Fatalf("expected the cached organization, got %+v, %v, %v", ref, found, err)
	}

	// A handle that the identity provider does not know is asked for once.
	for i := 0; i < 3; i++ {
		_, err := ResolveOrganization(ctx, "umbrella")
		requireNotFound(t, err)
	}
	if fake.lookups.Load() != 1 {
		t.Fatalf("expected one identity provider lookup, got %d", fake.lookups.Load())
	}
}

func TestConcurrentResolvesStoreOnce(t *testing.T) {

	fake, _ := setUpLookup(t, map[string]string{"hooli": "hooli-id"})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ref, err := ResolveOrganization(context.Background(), "hooli"); err != nil || ref.ID != "hooli-id" {
				t.Errorf("ResolveOrganization(hooli) = %+v, %v", ref, err)
			}
		}()
	}
	wg.Wait()
	if fake.lookups.Load() != 1 {
		t.Fatalf("expected one identity provider lookup, got %d", fake.lookups.Load())
	}
}

func TestLookupOrganizationById(t *testing.T) {

	fake, testDB := setUpLookup(t, map[string]string{"pied-piper": "pied-piper-id"})
	ctx := context.Background()

	// An unknown ID is not found, and the identity provider is not called.
	if _, found, err := LookupOrganizationById(ctx, "pied-piper-id"); err != nil || found {
		t.Fatalf("expected pied-piper-id to be unknown before it is resolved, got %v, %v", found, err)
	}
	if fake.lookups.Load() != 0 {
		t.Fatal("LookupOrganizationById called the identity provider")
	}

	// The ID of an organization that ResolveOrganization stored is found with its handle.
	if _, err := ResolveOrganization(ctx, "pied-piper"); err != nil {
		t.Fatal(err)
	}
	ref, found, err := LookupOrganizationById(ctx, "pied-piper-id")
	if err != nil || !found || ref.ID != "pied-piper-id" || ref.Handle != "pied-piper" {
		t.Fatalf("LookupOrganizationById(pied-piper-id) = %+v, %v, %v", ref, found, err)
	}

	// The second lookup comes from the cache, so it finds the row after the row is gone.
	if _, err := testDB.DB.Exec(`DELETE FROM organizations WHERE org_id = 'pied-piper-id'`); err != nil {
		t.Fatal(err)
	}
	if ref, found, err := LookupOrganizationById(ctx, "pied-piper-id"); err != nil || !found ||
		ref.Handle != "pied-piper" {
		t.Fatalf("expected the cached organization, got %+v, %v, %v", ref, found, err)
	}
}
