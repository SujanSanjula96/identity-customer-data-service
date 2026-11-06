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

func setUp(t *testing.T) *fakeResolver {

	t.Helper()
	_ = log.Init("ERROR")
	fake := &fakeResolver{
		stored: map[string]string{"acme": "acme-id"},
		known:  map[string]string{"globex": "globex-id"},
	}
	SetResolver(fake.resolve)
	t.Cleanup(func() {
		SetResolver(nil)
	})
	return fake
}

func TestReadAndWriteUseTheOrgId(t *testing.T) {

	fake := setUp(t)
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
}

func TestTheContextRefNeedsNoLookup(t *testing.T) {

	fake := setUp(t)
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

	fake := setUp(t)
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

	fake := setUp(t)
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
