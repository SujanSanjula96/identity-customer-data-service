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

package orgkey

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// fakeResolver knows the handles of stored, and stores the handles of known when asked to.
type fakeResolver struct {
	mu     sync.Mutex
	stored map[string]string
	known  map[string]string
	fail   error
	calls  atomic.Int32
	stores atomic.Int32
}

func (f *fakeResolver) resolve(_ context.Context, handle string, store bool) (Ref, bool, error) {

	f.calls.Add(1)
	if f.fail != nil {
		return Ref{}, false, f.fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.stored[handle]; ok {
		return Ref{ID: id, Handle: handle}, true, nil
	}
	if !store {
		return Ref{}, false, nil
	}
	id, ok := f.known[handle]
	if !ok {
		return Ref{}, false, nil
	}
	f.stores.Add(1)
	f.stored[handle] = id
	return Ref{ID: id, Handle: handle}, true, nil
}

func setUp(t *testing.T, phase database.OrgPhase) *fakeResolver {

	t.Helper()
	_ = log.Init("ERROR")
	config.OverrideCDSRuntime(config.Config{DataSource: config.DataSourceConfig{Type: database.TypePostgres}})
	database.SetOrgPhase(phase)
	fake := &fakeResolver{
		stored: map[string]string{"acme": "acme-id"},
		known:  map[string]string{"globex": "globex-id"},
	}
	SetResolver(fake.resolve)
	t.Cleanup(func() {
		database.SetOrgPhase(database.DefaultOrgPhase)
		SetResolver(nil)
	})
	return fake
}

func TestReadPhasesUseTheOrgId(t *testing.T) {

	for _, phase := range []database.OrgPhase{database.OrgPhaseReadOrgId, database.OrgPhaseOrgIdOnly} {
		t.Run(string(phase), func(t *testing.T) {
			fake := setUp(t, phase)
			ctx := context.Background()

			key, known, err := ForRead(ctx, "acme")
			if err != nil || !known || key != "acme-id" {
				t.Fatalf("ForRead(acme) = %q, %v, %v", key, known, err)
			}
			// A read never stores an organization.
			if _, known, err := ForRead(ctx, "globex"); err != nil || known {
				t.Fatalf("expected globex to be unknown on read, got %v, %v", known, err)
			}
			if fake.stores.Load() != 0 {
				t.Fatal("a read stored an organization")
			}

			key, err = ForWrite(ctx, "globex")
			if err != nil || key != "globex-id" {
				t.Fatalf("ForWrite(globex) = %q, %v", key, err)
			}
			_, err = ForWrite(ctx, "initech")
			var clientError *errors2.ClientError
			if !errors.As(err, &clientError) || clientError.StatusCode != http.StatusNotFound {
				t.Fatalf("expected a 404 for an unknown organization, got %v", err)
			}
		})
	}
}

func TestDualWriteUsesTheHandle(t *testing.T) {

	fake := setUp(t, database.OrgPhaseDualWrite)
	ctx := context.Background()

	key, known, err := ForRead(ctx, "unknown")
	if err != nil || !known || key != "unknown" {
		t.Fatalf("ForRead = %q, %v, %v", key, known, err)
	}
	if fake.calls.Load() != 0 {
		t.Fatal("a dual_write read looked the organization up")
	}

	// The write stores the organization, so that the statement can read its ID.
	if key, err := ForWrite(ctx, "globex"); err != nil || key != "globex" {
		t.Fatalf("ForWrite = %q, %v", key, err)
	}
	if fake.stores.Load() != 1 {
		t.Fatal("expected the write to store the organization")
	}

	// A failed lookup does not fail a dual_write write.
	fake.fail = errors.New("identity provider down")
	Invalidate("other")
	if key, err := ForWrite(ctx, "other"); err != nil || key != "other" {
		t.Fatalf("ForWrite with a failed lookup = %q, %v", key, err)
	}
}

func TestTheContextRefNeedsNoLookup(t *testing.T) {

	fake := setUp(t, database.OrgPhaseOrgIdOnly)
	ctx := WithRef(context.Background(), Ref{ID: "sub-id", Handle: "sub"})

	key, known, err := ForRead(ctx, "sub")
	if err != nil || !known || key != "sub-id" {
		t.Fatalf("ForRead = %q, %v, %v", key, known, err)
	}
	if fake.calls.Load() != 0 {
		t.Fatal("expected no lookup for the organization of the context")
	}
}

func TestResultsAreCached(t *testing.T) {

	fake := setUp(t, database.OrgPhaseOrgIdOnly)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, _, err := ForRead(ctx, "acme"); err != nil {
			t.Fatal(err)
		}
	}
	if fake.calls.Load() != 1 {
		t.Fatalf("expected one lookup, got %d", fake.calls.Load())
	}

	// An unknown organization is remembered for writes too.
	for i := 0; i < 3; i++ {
		_, _ = ForWrite(ctx, "initech")
	}
	if fake.calls.Load() != 2 {
		t.Fatalf("expected one more lookup, got %d", fake.calls.Load())
	}

	Invalidate("acme")
	_, _, _ = ForRead(ctx, "acme")
	if fake.calls.Load() != 3 {
		t.Fatalf("expected a lookup after Invalidate, got %d", fake.calls.Load())
	}
}

func TestConcurrentWritesStoreOnce(t *testing.T) {

	fake := setUp(t, database.OrgPhaseOrgIdOnly)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := ForWrite(context.Background(), "globex"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if fake.stores.Load() != 1 {
		t.Fatalf("expected one store, got %d", fake.stores.Load())
	}
}
