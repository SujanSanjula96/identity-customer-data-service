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
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	adminConfigPkg "github.com/wso2/identity-customer-data-service/internal/admin_config/provider"
	adminConfigService "github.com/wso2/identity-customer-data-service/internal/admin_config/service"
	appProvider "github.com/wso2/identity-customer-data-service/internal/application/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/client"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/pagination"
	"github.com/wso2/identity-customer-data-service/internal/system/security"

	"github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/internal/profile/provider"
	profileService "github.com/wso2/identity-customer-data-service/internal/profile/service"
	"github.com/wso2/identity-customer-data-service/internal/system/authn"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
)

type ProfileHandler struct {
	store map[string]model.Profile
	mu    *sync.RWMutex
}

func NewProfileHandler() *ProfileHandler {

	return &ProfileHandler{
		store: make(map[string]model.Profile),
		mu:    &sync.RWMutex{},
	}
}

// GetProfile handles profile retrieval requests
func (ph *ProfileHandler) GetProfile(w http.ResponseWriter, r *http.Request) {

	err := security.AuthnAndAuthz(r, "profile:view")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)

	if !isCDSEnabled(orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	profileId := r.PathValue("profileId")
	if profileId == "" {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: "Invalid path for profile retrieval",
		}, http.StatusNotFound)
		utils.HandleError(w, clientError)
		return
	}
	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()
	profile, err := profilesService.GetProfile(profileId)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	filterParams := parseApplicationDataParams(r)
	// In app_id mode the caller's clientId is resolved to its app ID; the system-app check runs on that identifier.
	callerClientID := getCallerClientIDFromRequest(r)
	callerAppIdentifier := callerClientID
	if config.GetCDSRuntime().Config.UsesAppIDIdentifier() && callerClientID != "" {
		resolved, resolveErr := appProvider.NewApplicationProvider().GetApplicationService().
			ResolveAppIdentifierByClientID(orgHandle, callerClientID)
		if resolveErr != nil {
			utils.HandleError(w, resolveErr)
			return
		}
		callerAppIdentifier = resolved
	}
	isSystemApp := isCallerSystemApplication(orgHandle, callerAppIdentifier)

	profile.ApplicationData = profileService.FilterApplicationData(
		profile.ApplicationData,
		callerAppIdentifier,
		isSystemApp,
		filterParams,
	)

	if !isSystemApp {
		consentIds := parseCommaSeparatedOrRepeated(r.URL.Query()["consentCategoryId"])
		filtered, filterErr := profileService.FilterProfileByConsent(*profile, profileId, orgHandle, consentIds)
		if filterErr != nil {
			utils.HandleError(w, filterErr)
			return
		}
		profile = &filtered
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(profile)
}

// GetCurrentUserProfile handles retrieval of the current user's profile
func (ph *ProfileHandler) GetCurrentUserProfile(w http.ResponseWriter, r *http.Request) {

	if err := security.AuthnAndAuthz(r, "profile:view"); err != nil {
		utils.HandleError(w, err)
		return
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !isCDSEnabled(orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	var profileId string
	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	// Try cookie-based resolution first
	cookie, err := r.Cookie(constants.ProfileCookie)
	if err == nil && cookie.Value != "" {
		cookieObj, err := profilesService.GetProfileCookieById(cookie.Value)
		if err == nil && cookieObj != nil && cookieObj.IsActive {
			profileId = cookieObj.ProfileId
		}
	}

	// Fallback to token-based resolution
	if profileId == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")

			claims, err := authn.ParseJWTClaims(token)
			if err != nil {
				http.Error(w, "Invalid token", http.StatusUnauthorized)
				return
			}

			// Extract 'sub' claim and consider it as userId
			sub, ok := claims["sub"]
			if !ok {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.GET_PROFILE.Code,
					Message:     errors2.GET_PROFILE.Message,
					Description: "Unable to resolve profile for current user",
				}, http.StatusUnauthorized)
				utils.HandleError(w, clientError)
				return
			}

			// Lookup profile by username (sub)
			subStr, ok := sub.(string)
			if !ok || subStr == "" {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.GET_PROFILE.Code,
					Message:     errors2.GET_PROFILE.Message,
					Description: "Unable to resolve profile for current user",
				}, http.StatusUnauthorized)
				utils.HandleError(w, clientError)
				return
			}
			profile, err := profilesService.FindProfileByUserId(subStr)
			if err != nil {
				utils.HandleError(w, err)
				return
			}
			if profile == nil {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.GET_PROFILE.Code,
					Message:     errors2.GET_PROFILE.Message,
					Description: "Profile not found for current user",
				}, http.StatusNotFound)
				utils.HandleError(w, clientError)
				return
			}
			profileId = profile.ProfileId
		}
	}

	// Fetch the profile using the resolved profile ID
	profile, err := profilesService.GetProfile(profileId)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	if profile == nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: "Profile not found for current user",
		}, http.StatusNotFound)
		utils.HandleError(w, clientError)
		return
	}

	utils.RespondJSON(w, http.StatusOK, profile, constants.ProfileResource)
}

// DeleteProfile handles profile deletion
func (ph *ProfileHandler) DeleteProfile(w http.ResponseWriter, r *http.Request) {

	err := security.AuthnAndAuthz(r, "profile:delete")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !isCDSEnabled(orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}
	profileId := r.PathValue("profileId")
	if profileId == "" {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.DELETE_PROFILE.Code,
			Message:     errors2.DELETE_PROFILE.Message,
			Description: "Invalid path for profile deletion",
		}, http.StatusNotFound)
		utils.HandleError(w, clientError)
		return
	}
	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()
	err = profilesService.DeleteProfile(profileId)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (ph *ProfileHandler) GetAllProfiles(w http.ResponseWriter, r *http.Request) {

	if err := security.AuthnAndAuthz(r, "profile:view"); err != nil {
		utils.HandleError(w, err)
		return
	}

	logger := log.GetLogger()
	orgHandle := utils.ExtractOrgHandleFromPath(r)

	if !isCDSEnabled(orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	limit, lerr := pagination.ParsePageSize(r)
	if lerr != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: lerr.Error(),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	cursorStr := strings.TrimSpace(r.URL.Query().Get("cursor"))
	cursor, cerr := model.DecodeProfileCursor(cursorStr)
	if cerr != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: cerr.Error(),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	// Parse filters
	queryFilters := r.URL.Query()[constants.Filter]
	filters := make([]string, 0)
	for _, f := range queryFilters {
		splitFilters := strings.Split(f, " and ")
		for _, sf := range splitFilters {
			sf = strings.TrimSpace(sf)
			if sf != "" {
				filters = append(filters, sf)
			}
		}
	}

	requestedAttrs := parseRequestedAttributes(r)

	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	var (
		profiles []model.ProfileResponse
		hasMore  bool
		err      error
	)

	if len(filters) > 0 {
		logger.Info("Fetching profiles with filters + cursor pagination")
		profiles, hasMore, err = profilesService.GetAllProfilesWithFilterCursor(orgHandle, filters, limit, cursor)
	} else {
		logger.Info("Fetching all profiles + cursor pagination")
		profiles, hasMore, err = profilesService.GetAllProfilesCursor(orgHandle, limit, cursor)
	}

	if err != nil {
		utils.HandleError(w, err)
		return
	}

	items := buildProfileListResponse(profiles, requestedAttrs)

	var nextCursorStr, prevCursorStr string

	if len(profiles) > 0 {
		first := profiles[0]
		last := profiles[len(profiles)-1]

		// Determine request direction (first page behaves like "next")
		reqDir := "next"
		if cursor != nil && strings.TrimSpace(cursor.Direction) != "" {
			reqDir = strings.TrimSpace(cursor.Direction)
		}

		// next cursor (go older): only if there are more results in "next" direction
		// - When reqDir == "next": hasMore means more older pages
		// - When reqDir == "prev": we don't know from hasMore; but user can still go older from here
		//   so return next cursor if the request had a cursor (you navigated) OR if hasMore in next mode.
		if reqDir == "next" {
			if hasMore {
				nextCursorStr = model.EncodeProfileCursor(model.ProfileCursor{
					CreatedAt: last.Meta.CreatedAt,
					ProfileId: last.ProfileId,
					Direction: "next",
				})
			}
			// prev cursor (go newer): exists whenever this was not the first request
			if cursor != nil {
				prevCursorStr = model.EncodeProfileCursor(model.ProfileCursor{
					CreatedAt: first.Meta.CreatedAt,
					ProfileId: first.ProfileId,
					Direction: "prev",
				})
			}
		} else { // reqDir == "prev"
			// prev cursor (go newer): ONLY if there are more newer rows (hasMore in prev mode)
			if hasMore {
				prevCursorStr = model.EncodeProfileCursor(model.ProfileCursor{
					CreatedAt: first.Meta.CreatedAt,
					ProfileId: first.ProfileId,
					Direction: "prev",
				})
			}

			// next cursor (go older): always provide it if we navigated using a cursor
			// (lets you go forward again after going back)
			if cursor != nil {
				nextCursorStr = model.EncodeProfileCursor(model.ProfileCursor{
					CreatedAt: last.Meta.CreatedAt,
					ProfileId: last.ProfileId,
					Direction: "next",
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

func buildProfileListResponse(profiles []model.ProfileResponse, requestedAttrs map[string][]string) []model.ProfileListResponse {

	result := make([]model.ProfileListResponse, 0, len(profiles))

	for _, profile := range profiles {
		profileRes := model.ProfileListResponse{
			ProfileId:  profile.ProfileId,
			Meta:       profile.Meta,
			UserId:     profile.UserId,
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
				// Note: Filter out the allowed application data only if it is not a system app."

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

	err := security.AuthnAndAuthz(r, "profile:create")
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	orgHandle := utils.ExtractOrgHandleFromPath(r)

	if !isCDSEnabled(orgHandle) {
		errMsg := "CDS is not enabled for organization: " + orgHandle
		log.GetLogger().Info(errMsg)
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errMsg,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	var profile model.ProfileRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&profile)

	if err != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ADD_PROFILE.Code,
			Message:     errors2.ADD_PROFILE.Message,
			Description: utils.HandleDecodeError(err, "profile"),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	// Check if a valid cookie exists
	profileCookie, err := r.Cookie(constants.ProfileCookie)
	if err == nil && profileCookie.Value != "" {
		if ph.handleExistingCookie(w, r, profileCookie.Value) {
			return
		}
	}

	// If no valid cookie, create a new profile and cookie
	profileResponse, err := profilesService.CreateProfile(profile, orgHandle)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	cookie, err := profilesService.CreateProfileCookie(profileResponse.ProfileId)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	// Create and set a new cookie
	err = setProfileCookie(w, cookie.CookieId, r)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	profileResponse.AnonymousProfileTracker = cookie.CookieId

	// Construct Location header for created resource
	serverURL := config.GetCDSRuntime().Config.ServerURL
	location := fmt.Sprintf("%s/t/%s%s/profiles/%s",
		serverURL,
		orgHandle,
		constants.ApiBasePath+"/v1",
		profileResponse.ProfileId,
	)

	w.Header().Set("Location", location)
	utils.RespondJSON(w, http.StatusCreated, profileResponse, constants.ProfileResource)
}

// Handles existing cookie logic, returns true if response was already written
func (ph *ProfileHandler) handleExistingCookie(w http.ResponseWriter, r *http.Request, cookieVal string) bool {

	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()
	cookieObj, err := profilesService.GetProfileCookieById(cookieVal)
	if err != nil || cookieObj == nil {
		return false
	}

	if !cookieObj.IsActive {
		return false
	}
	profileResponse, err := profilesService.GetProfile(cookieObj.ProfileId)
	if err != nil {
		utils.HandleError(w, err)
		return true
	}

	_ = setProfileCookie(w, cookieObj.CookieId, r)
	profileResponse.AnonymousProfileTracker = cookieObj.CookieId
	utils.RespondJSON(w, http.StatusOK, profileResponse, constants.ProfileResource)
	return true
}

func setProfileCookie(w http.ResponseWriter, cookieId string, r *http.Request) error {
	cookie := &http.Cookie{
		Name:     constants.ProfileCookie,
		Value:    cookieId,
		Path:     "/",
		Domain:   resolveDomain(),
		HttpOnly: true,
		Secure:   !strings.HasPrefix(r.Host, "localhost"),
		SameSite: http.SameSiteNoneMode,
	}
	http.SetCookie(w, cookie)
	return nil
}

func resolveDomain() string {
	authServerConfig := config.GetCDSRuntime().Config.AuthServer
	return authServerConfig.CookieDomain
}

func (ph *ProfileHandler) UpdateProfile(writer http.ResponseWriter, request *http.Request) {

	err := security.AuthnAndAuthz(request, "profile:update")
	if err != nil {
		utils.HandleError(writer, err)
		return
	}

	orgHandle := utils.ExtractOrgHandleFromPath(request)
	if !isCDSEnabled(orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(writer, clientError)
		return
	}

	profileId := request.PathValue("profileId")
	if profileId == "" {
		http.Error(writer, "Invalid path", http.StatusNotFound)
		return
	}

	var profile model.ProfileRequest
	err = json.NewDecoder(request.Body).Decode(&profile)
	if err != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: utils.HandleDecodeError(err, "profile"),
		}, http.StatusBadRequest)
		utils.HandleError(writer, clientError)
		return
	}

	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	_, err = profilesService.UpdateProfile(profileId, orgHandle, profile)
	if err != nil {
		utils.HandleError(writer, err)
		return
	}

	profileResponse, err := profilesService.GetProfile(profileId)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to update profile with profileId: %s", profileId)
		log.GetLogger().Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errMsg,
		}, err)
		utils.HandleError(writer, serverError)
	}
	utils.RespondJSON(writer, http.StatusOK, profileResponse, constants.ProfileResource)
}

// PatchProfile handles partial updates to a profile
func (ph *ProfileHandler) PatchProfile(w http.ResponseWriter, r *http.Request) {

	err := security.AuthnAndAuthz(r, "profile:update")
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !isCDSEnabled(orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	profileId := r.PathValue("profileId")
	if profileId == "" {
		http.Error(w, "Invalid path", http.StatusNotFound)
		return
	}

	var patchData map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&patchData); err != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: utils.HandleDecodeError(err, "profile"),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
	}

	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()
	_, err = profilesService.PatchProfile(profileId, orgHandle, patchData)
	if err != nil {
		utils.HandleError(w, err)
		return
	}
	profileResponse, err := profilesService.GetProfile(profileId)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to update profile with profileId: %s", profileId)
		log.GetLogger().Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errMsg,
		}, err)
		utils.HandleError(w, serverError)
	}
	utils.RespondJSON(w, http.StatusOK, profileResponse, constants.ProfileResource)
}

// LinkProfile links an anonymous profile to a user
func (ph *ProfileHandler) LinkProfile(w http.ResponseWriter, r *http.Request) {

	err := security.AuthnAndAuthz(r, "profile:link")
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !isCDSEnabled(orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	profileId := r.PathValue("profileId")
	if profileId == "" {
		http.Error(w, "Invalid path", http.StatusNotFound)
		return
	}

	var linkRequest model.ProfileLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&linkRequest); err != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: utils.HandleDecodeError(err, "profile link"),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	userId := strings.TrimSpace(linkRequest.UserId)
	if userId == "" {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: "user_id is required to link a profile",
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	existingProfile, err := profilesService.GetProfile(profileId)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	profileRequest := model.ProfileRequest{
		UserId:             userId,
		IdentityAttributes: existingProfile.IdentityAttributes,
		Traits:             existingProfile.Traits,
		ApplicationData:    profileService.WideAppDataMap(existingProfile.ApplicationData),
	}

	_, err = profilesService.UpdateProfile(profileId, orgHandle, profileRequest)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	log.GetLogger().Info(fmt.Sprintf("Linked profile: %s to user: %s in organization: %s", profileId, userId,
		orgHandle))

	linkResponse := model.ProfileLinkResponse{
		ProfileId: profileId,
		UserId:    userId,
	}
	utils.RespondJSON(w, http.StatusOK, linkResponse, constants.ProfileResource)
}

// PatchCurrentUserProfile handles partial updates to the current user's profile
func (ph *ProfileHandler) PatchCurrentUserProfile(w http.ResponseWriter, r *http.Request) {

	logger := log.GetLogger()
	if err := security.AuthnAndAuthz(r, "profile:update"); err != nil {
		utils.HandleError(w, err)
		return
	}

	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !isCDSEnabled(orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	var profileId string
	var err error
	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	// Try cookie-based profileId resolution (preferred)
	cookie, err := r.Cookie(constants.ProfileCookie)
	if err == nil && cookie.Value != "" {
		cookieObj, err := profilesService.GetProfileCookieById(cookie.Value)
		if err == nil && cookieObj != nil && cookieObj.IsActive {
			profileId = cookieObj.ProfileId
		}
	}

	// Fallback: token-based flow (get sub → lookup profile → extract profileId)
	if profileId == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")

			claims, err := authn.ParseJWTClaims(token)
			if err != nil {
				http.Error(w, "Invalid token", http.StatusUnauthorized)
				return
			}

			sub, ok := claims["sub"]
			if !ok {
				http.Error(w, "Missing 'sub' in token", http.StatusUnauthorized)
				return
			}

			// Lookup profile by username (sub)
			subStr, ok := sub.(string)
			if !ok || subStr == "" {
				http.Error(w, "Missing 'sub' in token", http.StatusUnauthorized)
				return
			}

			// Lookup profile by sub (username)
			profile, err := profilesService.FindProfileByUserId(subStr)
			if err != nil || profile == nil {
				http.Error(w, "Profile not found for token subject", http.StatusUnauthorized)
				return
			}
			profileId = profile.ProfileId
		}
	}

	// If still no profileId, reject
	if profileId == "" {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: "Invalid authentication method. No valid profileId found.",
		}, http.StatusUnauthorized)
		utils.HandleError(w, clientError)
		return
	}

	// Parse patch payload
	var patchData map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&patchData); err != nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: utils.HandleDecodeError(err, "profile"),
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	// Apply patch
	updatedProfile, err := profilesService.PatchProfile(profileId, orgHandle, patchData)
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	// Return updated profile
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(updatedProfile); err != nil {
		errMsg := fmt.Sprintf("Failed to encode profile response for profileId: %s", profileId)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errMsg,
		}, err)
		utils.HandleError(w, serverError)
	}
}

func (ph *ProfileHandler) SyncProfile(writer http.ResponseWriter, request *http.Request) {

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
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: utils.HandleDecodeError(err, "profile"),
		}, http.StatusBadRequest)
		utils.HandleError(writer, clientError)
		return
	}

	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	profileId := profileSync.ProfileId
	identityClaims := profileSync.Claims
	orgHandle := profileSync.OrgHandle

	if orgHandle == "" {
		//orgHandle = utils.ExtractOrgHandleFromPath(request)
		//todo: should we expect orgHandle in the path or as body param
		errMsg := fmt.Sprintf("Organization handle cannot be empty in profile sync event: %s", profileSync.Event)
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errMsg,
		}, http.StatusBadRequest)
		utils.HandleError(writer, clientError)
		return
	}

	if !isCDSEnabled(orgHandle) {
		errMsg := "Unable to process profile sync event as CDS is not enabled for organization: " + orgHandle
		log.GetLogger().Info(errMsg)
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errMsg,
		}, http.StatusBadRequest)
		utils.HandleError(writer, clientError)
		return
	}

	var existingProfile *model.ProfileResponse

	if profileSync.Event == constants.AddUserEvent {
		if profileSync.ProfileCookie != "" && profileSync.UserId != "" {
			logger.Debug("Syncing profile for user id: " + profileSync.UserId + " with profile cookie: " + profileSync.ProfileCookie)
			cookieObj, err := profilesService.GetProfileCookieById(profileSync.ProfileCookie)
			if err == nil && cookieObj != nil && cookieObj.IsActive {
				profileId = cookieObj.ProfileId
				logger.Debug("Found active profile cookie with profile id: " + profileId)
			}

			// This scenario is when the user anonymously tried and then trying to signup or login. So profile with profile id exists
			existingProfile, err = profilesService.GetProfile(profileId)
			if err != nil {
				utils.HandleError(writer, err)
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
					UserId:             profileSync.UserId,
					IdentityAttributes: existingProfile.IdentityAttributes,
					Traits:             existingProfile.Traits,
					ApplicationData:    profileService.WideAppDataMap(existingProfile.ApplicationData),
				}

				// Save updated profile
				_, err = profilesService.UpdateProfile(existingProfile.ProfileId, orgHandle, profileRequest)
				if err != nil {
					utils.HandleError(writer, err)
					return
				}
				return
			}
			return
		} else if profileSync.ProfileCookie == "" && profileSync.UserId != "" {
			logger.Debug("Syncing profile for user id: " + profileSync.UserId + " without profile cookie")
			// this is when we create a profile for a new user created in IS
			existingProfile, err = profilesService.FindProfileByUserId(profileSync.UserId)
			if err != nil {
				if !utils.HasClientErrorCode(err, errors2.PROFILE_NOT_FOUND.Code) {
					utils.HandleError(writer, err)
					return
				}
			}
			if existingProfile == nil {
				identityAttributes := make(map[string]interface{})
				for claimURI, value := range identityClaims {
					attributeKeyPath := extractClaimKeyFromLocalURI(claimURI)
					setNestedMapValue(identityAttributes, attributeKeyPath, value)
				}

				profileRequest := model.ProfileRequest{
					UserId:             profileSync.UserId,
					IdentityAttributes: identityAttributes,
				}
				_, err := profilesService.CreateProfile(profileRequest, orgHandle)
				if err != nil {
					utils.HandleError(writer, err)
					return
				}
			}
			return
		}
		return
		// if needed can ensure if profile got created
	}

	if profileSync.Event == constants.DeleteUserEvent {
		existingProfile, err = profilesService.FindProfileByUserId(profileSync.UserId)
		if err != nil {
			utils.HandleError(writer, err)
			return
		}
		if existingProfile == nil {
			logger.Debug("No profile found for user: " + profileSync.UserId)
			return
		}
		err := profilesService.DeleteProfile(existingProfile.ProfileId)
		if err != nil {
			utils.HandleError(writer, err)
			return
		}
		return
	}

	if profileSync.Event == constants.UpdateUserClaimsEvent || profileSync.Event == constants.UpdateUserClaimEvent {
		if profileSync.UserId != "" {
			existingProfile, err = profilesService.FindProfileByUserId(profileSync.UserId)
			if err != nil {
				utils.HandleError(writer, err)
				return
			}
			if existingProfile == nil {
				log.GetLogger().Info("creating new profile for user: " + profileSync.UserId)
				identityAttributes := make(map[string]interface{})

				for claimURI, value := range identityClaims {
					attributeKeyPath := extractClaimKeyFromLocalURI(claimURI)
					setNestedMapValue(identityAttributes, attributeKeyPath, value)
				}

				profileRequest := model.ProfileRequest{
					UserId:             profileSync.UserId,
					IdentityAttributes: identityAttributes,
				}
				_, err := profilesService.CreateProfile(profileRequest, orgHandle)

				if err != nil {
					return
				}
				return

			} else {
				// Update identity attributes based on claim URIs
				if existingProfile.IdentityAttributes == nil {
					existingProfile.IdentityAttributes = make(map[string]interface{})
				}

				for claimURI, value := range identityClaims {
					attributeKeyPath := extractClaimKeyFromLocalURI(claimURI)
					setNestedMapValue(existingProfile.IdentityAttributes, attributeKeyPath, value)
				}

				profileRequest := model.ProfileRequest{
					UserId:             existingProfile.UserId,
					IdentityAttributes: existingProfile.IdentityAttributes,
					Traits:             existingProfile.Traits,
					ApplicationData:    profileService.WideAppDataMap(existingProfile.ApplicationData),
				}

				// Save updated profile
				_, err = profilesService.UpdateProfile(existingProfile.ProfileId, orgHandle, profileRequest)
				if err != nil {
					utils.HandleError(writer, err)
					return
				}
			}
		}
		return
	}

	if profileSync.Event == constants.AuthenticationSuccess {
		logger.Info("Authentication success event received for user: " + profileSync.UserId)

		if profileSync.UserId == "" {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UPDATE_PROFILE.Code,
				Message:     errors2.UPDATE_PROFILE.Message,
				Description: "UserId is required for authentication success event.",
			}, http.StatusBadRequest)
			utils.HandleError(writer, clientError)
			return
		}

		// cookie + userId (anonymous → authenticated)
		if profileSync.ProfileCookie != "" {
			logger.Debug("Syncing profile for user id: " + profileSync.UserId +
				" with profile cookie: " + profileSync.ProfileCookie)

			// Step 1: Resolve anonymous profile from cookie
			var anonymousProfile *model.ProfileResponse
			cookieObj, err := profilesService.GetProfileCookieById(profileSync.ProfileCookie)
			if err != nil {
				if !utils.HasClientErrorCode(err, errors2.PROFILE_COOKIE_NOT_FOUND.Code) {
					utils.HandleError(writer, err)
					return
				}
			}
			if cookieObj != nil && cookieObj.IsActive {
				anonymousProfile, err = profilesService.GetProfile(cookieObj.ProfileId)
				if err != nil {
					logger.Debug("Failed to fetch anonymous profile from cookie",
						log.Error(err))
					anonymousProfile = nil
				}
			}

			// Step 2: Look up existing profile by userId
			existingUserProfile, err := profilesService.FindProfileByUserId(profileSync.UserId)
			if err != nil {
				if !utils.HasClientErrorCode(err, errors2.PROFILE_NOT_FOUND.Code) {
					utils.HandleError(writer, err)
					return
				}
				existingUserProfile = nil
			}

			// Step 3: Handle sub-cases
			if anonymousProfile != nil && existingUserProfile != nil {
				if anonymousProfile.ProfileId != existingUserProfile.ProfileId {
					// Both profiles exist and are different — attach userId on the anonymous profile.
					// UpdateProfile will enqueue for unification. The worker will detect two profiles
					// with the same userId and merge the anonymous profile into the permanent one.
					logger.Info(fmt.Sprintf(
						"Stitching temporary profile %s with the permanent profile %s",
						anonymousProfile.ProfileId, existingUserProfile.ProfileId))

					profileRequest := model.ProfileRequest{
						UserId:             profileSync.UserId,
						IdentityAttributes: anonymousProfile.IdentityAttributes,
						Traits:             anonymousProfile.Traits,
						ApplicationData:    profileService.WideAppDataMap(anonymousProfile.ApplicationData),
					}
					_, err = profilesService.UpdateProfile(anonymousProfile.ProfileId, orgHandle, profileRequest)
					if err != nil {
						utils.HandleError(writer, err)
						return
					}
				} else {
					// Cookie already points to the user's profile. Nothing to do.
					logger.Debug("Anonymous and user profile are the same: " +
						existingUserProfile.ProfileId + ". No action needed.")
				}

			} else if anonymousProfile != nil && existingUserProfile == nil {
				// Only anonymous profile exists. Attach userId and make it into a permanent profile.
				logger.Info("Attaching user id: " + profileSync.UserId +
					" to anonymous profile with profile id: " + anonymousProfile.ProfileId)

				profileRequest := model.ProfileRequest{
					UserId:             profileSync.UserId,
					IdentityAttributes: anonymousProfile.IdentityAttributes,
					Traits:             anonymousProfile.Traits,
					ApplicationData:    profileService.WideAppDataMap(anonymousProfile.ApplicationData),
				}

				_, err = profilesService.UpdateProfile(anonymousProfile.ProfileId, orgHandle, profileRequest)
				if err != nil {
					utils.HandleError(writer, err)
					return
				}

			} else {
				// Cookie was stale or invalid. No anonymous session data to recover.
				logger.Debug("Cookie was stale or invalid for user id: " + profileSync.UserId +
					". No anonymous profile to link.")
			}
		} else {
			// Decide whether to create a profile here for existing user accounts that misses profile.
			logger.Debug("No profile cookie provided for user id: " + profileSync.UserId)
		}
	}
	if profileSync.Event == constants.SessionTermination {
		logger.Info("Event received session termination for user: " + profileSync.UserId)
		if profileSync.UserId != "" {
			if strings.TrimSpace(profileSync.ProfileCookie) == "" {
				logger.Debug("No profile cookie provided for session termination. Skipping cookie deactivation.")
				return
			}
			existingProfile, err = profilesService.FindProfileByUserId(profileSync.UserId)
			if err != nil {
				utils.HandleError(writer, err)
				return
			}

			if existingProfile == nil {
				logger.Debug("No profile found for user: " + profileSync.UserId)
				return
			}

			profileCookie, err := profilesService.GetProfileCookieById(profileSync.ProfileCookie)
			if err != nil {
				if utils.HasClientErrorCode(err, errors2.PROFILE_COOKIE_NOT_FOUND.Code) {
					logger.Debug("No cookie found during session termination. Skipping cookie deactivation.")
					return
				}
				utils.HandleError(writer, err)
				return
			}

			owned := profileCookie.ProfileId == existingProfile.ProfileId
			if !owned {
				for _, ref := range existingProfile.MergedFrom {
					if ref.ProfileId == profileCookie.ProfileId {
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
			err = profilesService.UpdateCookieStatusByCookieId(profileCookie.CookieId, false)
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

	profileId := r.PathValue("profileId")
	if profileId == "" {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE_CONSENT.Code,
			Message:     errors2.GET_PROFILE_CONSENT.Message,
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

	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !isCDSEnabled(orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	// Get the profiles provider and service
	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	// Verify profile exists first
	consentRecords, err := profilesService.GetProfileConsents(profileId)
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

	profileId := r.PathValue("profileId")
	if profileId == "" {
		http.Error(w, "Invalid path", http.StatusNotFound)
		return
	}

	err := security.AuthnAndAuthz(r, "profile:update")
	if err != nil {
		utils.HandleError(w, err)
		return
	}

	orgHandle := utils.ExtractOrgHandleFromPath(r)
	if !isCDSEnabled(orgHandle) {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.CDS_NOT_ENABLED.Code,
			Message:     errors2.CDS_NOT_ENABLED.Message,
			Description: errors2.CDS_NOT_ENABLED.Description,
		}, http.StatusBadRequest)
		utils.HandleError(w, clientError)
		return
	}

	// Get the profiles provider and service
	profilesProvider := provider.NewProfilesProvider()
	profilesService := profilesProvider.GetProfilesService()

	// Verify profile exists first
	_, err = profilesService.GetProfile(profileId)
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

	err = profilesService.UpdateProfileConsents(profileId, orgHandle, consentUpdate)
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

	// For opaque tokens, introspect to get the app ID
	cfg := config.GetCDSRuntime().Config
	identityClient := client.NewIdentityClient(cfg)
	orgHandle := utils.ExtractOrgHandleFromPath(r)

	introspectionClaims, err := identityClient.IntrospectToken(token, orgHandle)
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
	if clientID, ok := claims[constants.ClientIdClaim].(string); ok && clientID != "" {
		return clientID
	}
	return ""
}

func isCallerSystemApplication(orgHandle, appId string) bool {
	if appId == "" {
		return false
	}
	adminConfigProvider := adminConfigPkg.NewAdminConfigProvider()
	adminConfigService := adminConfigProvider.GetAdminConfigService()
	isSystemApp, err := adminConfigService.IsSystemApplication(orgHandle, appId)
	if err != nil {
		return false
	}
	return isSystemApp
}

// isCDSEnabled checks if CDS is enabled for the given organization
func isCDSEnabled(orgHandle string) bool {
	return adminConfigService.GetAdminConfigService().IsCDSEnabled(orgHandle)
}
