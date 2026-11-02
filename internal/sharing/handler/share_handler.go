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

package handler

import (
	"context"
	"encoding/json"
	"net/http"

	adminConfigService "github.com/wso2/identity-customer-data-service/internal/admin_config/service"
	orgModel "github.com/wso2/identity-customer-data-service/internal/organization/model"
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	"github.com/wso2/identity-customer-data-service/internal/sharing/model"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/security"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

// PolicyResource names the sharing policy resource in the responses.
const PolicyResource = "share policy"

// DecodePolicyRequest decodes the body of a policy request.
func DecodePolicyRequest(w http.ResponseWriter, r *http.Request) (model.PolicyRequest, bool) {

	var req model.PolicyRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.SHARE_BAD_REQUEST.Code,
			Message:     errors2.SHARE_BAD_REQUEST.Message,
			Description: utils.HandleDecodeError(err, "policy request"),
		}, http.StatusBadRequest))
		return req, false
	}
	return req, true
}

// InitiatorOf authenticates the request and returns the organization of the path. It writes an
// error response when CDS is not enabled or the organization is not known.
func InitiatorOf(w http.ResponseWriter, r *http.Request, operation string) (*orgModel.Organization, bool) {

	if err := security.AuthnAndAuthz(r, operation); err != nil {
		utils.HandleError(w, err)
		return nil, false
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !adminConfigService.GetAdminConfigService().IsCDSEnabled(r.Context(), orgHandle) {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest))
		return nil, false
	}
	org, err := knownOrg(r.Context(), orgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return nil, false
	}
	return org, true
}

// knownOrg returns the stored organization of the handle. It stores the primary organization if it
// is not stored yet.
func knownOrg(ctx context.Context, orgHandle string) (*orgModel.Organization, error) {

	org, err := orgService.GetOrStoreOrganization(ctx, orgHandle)
	if err != nil {
		return nil, err
	}
	if org == nil {
		return nil, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ORGANIZATION_NOT_FOUND.Code,
			Message:     errors2.ORGANIZATION_NOT_FOUND.Message,
			Description: "CDS does not know the organization '" + orgHandle + "'.",
		}, http.StatusNotFound)
	}
	return org, nil
}
