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
	orgStore "github.com/wso2/identity-customer-data-service/internal/organization/store"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/sharing/model"
	"github.com/wso2/identity-customer-data-service/internal/sharing/service"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/security"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
	ruleService "github.com/wso2/identity-customer-data-service/internal/unification_rules/service"
)

const shareResource = "share policy"

// ShareHandler serves the share endpoints of schema attributes and unification rules.
type ShareHandler struct{}

func NewShareHandler() *ShareHandler {
	return &ShareHandler{}
}

// resourceOf checks the resource of the request and returns its type and ID. The org must own
// the resource, and the resource must be shareable.
type resourceOf func(r *http.Request, orgHandle string) (resourceType, resourceId string, err error)

func schemaAttributeOf(r *http.Request, orgHandle string) (string, string, error) {

	attr, err := schemaService.GetOwnedShareableAttribute(r.Context(), orgHandle, r.PathValue("scope"),
		r.PathValue("attrID"))
	if err != nil {
		return "", "", err
	}
	return model.ResourceSchemaAttribute, attr.AttributeId, nil
}

func unificationRuleOf(r *http.Request, orgHandle string) (string, string, error) {

	rule, err := ruleService.ValidateShareableRule(r.Context(), r.PathValue("ruleId"), orgHandle)
	if err != nil {
		return "", "", err
	}
	return model.ResourceUnificationRule, rule.RuleId, nil
}

func (h *ShareHandler) PutSchemaAttributeShare(w http.ResponseWriter, r *http.Request) {
	h.put(w, r, "profile_schema:share", schemaAttributeOf)
}

func (h *ShareHandler) GetSchemaAttributeShare(w http.ResponseWriter, r *http.Request) {
	h.get(w, r, "profile_schema:view", schemaAttributeOf)
}

func (h *ShareHandler) DeleteSchemaAttributeShare(w http.ResponseWriter, r *http.Request) {
	h.delete(w, r, "profile_schema:share", schemaAttributeOf)
}

func (h *ShareHandler) PutUnificationRuleShare(w http.ResponseWriter, r *http.Request) {
	h.put(w, r, "unification_rules:share", unificationRuleOf)
}

func (h *ShareHandler) GetUnificationRuleShare(w http.ResponseWriter, r *http.Request) {
	h.get(w, r, "unification_rules:view", unificationRuleOf)
}

func (h *ShareHandler) DeleteUnificationRuleShare(w http.ResponseWriter, r *http.Request) {
	h.delete(w, r, "unification_rules:share", unificationRuleOf)
}

func (h *ShareHandler) put(w http.ResponseWriter, r *http.Request, operation string, resolve resourceOf) {

	org, resourceType, resourceId, ok := h.prepare(w, r, operation, resolve)
	if !ok {
		return
	}
	var req model.ShareRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.SHARE_BAD_REQUEST.Code,
			Message:     errors2.SHARE_BAD_REQUEST.Message,
			Description: utils.HandleDecodeError(err, "share request"),
		}, http.StatusBadRequest))
		return
	}
	resp, err := service.PutPolicy(r.Context(), resourceType, resourceId, *org, req)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, resp, shareResource)
}

func (h *ShareHandler) get(w http.ResponseWriter, r *http.Request, operation string, resolve resourceOf) {

	org, resourceType, resourceId, ok := h.prepare(w, r, operation, resolve)
	if !ok {
		return
	}
	resp, err := service.GetPolicy(r.Context(), resourceType, resourceId, *org)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, resp, shareResource)
}

func (h *ShareHandler) delete(w http.ResponseWriter, r *http.Request, operation string, resolve resourceOf) {

	org, resourceType, resourceId, ok := h.prepare(w, r, operation, resolve)
	if !ok {
		return
	}
	if err := service.DeletePolicy(r.Context(), resourceType, resourceId, *org); err != nil {
		utils.HandleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// prepare authenticates the request, resolves the org of the path, and checks the resource.
func (h *ShareHandler) prepare(w http.ResponseWriter, r *http.Request, operation string,
	resolve resourceOf) (*orgModel.Organization, string, string, bool) {

	if err := security.AuthnAndAuthz(r, operation); err != nil {
		utils.HandleError(w, err)
		return nil, "", "", false
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !adminConfigService.GetAdminConfigService().IsCDSEnabled(r.Context(), orgHandle) {
		utils.HandleError(w, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest))
		return nil, "", "", false
	}
	org, err := knownOrg(r.Context(), orgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return nil, "", "", false
	}
	resourceType, resourceId, err := resolve(r, orgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return nil, "", "", false
	}
	return org, resourceType, resourceId, true
}

// knownOrg returns the org of the handle. A root that enabled CDS before B2B support has no org
// row yet, so CDS provisions its tree first.
func knownOrg(ctx context.Context, orgHandle string) (*orgModel.Organization, error) {

	org, err := orgStore.GetOrganizationByHandle(ctx, orgHandle)
	if err != nil || org != nil {
		return org, err
	}
	if _, err := orgService.ProvisionTree(ctx, orgHandle); err != nil {
		return nil, err
	}
	org, err = orgStore.GetOrganizationByHandle(ctx, orgHandle)
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
