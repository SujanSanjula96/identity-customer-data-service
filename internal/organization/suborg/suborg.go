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

// Package suborg holds the write rule of a sub organization. A sub organization can write profiles
// only.
package suborg

import (
	"context"
	"net/http"

	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
	"github.com/wso2/identity-customer-data-service/internal/system/errors"
)

// RejectWrite returns WriteNotAllowed when the handle belongs to a sub organization.
func RejectWrite(ctx context.Context, orgHandle string) error {

	org, err := orgStore.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil {
		return err
	}
	if org == nil || org.IsPrimary() {
		return nil
	}
	return WriteNotAllowed()
}

// WriteNotAllowed returns the error for a write in a sub organization.
func WriteNotAllowed() error {

	return errors.NewClientError(errors.ErrorMessage{
		Code:        errors.SUB_ORG_WRITE_NOT_ALLOWED.Code,
		Message:     errors.SUB_ORG_WRITE_NOT_ALLOWED.Message,
		Description: errors.SUB_ORG_WRITE_NOT_ALLOWED.Description,
	}, http.StatusForbidden)
}
