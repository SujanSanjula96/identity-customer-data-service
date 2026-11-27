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

// Package handler serves the profile REST API endpoints.
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	adminConfigPkg "github.com/wso2/identity-customer-data-service/internal/admin_config/provider"
	adminConfigService "github.com/wso2/identity-customer-data-service/internal/admin_config/service"
	appProvider "github.com/wso2/identity-customer-data-service/internal/application/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/audit"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/pagination"
	"github.com/wso2/identity-customer-data-service/internal/system/security"

	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	"github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/internal/profile/provider"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	"github.com/wso2/identity-customer-data-service/internal/system/authn"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

// ProfileHandler handles HTTP requests for customer profiles.
type ProfileHandler struct {
	store map[string]model.Profile
	mu    *sync.RWMutex
}

// NewProfileHandler creates a ProfileHandler.
func NewProfileHandler() *ProfileHandler {
	return &ProfileHandler{
		store: make(map[string]model.Profile),
		mu:    &sync.RWMutex{},
	}
}

// GetProfile handles profile retrieval requests
func (ph *ProfileHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "profile:view")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	if !isCDSEnabled(ctx, org.ID) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDSNotEnabled.Code,
			Message:     errors2.CDSNotEnabled.Message,
			Description: errors2.CDSNotEnabled.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	profileID := r.PathValue("profileId")
	if profileID == "" {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: "Invalid path for profile retrieval",
		}, http.StatusNotFound)
		utils.HandleError(w, clientError)
		return
	}
	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()
	profile, err := profilesService.GetProfile(ctx, profileID, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	caller, err := resolveCallerApp(ctx, r, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	profile, err = filterProfileForCaller(ctx, r, org.ID, caller, profile)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(profile)
}

// DeleteProfile handles profile deletion
func (ph *ProfileHandler) DeleteProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "profile:delete")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDSNotEnabled.Code,
			Message:     errors2.CDSNotEnabled.Message,
			Description: errors2.CDSNotEnabled.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	profileID := r.PathValue("profileId")
	if profileID == "" {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.DeleteProfile.Code,
			Message:     errors2.DeleteProfile.Message,
			Description: "Invalid path for profile deletion",
		}, http.StatusNotFound)
		utils.HandleError(w, clientError)
		return
	}
	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()
	err = profilesService.DeleteProfile(ctx, profileID, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetAllProfiles lists the organization's profiles with cursor pagination, applying the filter and
// fuzzyFilter query parameters when present.
func (ph *ProfileHandler) GetAllProfiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := security.AuthnAndAuthz(r, "profile:view"); err != nil {
		utils.HandleError(w, err)
		return
	}

	logger := log.GetLogger()
	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	if !isCDSEnabled(ctx, org.ID) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDSNotEnabled.Code,
			Message:     errors2.CDSNotEnabled.Message,
			Description: errors2.CDSNotEnabled.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	limit, lerr := pagination.ParsePageSize(r)
	if lerr != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: lerr.Error(),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	cursorStr := strings.TrimSpace(r.URL.Query().Get("cursor"))
	cursor, cerr := model.DecodeProfileCursor(cursorStr)
	if cerr != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: cerr.Error(),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	// Parse deterministic filters (filter=...).
	filters := parseFilterStrings(r.URL.Query()[constants.Filter])

	// Parse fuzzy filters (fuzzyFilter=...). These always use eq and go through the IR engine.
	fuzzyFilters := parseFilterStrings(r.URL.Query()[constants.FuzzyFilter])

	requestedAttrs := parseRequestedAttributes(r)

	// Listing reads the same profiles as GetProfile, so it applies the same rules: a system
	// application sees full profiles, any other caller only its own application data and
	// what each profile has consented to share.
	callerAppIdentifier, isSystemApp, err := resolveCaller(ctx, r, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	var consentIDs []string
	if !isSystemApp {
		filters = profileService.ScopeFiltersToCaller(filters, callerAppIdentifier)
		consentIDs = parseCommaSeparatedOrRepeated(r.URL.Query()["consentCategoryId"])
	}

	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	hasFuzzy := len(fuzzyFilters) > 0
	hasDeterministic := len(filters) > 0

	if hasFuzzy {
		// Parse optional threshold query param (applies to both fuzzy-only and hybrid paths).
		var threshold float64
		thresholdStr := strings.TrimSpace(r.URL.Query().Get("threshold"))
		if thresholdStr != "" {
			parsedThreshold, parseErr := strconv.ParseFloat(thresholdStr, 64)
			if parseErr != nil {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.GetProfile.Code,
					Message:     errors2.GetProfile.Message,
					Description: fmt.Sprintf("Invalid threshold value: %s", thresholdStr),
				}, http.StatusBadRequest)
				utils.HandleError(w, clientError)
				return
			}
			threshold = parsedThreshold
		}

		var fuzzyResults []model.FuzzyMatchResult
		var fuzzyErr error

		if hasDeterministic {
			fuzzyResults, fuzzyErr = profilesService.GetProfilesHybrid(
				r.Context(),
				org.ID,
				fuzzyFilters,
				filters,
				threshold,
				limit,
			)
		} else {
			fuzzyResults, fuzzyErr = profilesService.GetProfilesWithFuzzyResolution(r.Context(), org.ID, fuzzyFilters,
				threshold, limit)
		}

		if fuzzyErr != nil {
			utils.HandleError(w, fuzzyErr)
			return
		}

		if !isSystemApp {
			searchedOn := append(append([]string{}, fuzzyFilters...), filters...)
			fuzzyResults, fuzzyErr = profileService.FilterFuzzyResultsForCaller(ctx, fuzzyResults, org.ID,
				callerAppIdentifier, consentIDs, searchedOn)
			if fuzzyErr != nil {
				utils.HandleError(w, fuzzyErr)
				return
			}
		}

		items := buildFuzzyResponseItems(fuzzyResults, requestedAttrs)

		resp := model.ProfileListAPIResponse{
			Pagination: pagination.Pagination{
				Count:    len(items),
				PageSize: limit,
			},
			Items: items,
		}

		utils.RespondJSON(w, http.StatusOK, resp, constants.ProfileResource)
		return
	}

	var (
		profiles []model.ProfileResponse
		hasMore  bool
	)

	if len(filters) > 0 {
		logger.Info("Fetching profiles with filters + cursor pagination")
		profiles, hasMore, err = profilesService.GetAllProfilesWithFilterCursor(ctx, org.ID, filters, limit, cursor)
	} else {
		logger.Info("Fetching all profiles + cursor pagination")
		profiles, hasMore, err = profilesService.GetAllProfilesCursor(ctx, org.ID, limit, cursor)
	}

	if err != nil {
		utils.HandleError(w, err)
		return
	}

	visible := profiles
	if !isSystemApp {
		visible, err = profileService.FilterProfilesForCaller(ctx, profiles, org.ID, callerAppIdentifier,
			consentIDs, filters)
		if err != nil {
			utils.HandleError(w, err)
			return
		}
	}

	// The cursors below stay on the profiles the store returned, so that leaving some out
	// does not move the next page.
	items := buildProfileListResponse(visible, requestedAttrs)

	var nextCursorStr, prevCursorStr string

	if len(profiles) > 0 {
		first := profiles[0]
		last := profiles[len(profiles)-1]

		// Determine request direction (first page behaves like "next")
		reqDir := model.CursorNext
		if cursor != nil && strings.TrimSpace(cursor.Direction) != "" {
			reqDir = strings.TrimSpace(cursor.Direction)
		}

		// next cursor (go older): only if there are more results in "next" direction
		// - When reqDir == "next": hasMore means more older pages
		// - When reqDir == "prev": we don't know from hasMore; but user can still go older from here
		//   so return next cursor if the request had a cursor (you navigated) OR if hasMore in next mode.
		if reqDir == model.CursorNext {
			if hasMore {
				nextCursorStr = model.EncodeProfileCursor(model.ProfileCursor{
					CreatedAt: last.Meta.CreatedAt,
					ProfileID: last.ProfileID,
					Direction: model.CursorNext,
				})
			}
			// prev cursor (go newer): exists whenever this was not the first request
			if cursor != nil {
				prevCursorStr = model.EncodeProfileCursor(model.ProfileCursor{
					CreatedAt: first.Meta.CreatedAt,
					ProfileID: first.ProfileID,
					Direction: model.CursorPrev,
				})
			}
		} else { // reqDir == "prev"
			// prev cursor (go newer): ONLY if there are more newer rows (hasMore in prev mode)
			if hasMore {
				prevCursorStr = model.EncodeProfileCursor(model.ProfileCursor{
					CreatedAt: first.Meta.CreatedAt,
					ProfileID: first.ProfileID,
					Direction: model.CursorPrev,
				})
			}

			// next cursor (go older): always provide it if we navigated using a cursor
			// (lets you go forward again after going back)
			if cursor != nil {
				nextCursorStr = model.EncodeProfileCursor(model.ProfileCursor{
					CreatedAt: last.Meta.CreatedAt,
					ProfileID: last.ProfileID,
					Direction: model.CursorNext,
				})
			}
		}
	}

	resp := model.ProfileListAPIResponse{
		Pagination: pagination.Pagination{
			Count:          len(items),
			PageSize:       limit,
			NextCursor:     nextCursorStr,
			PreviousCursor: prevCursorStr,
		},
		Items: items,
	}

	utils.RespondJSON(w, http.StatusOK, resp, constants.ProfileResource)
}

func buildProfileListResponse(
	profiles []model.ProfileResponse,
	requestedAttrs map[string][]string,
) []model.ProfileListResponse {
	result := make([]model.ProfileListResponse, 0, len(profiles))

	for _, profile := range profiles {
		profileRes := model.ProfileListResponse{
			ProfileID:  profile.ProfileID,
			Meta:       profile.Meta,
			UserID:     profile.UserID,
			MergedFrom: profile.MergedFrom,
		}

		if requestedAttrs == nil {
			result = append(result, profileRes)
			continue
		}

		// Identity Attributes
		if fields, ok := requestedAttrs["identity_attributes"]; ok {
			filtered := make(map[string]interface{})
			for _, f := range fields {
				if f == "*" {
					filtered = profile.IdentityAttributes
					break
				}
				if val, exists := profile.IdentityAttributes[f]; exists {
					filtered[f] = val
				}
			}
			profileRes.IdentityAttributes = filtered
		}

		// Traits
		if fields, ok := requestedAttrs["traits"]; ok {
			filtered := make(map[string]interface{})
			for _, f := range fields {
				if f == "*" {
					filtered = profile.Traits
					break
				}
				if val, exists := profile.Traits[f]; exists {
					filtered[f] = val
				}
			}
			profileRes.Traits = filtered
		}

		// Application Data
		if fields, ok := requestedAttrs["application_data"]; ok {
			appData := profile.ApplicationData
			if len(appData) > 0 {
				filteredAppData := make(map[string]map[string]interface{})

				if len(requestedAttrs["application_data"]) == 0 {
					filteredAppData = appData
				} else {
					for appKey, appFields := range appData {
						temp := make(map[string]interface{})
						for _, f := range fields {
							if f == "*" {
								temp = appFields
								break
							}
							if val, ok := appFields[f]; ok {
								temp[f] = val
							}
						}
						if len(temp) > 0 {
							filteredAppData[appKey] = temp
						}
					}
				}
				if len(filteredAppData) > 0 {
					profileRes.ApplicationData = filteredAppData
				}
			}
		}

		result = append(result, profileRes)
	}

	return result
}

func parseRequestedAttributes(r *http.Request) map[string][]string {
	attrs := r.URL.Query().Get("attributes")
	if attrs == "" {
		return nil
	}

	result := make(map[string][]string)
	for _, attr := range strings.Split(attrs, ",") {
		attr = strings.TrimSpace(attr)
		parts := strings.SplitN(attr, ".", 2)
		scope := parts[0]
		if len(parts) == 2 {
			result[scope] = append(result[scope], parts[1])
		} else {
			result[scope] = append(result[scope], "*")
		}
	}
	return result
}

// InitProfile initializes a new profile based on the request body and sets a cookie
func (ph *ProfileHandler) InitProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "profile:create")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	if !isCDSEnabled(ctx, org.ID) {
		errMsg := "CDD is not enabled for organization: " + org.ID
		log.GetLogger().Info(errMsg)
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDSNotEnabled.Code,
			Message:     errors2.CDSNotEnabled.Message,
			Description: errMsg,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	var profile model.ProfileCreateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&profile)

	if err != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.AddProfile.Code,
			Message:     errors2.AddProfile.Message,
			Description: utils.HandleDecodeError(err, "profile"),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	caller, err := resolveCallerApp(ctx, r, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if err := checkApplicationDataWrite(caller, profile.ApplicationData); err != nil {
		utils.HandleError(w, err)
		return
	}

	// Check if a valid cookie exists
	profileCookie, err := r.Cookie(constants.ProfileCookie)
	if err == nil && profileCookie.Value != "" {
		if ph.handleExistingCookie(w, r, org.ID, caller, profileCookie.Value) {
			return
		}
	}

	// If no valid cookie, create a new profile and cookie
	profileResponse, err := profilesService.CreateProfile(ctx, profile.ToProfileRequest(), org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	// Set the target now, so that a later failure still names the created profile.
	audit.SetTargetID(ctx, profileResponse.ProfileID)

	cookie, err := profilesService.CreateProfileCookie(ctx, profileResponse.ProfileID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	// Create and set a new cookie
	setProfileCookie(w, cookie.CookieID)

	profileResponse.AnonymousTracker = cookie.CookieID

	w.Header().Set("Location", profileResponse.Meta.Location)
	utils.RespondJSON(w, http.StatusCreated, profileResponse, constants.ProfileResource)
}

// Handles existing cookie logic, returns true if response was already written
func (ph *ProfileHandler) handleExistingCookie(w http.ResponseWriter, r *http.Request, orgID string,
	caller callerApp, cookieVal string) bool {
	ctx := r.Context()

	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()
	cookieObj, err := profilesService.GetProfileCookieByID(ctx, cookieVal, orgID)
	if err != nil || cookieObj == nil {
		return false
	}

	if !cookieObj.IsActive {
		return false
	}
	// The request resolves to the profile of the active cookie.
	audit.SetTargetID(ctx, cookieObj.ProfileID)
	profileResponse, err := profilesService.GetProfile(ctx, cookieObj.ProfileID, orgID)
	if err != nil {
		utils.HandleError(w, err)
		return true
	}
	// The profile of a cookie can be merged into a profile that holds the data of other
	// applications, so the caller gets only what it may read, as with GetProfile.
	profileResponse, err = filterProfileForCaller(ctx, r, orgID, caller, profileResponse)
	if err != nil {
		utils.HandleError(w, err)
		return true
	}

	setProfileCookie(w, cookieObj.CookieID)
	profileResponse.AnonymousTracker = cookieObj.CookieID
	utils.RespondJSON(w, http.StatusOK, profileResponse, constants.ProfileResource)
	return true
}

func setProfileCookie(w http.ResponseWriter, cookieID string) {
	cookie := &http.Cookie{
		Name:     constants.ProfileCookie,
		Value:    cookieID,
		Path:     "/",
		Domain:   resolveDomain(),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)
}

func resolveDomain() string {
	authServerConfig := config.GetCDSRuntime().Config.AuthServer
	return authServerConfig.CookieDomain
}

// UpdateProfile updates a profile from the request body and responds with the updated profile.
func (ph *ProfileHandler) UpdateProfile(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	err := security.AuthnAndAuthz(request, "profile:update")
	if err != nil {
		utils.HandleError(writer, err)
		return
	}

	org, err := orgService.ResolveOrganization(request.Context(), utils.ExtractOrgHandleFromPath(request))
	if err != nil {
		utils.HandleError(writer, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDSNotEnabled.Code,
			Message:     errors2.CDSNotEnabled.Message,
			Description: errors2.CDSNotEnabled.Description,
		}, http.StatusBadRequest)
		utils.HandleError(writer, clientError)
		return
	}

	profileID := request.PathValue("profileId")
	if profileID == "" {
		http.Error(writer, "Invalid path", http.StatusNotFound)
		return
	}

	var profile model.ProfileRequest
	err = json.NewDecoder(request.Body).Decode(&profile)
	if err != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: utils.HandleDecodeError(err, "profile"),
		}, http.StatusBadRequest)
		utils.HandleError(writer, clientError)
		return
	}

	caller, err := resolveCallerApp(ctx, request, org.ID)
	if err != nil {
		utils.HandleError(writer, err)
		return
	}
	if err := checkApplicationDataWrite(caller, profile.ApplicationData); err != nil {
		utils.HandleError(writer, err)
		return
	}

	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	_, err = profilesService.UpdateProfile(ctx, profileID, org.ID, profile)
	if err != nil {
		utils.HandleError(writer, err)
		return
	}

	profileResponse, err := profilesService.GetProfile(ctx, profileID, org.ID)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to update profile with profileId: %s", profileID)
		log.GetLogger().Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errMsg,
		}, err)
		utils.HandleError(writer, serverError)
		return
	}
	profileResponse, err = filterProfileForCaller(ctx, request, org.ID, caller, profileResponse)
	if err != nil {
		utils.HandleError(writer, err)
		return
	}
	utils.RespondJSON(writer, http.StatusOK, profileResponse, constants.ProfileResource)
}

// PatchProfile handles partial updates to a profile
func (ph *ProfileHandler) PatchProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "profile:update")
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDSNotEnabled.Code,
			Message:     errors2.CDSNotEnabled.Message,
			Description: errors2.CDSNotEnabled.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	profileID := r.PathValue("profileId")
	if profileID == "" {
		http.Error(w, "Invalid path", http.StatusNotFound)
		return
	}

	var patchData model.ProfilePatchRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&patchData); err != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: utils.HandleDecodeError(err, "profile"),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	caller, err := resolveCallerApp(ctx, r, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	// applyPatch merges only an application_data object, so only one can write the data of an
	// application.
	if patchData.ApplicationData != nil {
		if err := checkApplicationDataWrite(caller, patchData.ApplicationData); err != nil {
			utils.HandleError(w, err)
			return
		}
	}

	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()
	// The service reads the profile back after the update, so its answer is the response.
	profileResponse, err := profilesService.PatchProfile(ctx, profileID, org.ID, patchData)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	profileResponse, err = filterProfileForCaller(ctx, r, org.ID, caller, profileResponse)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	utils.RespondJSON(w, http.StatusOK, profileResponse, constants.ProfileResource)
}

// LinkProfile links an anonymous profile to a user
func (ph *ProfileHandler) LinkProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := security.AuthnAndAuthz(r, "profile:link")
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDSNotEnabled.Code,
			Message:     errors2.CDSNotEnabled.Message,
			Description: errors2.CDSNotEnabled.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	var linkRequest model.ProfileLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&linkRequest); err != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: utils.HandleDecodeError(err, "profile link"),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	profileID := strings.TrimSpace(linkRequest.ProfileID)
	if profileID == "" {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: "profile_id is required to link a profile",
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	userID := strings.TrimSpace(linkRequest.UserID)
	if userID == "" {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: "user_id is required to link a profile",
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	// Set the target before the lookup, so that every outcome names the profile and the user.
	audit.SetTargetID(ctx, profileID)
	audit.AddDetail(ctx, "user_id", userID)

	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	existingProfile, err := profilesService.GetProfile(ctx, profileID, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	audit.SetTargetID(ctx, auditTargetOf(existingProfile))

	if existingProfile.UserID != "" {
		if existingProfile.UserID != userID {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.ProfileAlreadyLinked.Code,
				Message:     errors2.ProfileAlreadyLinked.Message,
				Description: errors2.ProfileAlreadyLinked.Description,
			}, http.StatusConflict)
			utils.HandleError(w, clientError)
			return
		}

		linkResponse := model.ProfileLinkResponse{
			ProfileID: profileID,
			UserID:    userID,
		}
		utils.RespondJSON(w, http.StatusOK, linkResponse, constants.ProfileResource)
		return
	}

	// When the user already has a profile, the anonymous identity attributes only fill those it has no value for.
	identityAttributes := existingProfile.IdentityAttributes
	userProfile, err := profilesService.FindProfileByUserID(ctx, userID, org.ID)
	if err != nil && !utils.HasClientErrorCode(err, errors2.ProfileNotFound.Code) {
		utils.HandleError(w, err)
		return
	}
	if err == nil && userProfile != nil {
		identityAttributes = profileService.WithoutAttributesOf(identityAttributes, userProfile.IdentityAttributes)
	}

	profileRequest := model.ProfileRequest{
		UserID:             userID,
		IdentityAttributes: identityAttributes,
		Traits:             existingProfile.Traits,
		ApplicationData:    profileService.WideAppDataMap(existingProfile.ApplicationData),
	}

	_, err = profilesService.UpdateProfile(ctx, profileID, org.ID, profileRequest)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	log.GetLogger().Info(fmt.Sprintf("Linked profile: %s to user: %s in organization: %s", profileID, userID,
		org.ID))

	linkResponse := model.ProfileLinkResponse{
		ProfileID: profileID,
		UserID:    userID,
	}
	utils.RespondJSON(w, http.StatusOK, linkResponse, constants.ProfileResource)
}

// SyncProfile handles a user change sent by the identity server with the admin credentials, and applies it to
// the profile of that user.
func (ph *ProfileHandler) SyncProfile(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	err := security.AuthnWithAdminCredentials(request)
	if err != nil {
		utils.HandleError(writer, err)
		return
	}

	var profileSync model.ProfileSync
	logger := log.GetLogger()
	err = json.NewDecoder(request.Body).Decode(&profileSync)
	if err != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: utils.HandleDecodeError(err, "profile"),
		}, http.StatusBadRequest)
		utils.HandleError(writer, clientError)
		return
	}

	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	profileID := profileSync.ProfileID
	// The ID from the body is the attempted target. Each branch below replaces it with the profile
	// that the branch resolves and changes.
	audit.SetTargetID(ctx, profileID)
	audit.AddDetail(ctx, "sync_event", profileSync.Event)
	identityClaims := profileSync.Claims
	orgHandle := profileSync.OrgHandle
	// The body names the organization of the user, which can differ from the organization of the path.
	audit.AddDetail(ctx, "synced_org", orgHandle)

	if orgHandle == "" {
		// orgHandle = utils.ExtractOrgHandleFromPath(request)
		// todo: should we expect orgHandle in the path or as body param
		errMsg := fmt.Sprintf("Organization handle cannot be empty in profile sync event: %s", profileSync.Event)
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errMsg,
		}, http.StatusBadRequest)
		utils.HandleError(writer, clientError)
		return
	}

	// An identity server event must not store an organization that CDS does not know.
	org, known, err := orgService.LookupOrganization(ctx, orgHandle)
	if err != nil {
		utils.HandleError(writer, err)
		return
	}
	if !known || !isCDSEnabled(ctx, org.ID) {
		// An organization that CDS does not know has no org ID.
		orgLabel := orgHandle
		if known {
			orgLabel = org.ID
		}
		errMsg := "Unable to process profile sync event as CDD is not enabled for organization: " + orgLabel
		log.GetLogger().Info(errMsg)
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDSNotEnabled.Code,
			Message:     errors2.CDSNotEnabled.Message,
			Description: errMsg,
		}, http.StatusBadRequest)
		utils.HandleError(writer, clientError)
		return
	}

	var existingProfile *model.ProfileResponse

	if profileSync.Event == constants.AddUserEvent {
		if profileSync.ProfileCookie != "" && profileSync.UserID != "" {
			logger.Debug("Syncing profile for user id: " + profileSync.UserID + " with a profile cookie")
			cookieObj, err := profilesService.GetProfileCookieByID(ctx, profileSync.ProfileCookie, org.ID)
			if err == nil && cookieObj != nil && cookieObj.IsActive {
				profileID = cookieObj.ProfileID
				logger.Debug("Found active profile cookie with profile id: " + profileID)
				audit.SetTargetID(ctx, profileID)
			}

			// This scenario is when the user anonymously tried and then trying to signup or login. So profile with
			// profile id exists
			existingProfile, err = profilesService.GetProfile(ctx, profileID, org.ID)
			if err != nil {
				utils.HandleError(writer, err)
				return
			}
			if existingProfile != nil && isLinkedToAnotherUser(existingProfile, profileSync.UserID) {
				// The profile of the cookie (or the profile it is merged into) already belongs to a different
				// user. Do not give it to the new user. Create a separate profile for the new user instead.
				logger.Warn(fmt.Sprintf("Profile: %s is already linked to a different user. Not linking it "+
					"to the new user: %s.", existingProfile.ProfileID, profileSync.UserID))
				if err := createProfileForUserIfMissing(ctx, profilesService, profileSync.UserID, org.ID,
					identityClaims); err != nil {
					utils.HandleError(writer, err)
				}
				return
			}
			if existingProfile != nil {
				// Update identity attributes based on claim URIs
				if existingProfile.IdentityAttributes == nil {
					existingProfile.IdentityAttributes = make(map[string]interface{})
				}

				for claimURI, value := range identityClaims {
					attributeKeyPath := extractClaimKeyFromLocalURI(claimURI)
					setNestedMapValue(existingProfile.IdentityAttributes, attributeKeyPath, value)
				}

				profileRequest := model.ProfileRequest{
					UserID:             profileSync.UserID,
					IdentityAttributes: existingProfile.IdentityAttributes,
					Traits:             existingProfile.Traits,
					ApplicationData:    profileService.WideAppDataMap(existingProfile.ApplicationData),
				}

				// Save updated profile
				audit.SetTargetID(ctx, auditTargetOf(existingProfile))
				_, err = profilesService.UpdateProfile(ctx, existingProfile.ProfileID, org.ID, profileRequest)
				if err != nil {
					utils.HandleError(writer, err)
					return
				}
				return
			}
			return
		} else if profileSync.ProfileCookie == "" && profileSync.UserID != "" {
			logger.Debug("Syncing profile for user id: " + profileSync.UserID + " without profile cookie")
			// this is when we create a profile for a new user created in IS
			if err := createProfileForUserIfMissing(ctx, profilesService, profileSync.UserID, org.ID,
				identityClaims); err != nil {
				utils.HandleError(writer, err)
			}
			return
		}
		return
		// if needed can ensure if profile got created
	}

	if profileSync.Event == constants.DeleteUserEvent {
		existingProfile, err = profilesService.FindProfileByUserID(ctx, profileSync.UserID, org.ID)
		if err != nil {
			utils.HandleError(writer, err)
			return
		}
		if existingProfile == nil {
			logger.Debug("No profile found for user: " + profileSync.UserID)
			return
		}
		audit.SetTargetID(ctx, existingProfile.ProfileID)
		err := profilesService.DeleteProfile(ctx, existingProfile.ProfileID, org.ID)
		if err != nil {
			utils.HandleError(writer, err)
			return
		}
		return
	}

	if profileSync.Event == constants.UpdateUserClaimsEvent || profileSync.Event == constants.UpdateUserClaimEvent {
		if profileSync.UserID != "" {
			existingProfile, err = profilesService.FindProfileByUserID(ctx, profileSync.UserID, org.ID)
			if err != nil {
				utils.HandleError(writer, err)
				return
			}
			if existingProfile == nil {
				log.GetLogger().Info("creating new profile for user: " + profileSync.UserID)
				identityAttributes := make(map[string]interface{})

				for claimURI, value := range identityClaims {
					attributeKeyPath := extractClaimKeyFromLocalURI(claimURI)
					setNestedMapValue(identityAttributes, attributeKeyPath, value)
				}

				profileRequest := model.ProfileRequest{
					UserID:             profileSync.UserID,
					IdentityAttributes: identityAttributes,
				}
				created, err := profilesService.CreateProfile(ctx, profileRequest, org.ID)
				if err != nil {
					utils.HandleError(writer, err)
					return
				}
				audit.SetTargetID(ctx, created.ProfileID)
				return
			} else {
				audit.SetTargetID(ctx, existingProfile.ProfileID)
				// Update identity attributes based on claim URIs
				if existingProfile.IdentityAttributes == nil {
					existingProfile.IdentityAttributes = make(map[string]interface{})
				}

				for claimURI, value := range identityClaims {
					attributeKeyPath := extractClaimKeyFromLocalURI(claimURI)
					setNestedMapValue(existingProfile.IdentityAttributes, attributeKeyPath, value)
				}

				profileRequest := model.ProfileRequest{
					UserID:             existingProfile.UserID,
					IdentityAttributes: existingProfile.IdentityAttributes,
					Traits:             existingProfile.Traits,
					ApplicationData:    profileService.WideAppDataMap(existingProfile.ApplicationData),
				}

				// Save updated profile
				_, err = profilesService.UpdateProfile(ctx, existingProfile.ProfileID, org.ID, profileRequest)
				if err != nil {
					utils.HandleError(writer, err)
					return
				}
			}
		}
		return
	}

	if profileSync.Event == constants.AuthenticationSuccess {
		logger.Info("Authentication success event received for user: " + profileSync.UserID)

		if profileSync.UserID == "" {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UpdateProfile.Code,
				Message:     errors2.UpdateProfile.Message,
				Description: "UserId is required for authentication success event.",
			}, http.StatusBadRequest)
			utils.HandleError(writer, clientError)
			return
		}

		// cookie + userId (anonymous → authenticated)
		if profileSync.ProfileCookie != "" {
			logger.Debug("Syncing profile for user id: " + profileSync.UserID +
				" with profile cookie: " + profileSync.ProfileCookie)

			// Step 1: Resolve anonymous profile from cookie
			var anonymousProfile *model.ProfileResponse
			cookieObj, err := profilesService.GetProfileCookieByID(ctx, profileSync.ProfileCookie, org.ID)
			if err != nil {
				if !utils.HasClientErrorCode(err, errors2.ProfileCookieNotFound.Code) {
					utils.HandleError(writer, err)
					return
				}
			}
			if cookieObj != nil && cookieObj.IsActive {
				anonymousProfile, err = profilesService.GetProfile(ctx, cookieObj.ProfileID, org.ID)
				if err != nil {
					logger.Debug("Failed to fetch anonymous profile from cookie",
						log.Error(err))
					anonymousProfile = nil
				}
			}

			// Step 2: Look up existing profile by userId
			existingUserProfile, err := profilesService.FindProfileByUserID(ctx, profileSync.UserID, org.ID)
			if err != nil {
				if !utils.HasClientErrorCode(err, errors2.ProfileNotFound.Code) {
					utils.HandleError(writer, err)
					return
				}
				existingUserProfile = nil
			}

			// Step 3: Handle sub-cases
			if anonymousProfile != nil && anonymousProfile.UserID != "" {
				// The profile of the cookie (or the profile it is merged into) is already linked to a user.
				// Do not give a profile of a different user to this user.
				if isLinkedToAnotherUser(anonymousProfile, profileSync.UserID) {
					logger.Warn(fmt.Sprintf("Profile: %s is already linked to a different user. Not linking it "+
						"to the user: %s.", anonymousProfile.ProfileID, profileSync.UserID))
				} else {
					logger.Debug("Profile: " + anonymousProfile.ProfileID + " is already linked to the user: " +
						profileSync.UserID + ". No action needed.")
				}
			} else if anonymousProfile != nil && existingUserProfile != nil {
				if anonymousProfile.ProfileID != existingUserProfile.ProfileID {
					// Both profiles exist and are different — attach userId on the anonymous profile.
					// UpdateProfile will enqueue for unification. The worker will detect two profiles
					// with the same userId and merge the anonymous profile into the permanent one.
					logger.Info(fmt.Sprintf(
						"Stitching temporary profile %s with the permanent profile %s",
						anonymousProfile.ProfileID, existingUserProfile.ProfileID))

					// The anonymous identity attributes only fill those the permanent profile has no value for.
					profileRequest := model.ProfileRequest{
						UserID: profileSync.UserID,
						IdentityAttributes: profileService.WithoutAttributesOf(anonymousProfile.IdentityAttributes,
							existingUserProfile.IdentityAttributes),
						Traits:          anonymousProfile.Traits,
						ApplicationData: profileService.WideAppDataMap(anonymousProfile.ApplicationData),
					}
					audit.SetTargetID(ctx, auditTargetOf(anonymousProfile))
					_, err = profilesService.UpdateProfile(ctx, anonymousProfile.ProfileID, org.ID, profileRequest)
					if err != nil {
						utils.HandleError(writer, err)
						return
					}
				} else {
					// Cookie already points to the user's profile. Nothing to do.
					logger.Debug("Anonymous and user profile are the same: " +
						existingUserProfile.ProfileID + ". No action needed.")
				}
			} else if anonymousProfile != nil && existingUserProfile == nil {
				// Only anonymous profile exists. Attach userId and make it into a permanent profile.
				logger.Info("Attaching user id: " + profileSync.UserID +
					" to anonymous profile with profile id: " + anonymousProfile.ProfileID)

				profileRequest := model.ProfileRequest{
					UserID:             profileSync.UserID,
					IdentityAttributes: anonymousProfile.IdentityAttributes,
					Traits:             anonymousProfile.Traits,
					ApplicationData:    profileService.WideAppDataMap(anonymousProfile.ApplicationData),
				}

				audit.SetTargetID(ctx, auditTargetOf(anonymousProfile))
				_, err = profilesService.UpdateProfile(ctx, anonymousProfile.ProfileID, org.ID, profileRequest)
				if err != nil {
					utils.HandleError(writer, err)
					return
				}
			} else {
				// Cookie was stale or invalid. No anonymous session data to recover.
				logger.Debug("Cookie was stale or invalid for user id: " + profileSync.UserID +
					". No anonymous profile to link.")
			}
		} else {
			// Decide whether to create a profile here for existing user accounts that misses profile.
			logger.Debug("No profile cookie provided for user id: " + profileSync.UserID)
		}
	}
	// A session that ended by logout or by timing out leaves the cookie of the browser behind. Deactivate it, so
	// that what happens next in that browser is not added to the profile of this user.
	if profileSync.Event == constants.SessionTermination || profileSync.Event == constants.SessionExpiry {
		logger.Info("Event received " + profileSync.Event + " for user: " + profileSync.UserID)
		if profileSync.UserID != "" {
			if strings.TrimSpace(profileSync.ProfileCookie) == "" {
				logger.Debug("No profile cookie provided for session termination. Skipping cookie deactivation.")
				return
			}
			existingProfile, err = profilesService.FindProfileByUserID(ctx, profileSync.UserID, org.ID)
			if err != nil {
				utils.HandleError(writer, err)
				return
			}

			if existingProfile == nil {
				logger.Debug("No profile found for user: " + profileSync.UserID)
				return
			}
			audit.SetTargetID(ctx, existingProfile.ProfileID)

			profileCookie, err := profilesService.GetProfileCookieByID(ctx, profileSync.ProfileCookie, org.ID)
			if err != nil {
				if utils.HasClientErrorCode(err, errors2.ProfileCookieNotFound.Code) {
					logger.Debug("No cookie found during session termination. Skipping cookie deactivation.")
					return
				}
				utils.HandleError(writer, err)
				return
			}

			owned := profileCookie.ProfileID == existingProfile.ProfileID
			if !owned {
				for _, ref := range existingProfile.MergedFrom {
					if ref.ProfileID == profileCookie.ProfileID {
						owned = true
						break
					}
				}
			}
			if !owned {
				logger.Warn("Profile cookie does not belong to the resolved user profile. " +
					"Skipping cookie deactivation.")
				return
			}
			// Use the cookie's own profile ID, not the master profile ID.
			// The cookie may belong to a merged-from (child) profile.
			err = profilesService.UpdateCookieStatusByCookieID(ctx, profileCookie.CookieID, false)
			if err != nil {
				utils.HandleError(writer, err)
				return
			}
		}
	}

	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(`{"status": "updated"}`))
}

// GetProfileConsents handles retrieving consents for a specific profile
func (ph *ProfileHandler) GetProfileConsents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	profileID := r.PathValue("profileId")
	if profileID == "" {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.GetProfileConsent.Code,
			Message:     errors2.GetProfileConsent.Message,
			Description: "Invalid path for profile consents retrieval",
		}, http.StatusNotFound)
		utils.HandleError(w, clientError)
		return
	}

	err := security.AuthnAndAuthz(r, "profile:view")
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDSNotEnabled.Code,
			Message:     errors2.CDSNotEnabled.Message,
			Description: errors2.CDSNotEnabled.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	// Get the profiles provider and service
	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	// Verify profile exists first
	consentRecords, err := profilesService.GetProfileConsents(ctx, profileID, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(consentRecords)
}

// UpdateProfileConsents handles updating consents for a specific profile
func (ph *ProfileHandler) UpdateProfileConsents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	profileID := r.PathValue("profileId")
	if profileID == "" {
		http.Error(w, "Invalid path", http.StatusNotFound)
		return
	}

	err := security.AuthnAndAuthz(r, "profile:update")
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	org, err := orgService.ResolveOrganization(r.Context(), utils.ExtractOrgHandleFromPath(r))
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	if !isCDSEnabled(ctx, org.ID) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDSNotEnabled.Code,
			Message:     errors2.CDSNotEnabled.Message,
			Description: errors2.CDSNotEnabled.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	// Get the profiles provider and service
	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	// Verify profile exists first
	_, err = profilesService.GetProfile(ctx, profileID, org.ID)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	// Parse the request body
	var consentUpdate []model.ConsentRecord
	err = json.NewDecoder(r.Body).Decode(&consentUpdate)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	err = profilesService.UpdateProfileConsents(ctx, profileID, org.ID, consentUpdate)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(consentUpdate)
}

func setNestedMapValue(m map[string]interface{}, path string, value interface{}) {
	parts := strings.Split(path, ".")
	current := m
	for i, part := range parts {
		if i == len(parts)-1 {
			current[part] = value
		} else {
			if next, ok := current[part].(map[string]interface{}); ok {
				current = next
			} else {
				next := make(map[string]interface{})
				current[part] = next
				current = next
			}
		}
	}
	// todo: ensure the value type and also try how we merge the values here.
}

// parseCommaSeparatedOrRepeated handles both repeated params and comma-separated values.
// Supports: ?consentCategoryId=uuid1&consentCategoryId=uuid2
// and:      ?consentCategoryId=uuid1,uuid2
func parseCommaSeparatedOrRepeated(values []string) []string {
	var result []string
	for _, v := range values {
		for _, id := range strings.Split(v, ",") {
			if trimmed := strings.TrimSpace(id); trimmed != "" {
				result = append(result, trimmed)
			}
		}
	}
	return result
}

func extractClaimKeyFromLocalURI(localURI string) string {
	parts := strings.Split(localURI, "/")
	return parts[len(parts)-1]
}

// isLinkedToAnotherUser reports whether the profile already belongs to a user other than userID.
// For a merged (child) profile, GetProfile returns the user ID of the profile it is merged into, so this
// also covers that profile.
// auditTargetOf returns the ID of the profile that a change to profile writes. A profile that is
// merged into another one is changed through its master profile.
func auditTargetOf(profile *model.ProfileResponse) string {
	if profile.MergedTo != nil && profile.MergedTo.ProfileID != "" {
		return profile.MergedTo.ProfileID
	}
	return profile.ProfileID
}

func isLinkedToAnotherUser(profile *model.ProfileResponse, userID string) bool {
	return profile != nil && profile.UserID != "" && profile.UserID != userID
}

// createProfileForUserIfMissing creates a profile for the user from the identity claims, if the user has no
// profile yet.
func createProfileForUserIfMissing(ctx context.Context, profilesService profileService.ProfilesServiceInterface,
	userID, orgID string, identityClaims map[string]interface{}) error {
	existingProfile, err := profilesService.FindProfileByUserID(ctx, userID, orgID)
	if err != nil && !utils.HasClientErrorCode(err, errors2.ProfileNotFound.Code) {
		return err
	}
	if existingProfile != nil {
		audit.SetTargetID(ctx, existingProfile.ProfileID)
		return nil
	}
	identityAttributes := make(map[string]interface{})
	for claimURI, value := range identityClaims {
		attributeKeyPath := extractClaimKeyFromLocalURI(claimURI)
		setNestedMapValue(identityAttributes, attributeKeyPath, value)
	}
	profileRequest := model.ProfileRequest{
		UserID:             userID,
		IdentityAttributes: identityAttributes,
	}
	created, err := profilesService.CreateProfile(ctx, profileRequest, orgID)
	if err != nil {
		return err
	}
	audit.SetTargetID(ctx, created.ProfileID)
	return nil
}

func getCallerClientIDFromRequest(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return ""
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	logger := log.GetLogger()
	if authn.IsJWT(token) {
		claims, err := authn.ParseJWTClaims(token)
		if err != nil {
			logger.Debug("Failed to parse JWT claims for app ID extraction", log.Error(err))
			return ""
		}
		return extractAppIDFromClaims(claims)
	}

	// For opaque tokens, introspect to get the app ID, as the authentication of the request does
	introspectionClaims, err := authn.IntrospectToken(token, utils.ExtractOrgHandleFromPath(r),
		utils.ExtractPrimaryOrgHandle(r))
	if err != nil {
		logger.Debug("Failed to introspect token for app ID extraction", log.Error(err))
		return ""
	}

	return extractAppIDFromClaims(introspectionClaims)
}

// extractAppIDFromClaims tries to extract the application ID from standard claims like "azp" or "client_id"
func extractAppIDFromClaims(claims map[string]interface{}) string {
	// Try azp  (standard OAuth 2.0 claim for app identification)
	if azp, ok := claims[constants.AZPClaim].(string); ok && azp != "" {
		return azp
	}
	// Fall back to client_id
	if clientID, ok := claims[constants.ClientIDClaim].(string); ok && clientID != "" {
		return clientID
	}
	return ""
}

// resolveCaller returns the identifier of the application making the request and whether
// it is a system application. In app_id mode the caller's clientId is resolved to its app
// ID; the system-app check runs on that identifier.
func resolveCaller(ctx context.Context, r *http.Request, orgID string) (string, bool, error) {
	callerClientID := getCallerClientIDFromRequest(r)
	callerAppIdentifier := callerClientID
	if config.GetCDSRuntime().Config.UsesAppIDIdentifier() && callerClientID != "" {
		resolved, err := appProvider.NewApplicationProvider().GetApplicationService().
			ResolveAppIdentifierByClientID(ctx, orgID, callerClientID)
		if err != nil {
			return "", false, err
		}
		callerAppIdentifier = resolved
	}
	return callerAppIdentifier, isCallerSystemApplication(ctx, orgID, callerAppIdentifier), nil
}

// callerApp is the application that makes a request, and whether it is a system application of
// the organization.
type callerApp struct {
	appIdentifier string
	isSystemApp   bool
}

// resolveCallerApp returns the application that makes the request. A handler resolves it once,
// and uses it both to check what the request writes and to filter what the response returns.
func resolveCallerApp(ctx context.Context, r *http.Request, orgID string) (callerApp, error) {
	appIdentifier, isSystemApp, err := resolveCaller(ctx, r, orgID)
	if err != nil {
		return callerApp{}, err
	}
	return callerApp{appIdentifier: appIdentifier, isSystemApp: isSystemApp}, nil
}

// checkApplicationDataWrite refuses a write of the application data of another application. The
// application data of a profile is keyed by application, and an application that is not a system
// application writes only its own, as it reads only its own. A system application writes the data
// of any application.
func checkApplicationDataWrite(caller callerApp, appData map[string]interface{}) error {
	if caller.isSystemApp {
		return nil
	}
	for appID := range appData {
		if caller.appIdentifier == "" || appID != caller.appIdentifier {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:    errors2.FORBIDDEN.Code,
				Message: errors2.FORBIDDEN.Message,
				Description: fmt.Sprintf("An application can write only its own application data, not the data "+
					"of application: %s", appID),
			}, http.StatusForbidden)
		}
	}
	return nil
}

// filterProfileForCaller limits a profile to what the calling application may read. Every
// response that carries a stored profile goes through it, so that no endpoint returns more than
// GetProfile does. A profile that InitProfile has just created needs none: it holds only what the
// caller sent.
func filterProfileForCaller(ctx context.Context, r *http.Request, orgID string, caller callerApp,
	profile *model.ProfileResponse) (*model.ProfileResponse, error) {
	profile.ApplicationData = profileService.FilterApplicationData(
		profile.ApplicationData,
		caller.appIdentifier,
		caller.isSystemApp,
		parseApplicationDataParams(r),
	)
	if caller.isSystemApp {
		return profile, nil
	}

	consentIDs := parseCommaSeparatedOrRepeated(r.URL.Query()["consentCategoryId"])
	filtered, err := profileService.FilterProfileByConsent(ctx, *profile, profile.ProfileID, orgID, consentIDs)
	if err != nil {
		return nil, err
	}
	return &filtered, nil
}

func isCallerSystemApplication(ctx context.Context, orgID string, appID string) bool {
	if appID == "" {
		return false
	}
	adminConfigProvider := adminConfigPkg.NewAdminConfigProvider()
	adminConfigService := adminConfigProvider.GetAdminConfigService()
	isSystemApp, err := adminConfigService.IsSystemApplication(ctx, orgID, appID)
	if err != nil {
		return false
	}
	return isSystemApp
}

// isCDSEnabled checks if CDS is enabled for the given organization
func isCDSEnabled(ctx context.Context, orgID string) bool {
	return adminConfigService.GetAdminConfigService().IsCDSEnabled(ctx, orgID)
}

// parseFilterStrings splits raw query-param filter values on " and " and returns
// a flat slice of individual filter strings, trimming whitespace.
func parseFilterStrings(raw []string) []string {
	result := make([]string, 0)
	for _, f := range raw {
		for _, sf := range strings.Split(f, " and ") {
			sf = strings.TrimSpace(sf)
			if sf != "" {
				result = append(result, sf)
			}
		}
	}
	return result
}

// buildFuzzyResponseItems converts a slice of FuzzyMatchResult into ProfileListResponse
// items, applying requestedAttrs field filtering if specified.
func buildFuzzyResponseItems(
	fuzzyResults []model.FuzzyMatchResult,
	requestedAttrs map[string][]string,
) []model.ProfileListResponse {
	items := make([]model.ProfileListResponse, 0, len(fuzzyResults))
	for _, fr := range fuzzyResults {
		score := fr.MatchScore
		profileRes := model.ProfileListResponse{
			ProfileID:      fr.Profile.ProfileID,
			Meta:           fr.Profile.Meta,
			UserID:         fr.Profile.UserID,
			MatchScore:     &score,
			ScoreBreakdown: fr.ScoreBreakdown,
		}

		if requestedAttrs == nil {
			profileRes.MergedFrom = fr.Profile.MergedFrom
			items = append(items, profileRes)
			continue
		}

		if fields, ok := requestedAttrs["identity_attributes"]; ok {
			filtered := make(map[string]interface{})
			for _, f := range fields {
				if f == "*" {
					filtered = fr.Profile.IdentityAttributes
					break
				}
				if val, exists := fr.Profile.IdentityAttributes[f]; exists {
					filtered[f] = val
				}
			}
			profileRes.IdentityAttributes = filtered
		}

		if fields, ok := requestedAttrs["traits"]; ok {
			filtered := make(map[string]interface{})
			for _, f := range fields {
				if f == "*" {
					filtered = fr.Profile.Traits
					break
				}
				if val, exists := fr.Profile.Traits[f]; exists {
					filtered[f] = val
				}
			}
			profileRes.Traits = filtered
		}

		appData := fr.Profile.ApplicationData
		if len(appData) > 0 {
			filteredAppData := make(map[string]map[string]interface{})
			if len(requestedAttrs["application_data"]) == 0 {
				filteredAppData = appData
			} else {
				fields := requestedAttrs["application_data"]
				for appKey, appFields := range appData {
					temp := make(map[string]interface{})
					for _, f := range fields {
						if f == "*" {
							temp = appFields
							break
						}
						if val, ok := appFields[f]; ok {
							temp[f] = val
						}
					}
					if len(temp) > 0 {
						filteredAppData[appKey] = temp
					}
				}
			}
			if len(filteredAppData) > 0 {
				profileRes.ApplicationData = filteredAppData
			}
		}

		profileRes.MergedFrom = fr.Profile.MergedFrom
		items = append(items, profileRes)
	}
	return items
}
