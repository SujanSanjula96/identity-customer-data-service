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

package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

// ISOrganization is an organization from the IS organization management API.
type ISOrganization struct {
	Id        string `json:"id"`
	Name      string `json:"name"`
	OrgHandle string `json:"orgHandle"`
	Status    string `json:"status"`
	Parent    *struct {
		Id string `json:"id"`
	} `json:"parent,omitempty"`
}

type isOrganizationList struct {
	Organizations []ISOrganization `json:"organizations"`
	Links         []struct {
		Href string `json:"href"`
		Rel  string `json:"rel"`
	} `json:"links"`
}

// GetSelfOrganization returns the organization that rootHandle names.
func (c *IdentityClient) GetSelfOrganization(orgHandle string) (ISOrganization, error) {

	var org ISOrganization
	endpoint := fmt.Sprintf("https://%s/t/%s/api/server/v1/organizations/self", c.BaseURL, url.PathEscape(orgHandle))
	_, err := c.getJSON(endpoint, orgHandle, &org)
	return org, err
}

// GetOrganization returns a descendant organization of the root that rootHandle names. The
// bool is false when IS does not know the organization.
func (c *IdentityClient) GetOrganization(rootHandle, orgId string) (ISOrganization, bool, error) {

	var org ISOrganization
	endpoint := fmt.Sprintf("https://%s/t/%s/api/server/v1/organizations/%s", c.BaseURL,
		url.PathEscape(rootHandle), url.PathEscape(orgId))
	status, err := c.getJSON(endpoint, rootHandle, &org)
	if status == http.StatusNotFound {
		return org, false, nil
	}
	if err != nil {
		return org, false, err
	}
	return org, true, nil
}

// ListDescendantOrganizations returns all organizations below the root that rootHandle names.
// The list API does not return the parent, so the caller reads each organization for it.
func (c *IdentityClient) ListDescendantOrganizations(rootHandle string) ([]ISOrganization, error) {

	next := fmt.Sprintf("https://%s/t/%s/api/server/v1/organizations?recursive=true&limit=100", c.BaseURL,
		url.PathEscape(rootHandle))
	var all []ISOrganization
	for pages := 0; next != "" && pages < 100; pages++ {
		var page isOrganizationList
		if _, err := c.getJSON(next, rootHandle, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Organizations...)
		next = ""
		for _, link := range page.Links {
			if link.Rel == "next" && link.Href != "" {
				next = link.Href
				if strings.HasPrefix(next, "/") {
					next = "https://" + c.BaseURL + next
				}
			}
		}
	}
	return all, nil
}

// getJSON sends a GET request with a token for the org, and decodes a 200 response into out. It
// returns the HTTP status.
func (c *IdentityClient) getJSON(endpoint, orgHandle string, out interface{}) (int, error) {

	failed := func(desc string, cause error) error {
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.ORGANIZATION_SYNC.Code,
			Message:     errors2.ORGANIZATION_SYNC.Message,
			Description: desc,
		}, cause)
	}

	token, err := c.FetchToken(orgHandle)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, failed("Failed to create the request: "+endpoint, err)
	}
	if config.GetCDSRuntime().Config.AuthServer.IsSystemAppGrantEnabled {
		req.Header.Set("Authorization", constants.SystemAppHeader+constants.SpaceSeparator+token)
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return 0, failed("Failed to call: "+endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, failed("Failed to read the response of: "+endpoint, err)
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, failed(fmt.Sprintf("%s returned status %d: %s", endpoint, resp.StatusCode,
			strings.TrimSpace(string(body))), fmt.Errorf("status %d", resp.StatusCode))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, failed("Failed to parse the response of: "+endpoint, err)
	}
	return resp.StatusCode, nil
}
