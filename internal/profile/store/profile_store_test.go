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

package store

import (
	"context"
	"errors"
	"net/http"
	"testing"

	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

func TestValidateFilterFieldsAcceptsSafeFields(t *testing.T) {

	filters := []string{
		"user_id eq u-1",
		"profile_id sw p-",
		"traits.email co example.com",
		"identity_attributes.given_name eq O'Brien", // the value is a bind argument
		"application_data.app-1.loyalty_tier eq gold",
		"malformed",
	}
	if err := validateFilterFields(filters); err != nil {
		t.Fatalf("validateFilterFields(%q) = %v; want nil", filters, err)
	}
}

func TestValidateFilterFieldsRejectsUnsafeFields(t *testing.T) {

	for _, field := range []string{
		"traits.x'||(SELECT pg_sleep(5))||'",
		"traits.{{org}}",
		"application_data.app.key'--",
		"traits.a;b",
	} {
		requireBadRequest(t, field, validateFilterFields([]string{field + " eq v"}))
	}
}

// The store functions check the fields before they resolve the organization or open the
// database, so no database is necessary here.
func TestFilterStoresRejectUnsafeFields(t *testing.T) {

	filter := "traits.x'||(SELECT 1)||' co v"

	_, _, err := GetAllProfilesWithFilter(context.Background(), "acme", []string{filter}, 10, nil)
	requireBadRequest(t, "GetAllProfilesWithFilter", err)

	_, err = GetProfileIDsWithFilters(context.Background(), "acme", []string{filter}, nil)
	requireBadRequest(t, "GetProfileIDsWithFilters", err)
}

func requireBadRequest(t *testing.T, name string, err error) {

	t.Helper()
	var clientError *errors2.ClientError
	if !errors.As(err, &clientError) || clientError.StatusCode != http.StatusBadRequest {
		t.Errorf("%s: err = %v; want a client error with status 400", name, err)
	}
}
