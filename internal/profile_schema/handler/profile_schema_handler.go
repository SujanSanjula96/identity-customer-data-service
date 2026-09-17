/*
 * Copyright (c) 2025, WSO2 LLC. (http://www.wso2.com).
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
	"fmt"
	"net/http"
	"strings"
	"sync"

	adminConfigService "github.com/wso2/identity-customer-data-service/internal/admin_config/service"
	"github.com/wso2/identity-customer-data-service/internal/system/security"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"

	"github.com/google/uuid"
	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/profile_schema/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

type ProfileSchemaHandler struct {
	store map[string]model.ProfileSchemaAttribute
	mu    *sync.RWMutex
}

func NewProfileSchemaHandler() *ProfileSchemaHandler {

	return &ProfileSchemaHandler{
		store: make(map[string]model.ProfileSchemaAttribute),
		mu:    &sync.RWMutex{},
	}
}

// AddProfileSchemaAttributesForScope handles adding a new profile schema attribute.
func (psh *ProfileSchemaHandler) AddProfileSchemaAttributesForScope(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	scope := r.PathValue("scope")
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	err := security.AuthnAndAuthz(r, "profile_schema:create")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	var schemaAttributes []model.ProfileSchemaAttribute
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schemaAttributes); err != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Code,
			Message:     errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Message,
			Description: utils.HandleDecodeError(err, "profile schema attributes"),
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}
	// Validate the scope
	if scope == constants.IdentityAttributes {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:    errors2.ATTRIBUTE_UPATE_NOT_SUPPORTED.Code,
			Message: errors2.ATTRIBUTE_UPATE_NOT_SUPPORTED.Message,
			Description: "Identity attributes cannot be created. " +
				"Use Identity Provider to define and manage identity attributes.",
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}
	if !constants.AllowedAttributesScope[scope] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Code,
			Message:     errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Message,
			Description: "Invalid scope for profile schema attribute: " + scope,
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}
	if scope != constants.ApplicationData {
		for _, attr := range schemaAttributes {
			if attr.ApplicationIdentifier != "" {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Code,
					Message:     errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Message,
					Description: "Application identifier is allowed for application data attributes only.",
				}, http.StatusBadRequest)
				utils.WriteErrorResponse(w, clientError)
				return
			}
		}
	}
	if len(schemaAttributes) == 0 {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Code,
			Message:     errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Message,
			Description: "No attributes provided in the request body.",
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}
	// Generate a new UUID for each attribute if not provided
	for i := range schemaAttributes {
		if schemaAttributes[i].AttributeId == "" {
			schemaAttributes[i].AttributeId = uuid.New().String()
		}
		if schemaAttributes[i].Mutability == "" {
			schemaAttributes[i].Mutability = constants.MutabilityReadWrite
		}
		schemaAttributes[i].OrgId = orgHandle
	}

	schemaProvider := provider.NewProfileSchemaProvider()
	schemaService := schemaProvider.GetProfileSchemaService()
	addedAttributes, err := schemaService.AddProfileSchemaAttributesForScope(ctx, schemaAttributes, scope, orgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	schemaAttributesNew := addedAttributes
	for i := range schemaAttributesNew {
		schemaAttributesNew[i].OrgId = ""
	}
	utils.RespondJSON(w, http.StatusCreated, schemaAttributesNew, constants.SchemaAttribute)
}

// GetProfileSchema handles fetching the entire profile schema.
func (psh *ProfileSchemaHandler) GetProfileSchema(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	orgHandle := utils.ExtractOrgHandleFromPath(r)
	err := security.AuthnAndAuthz(r, "profile_schema:view")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	schemaProvider := provider.NewProfileSchemaProvider()
	schemaService := schemaProvider.GetProfileSchemaService()
	profileSchema, err := schemaService.GetProfileSchema(ctx, orgHandle)

	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, profileSchema, constants.SchemaAttribute)
}

// GetProfileSchemaAttributeById handles fetching the entire profile schema.
func (psh *ProfileSchemaHandler) GetProfileSchemaAttributeById(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	scope := r.PathValue("scope")
	attributeId := r.PathValue("attrID")
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	err := security.AuthnAndAuthz(r, "profile_schema:view")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	schemaProvider := provider.NewProfileSchemaProvider()
	schemaService := schemaProvider.GetProfileSchemaService()

	if !constants.AllowedAttributesScope[scope] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Code,
			Message:     errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Message,
			Description: "Invalid scope for profile schema attribute: " + scope,
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}

	attribute, err := schemaService.GetProfileSchemaAttributeById(ctx, orgHandle, attributeId)

	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, attribute, constants.SchemaAttribute)
}

// GetProfileSchemaAttributeForScope handles fetching the entire profile schema for the scope.
func (psh *ProfileSchemaHandler) GetProfileSchemaAttributeForScope(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	scope := r.PathValue("scope")
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	err := security.AuthnAndAuthz(r, "profile_schema:view")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	if !constants.AllowedAttributesScope[scope] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Code,
			Message:     errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Message,
			Description: "Invalid scope for profile schema attribute: " + scope,
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}

	schemaProvider := provider.NewProfileSchemaProvider()
	schemaService := schemaProvider.GetProfileSchemaService()
	var attributes interface{}

	// Build the filter from query params
	queryFilters := r.URL.Query()[constants.Filter] // Slice of filter params

	var filters []string
	for _, f := range queryFilters {
		// Split by " and " to support multiple conditions in a single filter param
		splitFilters := strings.Split(f, " and ")
		for _, sf := range splitFilters {
			sf = strings.TrimSpace(sf)
			if sf != "" {
				filters = append(filters, sf)
			}
		}
	}
	if len(queryFilters) > 0 {
		attributes, err = schemaService.GetProfileSchemaAttributesByScopeAndFilter(ctx, orgHandle, scope, filters)
	} else {
		if constants.AllowedAttributesScope[scope] {
			attributes, err = schemaService.GetProfileSchemaAttributesByScope(ctx, orgHandle, scope)
		}
	}

	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, attributes, constants.SchemaAttribute)
}

// PatchProfileSchemaAttributeById updates a profile schema attribute.
func (psh *ProfileSchemaHandler) PatchProfileSchemaAttributeById(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	attributeId := r.PathValue("attrID")
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	err := security.AuthnAndAuthz(r, "profile_schema:update")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	scope := r.PathValue("scope")
	if !constants.AllowedAttributesScope[scope] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Code,
			Message:     errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Message,
			Description: "Invalid scope for profile schema attribute: " + scope,
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}
	if scope == constants.IdentityAttributes {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ATTRIBUTE_UPATE_NOT_SUPPORTED.Code,
			Message:     errors2.ATTRIBUTE_UPATE_NOT_SUPPORTED.Message,
			Description: "Identity attributes cannot be created or modified. Please update through the Identity Provider.",
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}
	schemaProvider := provider.NewProfileSchemaProvider()
	schemaService := schemaProvider.GetProfileSchemaService()
	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}
	err = schemaService.UpdateProfileSchemaAttributeById(ctx, orgHandle, attributeId, updates, scope)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	attribute, err := schemaService.GetProfileSchemaAttributeById(ctx, orgHandle, attributeId)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(attribute)
}

// DeleteProfileSchema removes the entire profile schema.
func (psh *ProfileSchemaHandler) DeleteProfileSchema(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	orgHandle := utils.ExtractOrgHandleFromPath(r)
	err := security.AuthnAndAuthz(r, "profile_schema:delete")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	schemaProvider := provider.NewProfileSchemaProvider()
	schemaService := schemaProvider.GetProfileSchemaService()
	err = schemaService.DeleteProfileSchema(ctx, orgHandle)

	if err != nil {
		utils.HandleError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

// DeleteProfileSchemaAttributeById removes a profile schema attribute.
func (psh *ProfileSchemaHandler) DeleteProfileSchemaAttributeById(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	attributeId := r.PathValue("attrID")
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	err := security.AuthnAndAuthz(r, "profile_schema:delete")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	scope := r.PathValue("scope")
	if !constants.AllowedAttributesScope[scope] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Code,
			Message:     errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Message,
			Description: "Invalid scope for profile schema attribute: " + scope,
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}
	schemaProvider := provider.NewProfileSchemaProvider()
	schemaService := schemaProvider.GetProfileSchemaService()

	err = schemaService.DeleteProfileSchemaAttributeById(ctx, orgHandle, attributeId)

	if err != nil {
		utils.HandleError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

func (psh *ProfileSchemaHandler) DeleteProfileSchemaAttributeForScope(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "profile_schema:delete")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)

	if !isCDSEnabled(ctx, orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	scope := r.PathValue("scope")
	if !constants.AllowedAttributesScope[scope] {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Code,
			Message:     errors2.PROFILE_SCHEMA_ADD_BAD_REQUEST.Message,
			Description: "Invalid scope for profile schema attribute: " + scope,
		}, http.StatusBadRequest)
		utils.WriteErrorResponse(w, clientError)
		return
	}
	if scope == constants.IdentityAttributes {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.INVALID_ATTRIBUTE.Code,
			Message:     errors2.INVALID_ATTRIBUTE.Message,
			Description: "Identity attributes cannot be created or modified via this endpoint.",
		}, http.StatusMethodNotAllowed)
		utils.WriteErrorResponse(w, clientError)
		return
	}
	schemaProvider := provider.NewProfileSchemaProvider()
	schemaService := schemaProvider.GetProfileSchemaService()

	err = schemaService.DeleteProfileSchemaAttributesByScope(ctx, orgHandle, scope)

	if err != nil {
		utils.HandleError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

func (psh *ProfileSchemaHandler) SyncProfileSchema(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	err := security.AuthnWithAdminCredentials(r)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	var schemaSync model.ProfileSchemaSync
	if err := json.NewDecoder(r.Body).Decode(&schemaSync); err != nil {
		http.Error(w, "Invalid request.", http.StatusBadRequest)
		return
	}
	orgId := schemaSync.OrgId
	if orgId == "" {
		errMsg := fmt.Sprintf("Organization handle cannot be empty in sync event: %s", schemaSync.Event)
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errMsg,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	logger := log.GetLogger()
	if !isCDSEnabled(ctx, orgId) {
		errMsg := "Unable to process profile sync event as CDS is not enabled for organization: " + orgId
		logger.Info(errMsg)
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errMsg,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	logger.Info(fmt.Sprintf("Received schema sync request: %s for organization: %s ", schemaSync.Event, schemaSync.OrgId))

	if schemaSync.Event == constants.AddScimAttributeEvent || schemaSync.Event == constants.UpdateScimAttributeEvent ||
		schemaSync.Event == constants.DeleteScimAttributeEvent ||
		schemaSync.Event == constants.UpdateLocalAttributeEvent || schemaSync.Event == constants.DeleteLocalClaimEvent {
		// Enqueue the schema sync job for asynchronous processing
		err := workers.EnqueueSchemaSyncJob(schemaSync)
		if err != nil {
			errMsg := fmt.Sprintf("Unable to process schema sync request for organization: %s. The system is currently at capacity. Please try again in a few moments.", schemaSync.OrgId)
			logger.Error(errMsg)
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.SYNC_PROFILE_SCHEMA.Code,
				Message:     errors2.SYNC_PROFILE_SCHEMA.Message,
				Description: errMsg,
			}, fmt.Errorf("error enqueuing schema sync job"))
			utils.HandleError(w, serverError)
			return
		}

		logger.Debug("Schema sync request enqueued successfully for organization: " + schemaSync.OrgId)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Profile schema sync request accepted for processing."})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "Unknown sync event."})

}

// isCDSEnabled checks if CDS is enabled for the given organization
func isCDSEnabled(ctx context.Context, orgHandle string) bool {
	return adminConfigService.GetAdminConfigService().IsCDSEnabled(ctx, orgHandle)
}
