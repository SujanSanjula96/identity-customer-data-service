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
	"encoding/json"
	"fmt"
	"net/http"

	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	"github.com/wso2/identity-customer-data-service/internal/service_credentials/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/idp"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/security"
	"github.com/wso2/identity-customer-data-service/internal/system/servicecreds"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

// ServiceCredentialsHandler receives the service credentials the identity
// provider sends for an organization. Only the identity provider calls it, with
// the sync admin credentials.
type ServiceCredentialsHandler struct{}

func NewServiceCredentialsHandler() *ServiceCredentialsHandler {
	return &ServiceCredentialsHandler{}
}

// SyncServiceCredentials stores the credentials of the organization, after the
// adapter of the identity provider has verified them.
func (h *ServiceCredentialsHandler) SyncServiceCredentials(w http.ResponseWriter, r *http.Request) {

	if err := security.AuthnWithAdminCredentials(r); err != nil {
		utils.HandleError(w, err)
		return
	}
	if pathOrgId, _ := r.Context().Value(constants.OrgIdContextKey).(string); pathOrgId != "" {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.SUB_ORG_CONFIG_NOT_ALLOWED.Code,
			Message:     errors2.SUB_ORG_CONFIG_NOT_ALLOWED.Message,
			Description: "Only a primary organization has service credentials.",
		}, http.StatusBadRequest))
		return
	}
	verifier, supported := idp.GetAdapter().(idp.ServiceCredentialsVerifier)
	if !supported || !servicecreds.Enabled() {
		utils.HandleError(w, errors2.NewClientError(errors2.SERVICE_CREDENTIALS_NOT_CONFIGURED,
			http.StatusBadRequest))
		return
	}

	var event model.ServiceCredentialsSyncEvent
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil || event.Event != model.EventServiceCredentialsUpdated ||
		event.OrgId == "" || event.ClientID == "" || event.ClientSecret == "" {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.BAD_REQUEST.Code,
			Message: errors2.BAD_REQUEST.Message,
			Description: fmt.Sprintf("The body must have 'event' %s, 'org_id', 'client_id' and 'client_secret'.",
				model.EventServiceCredentialsUpdated),
		}, http.StatusBadRequest))
		return
	}

	orgHandle := utils.ExtractOrgHandleFromPath(r)
	// The credentials can arrive before CDS stores the organization, since CDS needs them to read it from the
	// identity provider. An organization CDS already knows has to be the one of the event.
	ref, known, err := orgService.LookupOrganization(r.Context(), orgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if known && ref.ID != event.OrgId {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.BAD_REQUEST.Code,
			Message:     errors2.BAD_REQUEST.Message,
			Description: fmt.Sprintf("'org_id' is not the ID of the organization %s.", orgHandle),
		}, http.StatusBadRequest))
		return
	}
	if err := verifier.VerifyServiceCredentials(r.Context(), orgHandle, event.ClientID,
		event.ClientSecret); err != nil {
		utils.HandleError(w, err)
		return
	}
	err = servicecreds.Save(r.Context(), servicecreds.Credentials{
		OrgId:        event.OrgId,
		OrgHandle:    orgHandle,
		ClientID:     event.ClientID,
		ClientSecret: event.ClientSecret,
	})
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	log.GetLogger().Info(fmt.Sprintf("Stored the service credentials %s of organization: %s", event.ClientID,
		orgHandle))
	w.WriteHeader(http.StatusNoContent)
}
