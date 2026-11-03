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

package idp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/wso2/identity-customer-data-service/internal/system/client"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

// WSO2ISAdapter reads the organization tree from the WSO2 IS organization management API.
type WSO2ISAdapter struct {
	client *client.IdentityClient
}

// NewWSO2ISAdapter returns an adapter for the configured IS.
func NewWSO2ISAdapter() *WSO2ISAdapter {
	return &WSO2ISAdapter{client: client.NewIdentityClient(config.GetCDSRuntime().Config)}
}

func (a *WSO2ISAdapter) GetPrimaryOrganization(_ context.Context, primaryHandle string) (Organization, error) {

	org, found, err := a.client.GetSelfOrganization(primaryHandle)
	if err != nil {
		return Organization{}, err
	}
	// A handle that IS does not know, or that names a sub organization, is not a primary
	// organization. The caller sent a wrong handle, so it is a client error.
	if !found {
		return Organization{}, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ORGANIZATION_NOT_FOUND.Code,
			Message:     errors2.ORGANIZATION_NOT_FOUND.Message,
			Description: fmt.Sprintf("The identity provider does not know the organization '%s'.", primaryHandle),
		}, http.StatusNotFound)
	}
	if org.Parent != nil && org.Parent.Id != "" {
		return Organization{}, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ORGANIZATION_NOT_FOUND.Code,
			Message:     errors2.ORGANIZATION_NOT_FOUND.Message,
			Description: fmt.Sprintf("Organization '%s' is not a primary organization.", primaryHandle),
		}, http.StatusNotFound)
	}
	return toOrganization(org), nil
}

func (a *WSO2ISAdapter) ListDescendants(_ context.Context, primaryHandle string) ([]Organization, error) {

	listed, err := a.client.ListDescendantOrganizations(primaryHandle)
	if err != nil {
		return nil, err
	}
	result := make([]Organization, 0, len(listed))
	for _, item := range listed {
		// The list API does not return the parent. Read each organization to get it.
		org, found, err := a.client.GetOrganization(primaryHandle, item.Id)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		result = append(result, toOrganization(org))
	}
	return result, nil
}

func (a *WSO2ISAdapter) GetOrganization(_ context.Context, primaryHandle, orgId string) (*Organization, error) {

	org, found, err := a.client.GetOrganization(primaryHandle, orgId)
	if err != nil || !found {
		return nil, err
	}
	result := toOrganization(org)
	return &result, nil
}

// IdentityAttributeSourceHandle returns the handle of the primary organization. In IS, a sub
// organization inherits the claims of its primary organization.
func (a *WSO2ISAdapter) IdentityAttributeSourceHandle(_ string, primaryHandle string) string {
	return primaryHandle
}

func toOrganization(org client.ISOrganization) Organization {

	result := Organization{
		Id:     org.Id,
		Handle: org.OrgHandle,
		Name:   org.Name,
		Status: org.Status,
	}
	if org.Parent != nil {
		result.ParentId = org.Parent.Id
	}
	return result
}
