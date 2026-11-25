// Package store persists customer profiles, their application data, consents, cookies and references.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/rows"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// The profile fields that are columns of their own rather than attributes.
const (
	fieldUserID    = "user_id"
	fieldProfileID = "profile_id"
)

// Unmarshal JSONB fields separately
func scanProfileRow(row map[string]interface{}) (model.Profile, error) {
	var (
		profile                       model.Profile
		traitsJSON, identityAttrsJSON []byte
	)

	profile.ProfileStatus = &model.ProfileStatus{}

	profile.ProfileID = row[fieldProfileID].(string)
	profile.UserID = row[fieldUserID].(string)
	profile.OrgID = rows.String(row, "org_id")
	profile.CreatedAt = row["created_at"].(time.Time)
	profile.UpdatedAt = row["updated_at"].(time.Time)
	profileStatus := row["profile_status"].(string)
	if profileStatus != "" && profileStatus != "null" {
		if profileStatus == constants.WaitOnAdmin {
			profile.ProfileStatus.IsWaitingOnAdmin = true
		}
		if profileStatus == constants.WaitOnUser {
			profile.ProfileStatus.IsWaitingOnUser = true
		}
		if profileStatus == constants.ReferenceProfile {
			profile.ProfileStatus.IsReferenceProfile = true
		}
		if profileStatus == constants.MergedTo {
			profile.ProfileStatus.ReferenceReason = row["reference_reason"].(string)
			profile.ProfileStatus.ReferenceProfileID = row["reference_profile_id"].(string)
		}
	}

	profile.ProfileStatus.ListProfile = row["list_profile"].(bool)
	traitsJSON = row["traits"].([]byte)
	identityAttrsJSON = row["identity_attributes"].([]byte)

	logger := log.GetLogger()
	// Unmarshal JSON fields
	if err := json.Unmarshal(traitsJSON, &profile.Traits); err != nil {
		errorMsg := "Failed to unmarshal traits"
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
		return model.Profile{}, serverError
	}
	if err := json.Unmarshal(identityAttrsJSON, &profile.IdentityAttributes); err != nil {
		errorMsg := "Failed to unmarshal identity attributes."
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
		return model.Profile{}, serverError
	}
	return profile, nil
}

func scanProfileConsentRow(row map[string]interface{}) model.ConsentRecord {
	var profileConsent model.ConsentRecord

	profileConsent.CategoryIdentifier = row["category_id"].(string)
	profileConsent.IsConsented = row["consent_status"].(bool)
	profileConsent.ConsentedAt = row["consented_at"].(time.Time)
	return profileConsent
}

// InsertProfile inserts a new profile into the database
func InsertProfile(ctx context.Context, orgID string, profile model.Profile) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := "Failed to get database client for adding a profile"
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.AddProfile.Code,
			Message:     errors2.AddProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	traitsJSON, _ := json.Marshal(profile.Traits)
	identityJSON, _ := json.Marshal(profile.IdentityAttributes)
	var profileStatus string
	if profile.ProfileStatus.IsReferenceProfile {
		profileStatus = constants.ReferenceProfile
	} else if profile.ProfileStatus.IsWaitingOnUser {
		profileStatus = constants.WaitOnUser
	} else if profile.ProfileStatus.IsWaitingOnAdmin {
		profileStatus = constants.WaitOnAdmin
	} else {
		profileStatus = constants.MergedTo
	}

	query := scripts.InsertProfile

	_, err = dbClient.ExecuteQueryContext(ctx, query,
		profile.ProfileID,
		profile.UserID,
		orgID,
		profile.CreatedAt,
		profile.UpdatedAt,
		profile.ProfileStatus.ListProfile,
		false, // delete_profile is not used in this context, set to false
		traitsJSON,
		identityJSON,
	)

	if err != nil {
		errorMsg := fmt.Sprintf("Failed to insert profile with Id: %s", profile.ProfileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.AddProfile.Code,
			Message:     errors2.AddProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	referenceQuery := scripts.InsertProfileReference

	_, err = dbClient.ExecuteQueryContext(ctx, referenceQuery,
		profile.ProfileID,
		profileStatus,
		profile.ProfileStatus.ReferenceProfileID,
		profile.ProfileStatus.ReferenceReason,
		orgID,
		orgID,
	)

	if err != nil {
		errorMsg := fmt.Sprintf("Failed to insert profile referencea with Id: %s", profile.ProfileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.AddProfile.Code,
			Message:     errors2.AddProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	err = InsertApplicationData(ctx, profile.ProfileID, profile.ApplicationData)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to insert profile with Id: %s", profile.ProfileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.AddProfile.Code,
			Message:     errors2.AddProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	logger.Info("Profile added successfully: " + profile.ProfileID)
	return nil
}

// InsertApplicationData stores each application's data for the profile, merging it into any existing data.
func InsertApplicationData(ctx context.Context, profileID string, apps []model.ApplicationData) error {
	for _, app := range apps {
		// Construct the update map
		updateMap := make(map[string]interface{})

		// Flatten app-specific fields
		for k, v := range app.AppSpecificData {
			updateMap["application_data."+k] = v
		}

		// Use the existing upsert method
		err := UpsertAppDatum(ctx, profileID, app.AppID, updateMap)
		logger := log.GetLogger()
		if err != nil {
			errorMsg := fmt.Sprintf("Failed to insert application data for profile with Id: %s and appId: %s",
				profileID, app.AppID)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.AddAppData.Code,
				Message:     errors2.AddAppData.Message,
				Description: errorMsg,
			}, err)
			return serverError
		}
	}
	return nil
}

// GetProfileOptions controls which related profile data is loaded.
type GetProfileOptions struct {
	IncludeApplicationData bool
}

// GetProfile retrieves a profile by its ID, including application data.
func GetProfile(ctx context.Context, profileID, orgID string) (*model.Profile, error) {
	return GetProfileWithOptions(ctx, profileID, orgID, GetProfileOptions{IncludeApplicationData: true})
}

// GetProfileWithOptions retrieves a profile by its ID using the supplied loading options.
func GetProfileWithOptions(ctx context.Context, profileID, orgID string,
	options GetProfileOptions) (*model.Profile, error) {
	// An organization that CDS does not know gets an empty key, which matches no row.

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while fetching profile with Id: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.GetProfileByID

	results, err := dbClient.ExecuteQueryContext(ctx, query, profileID, orgID)

	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug(fmt.Sprintf("No profile found with the given Id: %s", profileID))
		// todo: should we return a client error with 404 here?
		return nil, nil
	}
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching profile with Id: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
	}
	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("No profile found with the given Id: %s", profileID))
		// todo: should we return a client error with 404 here?
		return nil, nil
	}
	profile, err := scanProfileRow(results[0])
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching profile with Id: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	if options.IncludeApplicationData {
		profile.ApplicationData, _ = FetchApplicationData(ctx, profileID)
	}
	return &profile, nil
}

// GetProfileConsents retrieves the consents of a profile by its profileId
func GetProfileConsents(ctx context.Context, profileID, orgID string) ([]model.ConsentRecord, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while fetching profile consents with Id: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.GetProfileConsentsByProfileID

	results, err := dbClient.ExecuteQueryContext(ctx, query, profileID)

	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug(fmt.Sprintf("No profile found with the given Id: %s", profileID))
		// todo: should we return a client error with 404 here?
		return nil, nil
	}
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching consents of profile with Id: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
	}
	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("No profile found with the given Id: %s", profileID))
		var profile, _ = GetProfile(ctx, profileID, orgID)
		if profile != nil {
			// If no consents found and the user exists, return an empty slice instead of nil
			return []model.ConsentRecord{}, nil
		} else {
			return nil, nil
		}
	}
	profileConsents := make([]model.ConsentRecord, 0)
	for _, row := range results {
		profileConsents = append(profileConsents, scanProfileConsentRow(row))
	}
	return profileConsents, nil
}

// FetchApplicationData returns the data of every application stored for the profile.
func FetchApplicationData(ctx context.Context, profileID string) ([]model.ApplicationData, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed getting db client for fetching application data for profile with"+
			" Id: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetAppData.Code,
			Message:     errors2.GetAppData.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer func() { _ = dbClient.Close() }()
	query := scripts.GetAppDataByProfileID
	results, err := dbClient.ExecuteQueryContext(ctx, query, profileID)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching application data for profile with Id: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetAppData.Code,
			Message:     errors2.GetAppData.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}

	apps := make([]model.ApplicationData, 0)
	for _, row := range results {
		var (
			appID     string
			appBlob   []byte
			appParsed model.ApplicationData
		)

		appID = row["app_id"].(string)
		appBlob = row["application_data"].([]byte)

		if err := json.Unmarshal(appBlob, &appParsed); err != nil {
			errorMsg := fmt.Sprintf("Failed to un marshal application data for profile with Id: %s", profileID)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.GetAppData.Code,
				Message:     errors2.GetAppData.Message,
				Description: errorMsg,
			}, err)
			return nil, serverError
		}

		apps = append(apps, model.ApplicationData{
			AppID:           appID,
			AppSpecificData: appParsed.AppSpecificData,
		})
	}
	return apps, nil
}

// FetchApplicationDataWithAppID returns the profile's data for one application.
func FetchApplicationDataWithAppID(ctx context.Context, profileID string, appID string) (model.ApplicationData, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf(
			"Failed getting db client for fetching application data of app:%s for profile: %s",
			appID,
			profileID,
		)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetAppData.Code,
			Message:     errors2.GetAppData.Message,
			Description: errorMsg,
		}, err)
		return model.ApplicationData{}, serverError
	}
	defer func() { _ = dbClient.Close() }()
	query := scripts.GetAppDataByAppID
	results, err := dbClient.ExecuteQueryContext(ctx, query, profileID, appID)
	var app model.ApplicationData
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching application data of app:%s for profile: %s", appID, profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetAppData.Code,
			Message:     errors2.GetAppData.Message,
			Description: errorMsg,
		}, err)
		return app, serverError
	}

	for _, row := range results {
		var (
			appID     string
			appBlob   []byte
			appParsed model.ApplicationData
		)

		appID = row["app_id"].(string)
		appBlob = row["application_data"].([]byte)

		if err := json.Unmarshal(appBlob, &appParsed); err != nil {
			errorMsg := fmt.Sprintf("Failed unmarshalling application data of app:%s for profile: %s", appID, profileID)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.GetAppData.Code,
				Message:     errors2.GetAppData.Message,
				Description: errorMsg,
			}, err)
			return app, serverError
		}

		app.AppID = appID
		app.AppSpecificData = appParsed.AppSpecificData
	}
	return app, nil
}

// UpdateProfile updates the profile
func UpdateProfile(ctx context.Context, orgID string, profile model.Profile) error {
	return updateProfile(ctx, orgID, profile, false)
}

// LinkProfile updates the profile as UpdateProfile does, and fails if a different user has the profile.
func LinkProfile(ctx context.Context, orgID string, profile model.Profile) error {
	return updateProfile(ctx, orgID, profile, true)
}

func updateProfile(ctx context.Context, orgID string, profile model.Profile, link bool) error {
	// An organization that CDS does not know gets an empty key, which matches no row.

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get database client for updating profile: %s", profile.ProfileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	traitsJSON, _ := json.Marshal(profile.Traits)
	identityJSON, _ := json.Marshal(profile.IdentityAttributes)

	var profileStatus string
	if profile.ProfileStatus.IsReferenceProfile {
		profileStatus = constants.ReferenceProfile
	} else if profile.ProfileStatus.IsWaitingOnUser {
		profileStatus = constants.WaitOnUser
	} else if profile.ProfileStatus.IsWaitingOnAdmin {
		profileStatus = constants.WaitOnAdmin
	} else {
		profileStatus = constants.MergedTo
	}

	query := scripts.UpdateProfile
	if link {
		query = scripts.LinkProfile
	}

	_, err = dbClient.ExecuteQueryContext(ctx, query,
		profile.UserID,
		profile.ProfileStatus.ListProfile,
		profile.ProfileStatus.DeleteProfile,
		traitsJSON,
		identityJSON,
		profile.UpdatedAt,
		profile.ProfileID,
		orgID,
	)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed updating the profile: %s", profile.ProfileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	if link {
		// The database client does not return the count of changed rows.
		stored, err := GetProfileWithOptions(ctx, profile.ProfileID, orgID, GetProfileOptions{})
		if err != nil {
			return err
		}
		if stored == nil || stored.UserID != profile.UserID {
			logger.Debug(fmt.Sprintf("Profile: %s is already linked to a different user.", profile.ProfileID))
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.ProfileAlreadyLinked.Code,
				Message:     errors2.ProfileAlreadyLinked.Message,
				Description: errors2.ProfileAlreadyLinked.Description,
			}, http.StatusConflict)
		}
	}

	query = scripts.UpsertProfileReference

	_, err = dbClient.ExecuteQueryContext(ctx, query,
		profile.ProfileID,
		profileStatus,
		profile.ProfileStatus.ReferenceProfileID,
		profile.ProfileStatus.ReferenceReason,
		profile.ProfileID,
	)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed updating the profile: %s", profile.ProfileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	// Update application data
	err = InsertApplicationData(ctx, profile.ProfileID, profile.ApplicationData)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to insert profile with Id: %s", profile.ProfileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.AddProfile.Code,
			Message:     errors2.AddProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	return nil
}

// GetAllProfiles retrieves profiles using cursor-based pagination.
// It returns up to `limit` profiles and a boolean indicating if more records exist.
func GetAllProfiles(ctx context.Context,
	orgID string, limit int, cursor *model.ProfileCursor) ([]model.Profile, bool, error) {
	// An organization that CDS does not know gets an empty key, which matches no row.

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := "Failed to get database client for fetching all profiles"
		logger.Debug(errorMsg, log.Error(err))
		return nil, false, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	// Defaults/safety
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	query := scripts.GetProfilesByOrgID

	var cursorTime interface{} = nil
	var cursorProfileID string = ""
	direction := model.CursorNext

	if cursor != nil {
		cursorTime = cursor.CreatedAt
		cursorProfileID = cursor.ProfileID
		if strings.TrimSpace(cursor.Direction) != "" {
			direction = strings.TrimSpace(cursor.Direction)
		}
		if direction != model.CursorNext && direction != model.CursorPrev {
			direction = model.CursorNext
		}
	}

	// lookahead
	limitPlusOne := limit + 1

	results, err := dbClient.ExecuteQueryContext(
		ctx,
		query,
		orgID,
		cursorTime,
		cursorProfileID,
		direction,
		limitPlusOne,
	)
	if err != nil {
		errorMsg := "Failed fetching all profiles"
		logger.Debug(errorMsg, log.Error(err))
		return nil, false, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
	}

	hasMore := false
	if len(results) > limit {
		hasMore = true
		results = results[:limit]
	}

	profiles := make([]model.Profile, 0, len(results))
	profileIDs := make([]string, 0, len(results))
	for _, row := range results {
		profile, err := scanProfileRow(row)
		if err != nil {
			return nil, false, err
		}
		profiles = append(profiles, profile)
		profileIDs = append(profileIDs, profile.ProfileID)
	}

	appDataMap, err := FetchApplicationDataBatch(ctx, profileIDs)
	if err != nil {
		errorMsg := "Failed fetching application data for profiles."
		logger.Debug(errorMsg, log.Error(err))
		return nil, false, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
	}

	for i := range profiles {
		profiles[i].ApplicationData = appDataMap[profiles[i].ProfileID]
	}

	// If direction=prev, SQL returned ASC to get “closest newer”.
	// Reverse so API always returns DESC order.
	if direction == model.CursorPrev {
		for i, j := 0, len(profiles)-1; i < j; i, j = i+1, j-1 {
			profiles[i], profiles[j] = profiles[j], profiles[i]
		}
	}

	return profiles, hasMore, nil
}

// FetchApplicationDataBatch returns the application data of several profiles in one query, keyed by profile ID.
func FetchApplicationDataBatch(ctx context.Context, profileIDs []string) (map[string][]model.ApplicationData, error) {
	result := make(map[string][]model.ApplicationData)
	if len(profileIDs) == 0 {
		return result, nil
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := "Failed getting db client for fetching application data batch."
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetAppData.Code,
			Message:     errors2.GetAppData.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	// Build placeholders: $1,$2,... for Postgres
	placeholders := make([]string, 0, len(profileIDs))
	args := make([]interface{}, 0, len(profileIDs))
	for i, id := range profileIDs {
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
		args = append(args, id)
	}

	query := scripts.GetAppDataByProfileIDs.Format(strings.Join(placeholders, ","))

	rows, err := dbClient.ExecuteQueryContext(ctx, query, args...)
	if err != nil {
		errorMsg := "Failed fetching application data batch."
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetAppData.Code,
			Message:     errors2.GetAppData.Message,
			Description: errorMsg,
		}, err)
	}

	for _, row := range rows {
		pid := row[fieldProfileID].(string)
		appID := row["app_id"].(string)
		appBlob := row["application_data"].([]byte)

		var parsed model.ApplicationData
		if err := json.Unmarshal(appBlob, &parsed); err != nil {
			errorMsg := fmt.Sprintf("Failed to unmarshal application data for profile: %s", pid)
			logger.Debug(errorMsg, log.Error(err))
			return nil, errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.GetAppData.Code,
				Message:     errors2.GetAppData.Message,
				Description: errorMsg,
			}, err)
		}

		result[pid] = append(result[pid], model.ApplicationData{
			AppID:           appID,
			AppSpecificData: parsed.AppSpecificData,
		})
	}

	// ensure empty slice for profiles with no app rows
	for _, pid := range profileIDs {
		if _, ok := result[pid]; !ok {
			result[pid] = []model.ApplicationData{}
		}
	}

	return result, nil
}

// DeleteProfile deletes a profile and its associated data
func DeleteProfile(ctx context.Context, profileID string) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed getting db client for deleting the profile: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DeleteProfile.Code,
			Message:     errors2.DeleteProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	// Step 1: Delete application_data explicitly (optional if ON DELETE CASCADE not enabled)
	_, err = dbClient.ExecuteQueryContext(ctx, scripts.DeleteProfileByProfileID, profileID)
	if err != nil {
		errorMsg := fmt.Sprintf("failed to delete application data for profile: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DeleteProfile.Code,
			Message:     errors2.DeleteProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	// Step 2: Delete child relationships where this is a parent (optional safety, ON DELETE CASCADE already exists)
	// :todo: Need to decide if its needed
	// _, err = dbClient.ExecuteQueryContext(ctx,
	//	`DELETE FROM profiles WHERE reference_profile_id = $1`, profileId)
	// if err != nil {
	//	errorMsg := fmt.Sprintf("failed to delete child profile links for profile: %s", profileId)
	//	logger.Debug(errorMsg, log.Error(err))
	//	serverError := errors2.NewServerError(errors2.ErrorMessage{
	//		Code:        errors2.DeleteProfile.Code,
	//		Message:     errors2.DeleteProfile.Message,
	//		Description: errorMsg,
	//	}, err)
	//	return serverError
	// }

	// Step 3: Delete the profile itself
	result, err := dbClient.ExecuteQueryContext(ctx, scripts.DeleteProfile, profileID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			logger.Debug(fmt.Sprintf("No profile found with the given Id: %s", profileID))
			return nil
		}
		errorMsg := fmt.Sprintf("failed to delete  profile: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DeleteProfile.Code,
			Message:     errors2.DeleteProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	rows := len(result)
	if rows == 0 {
		logger.Debug(
			fmt.Sprintf("No profile found with the given Id: %s. Probably would have been deleted already", profileID),
		)
	}

	logger.Info(fmt.Sprintf("Profile: %s and associated data deleted successfully", profileID))
	return nil
}

// UpsertAppDatum merges the updates into the profile's data for one application and saves it. List values
// are appended without duplicates and scalar values are overwritten.
func UpsertAppDatum(ctx context.Context, profileID string, appID string, updates map[string]interface{}) error {
	// Fetch existing application_data for the given app
	appData, err := FetchApplicationDataWithAppID(ctx, profileID, appID)
	if err != nil {
		return err
	}

	if appData.AppSpecificData == nil {
		appData.AppSpecificData = make(map[string]interface{})
	}

	logger := log.GetLogger()
	// Separate handling for "devices" key (top-level)
	for key, incomingVal := range updates {
		actualKey := strings.TrimPrefix(key, "application_data.")
		// Merge into app_specific_data
		existingVal := appData.AppSpecificData[actualKey]
		log.GetLogger().Info("Merging key: " + actualKey)
		appData.AppSpecificData[actualKey] = enrichFieldValues(existingVal, incomingVal)
	}

	// Final wrapper for marshaling
	type ApplicationDataJSON struct {
		AppSpecificData map[string]interface{} `json:"app_specific_data,omitempty"`
	}

	wrapper := ApplicationDataJSON{
		AppSpecificData: appData.AppSpecificData,
	}

	jsonBytes, err := json.Marshal(wrapper)
	errorMsg := fmt.Sprintf("Failed to marshal devices for application data for profile: %s", profileID)
	if err != nil {
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAppData.Code,
			Message:     errors2.UpdateAppData.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	// Upsert into application_data table
	query := scripts.InsertApplicationData

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		errorMsg := fmt.Sprintf(
			"Failed to get database client for upserting application data for profile: %s",
			profileID,
		)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAppData.Code,
			Message:     errors2.UpdateAppData.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	_, err = dbClient.ExecuteQueryContext(ctx, query, profileID, appID, jsonBytes)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to upsert application data for profile: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateAppData.Code,
			Message:     errors2.UpdateAppData.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	return nil
}

// DetachRefererProfileFromReference removes a child from a parent's child_profile_ids list
func DetachRefererProfileFromReference(ctx context.Context, referenceProfileID, profileID string) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf(
			"Failed to get database client for deleting child relationship of child: %s of parent: %s",
			referenceProfileID,
			profileID,
		)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DeleteProfile.Code,
			Message:     errors2.DeleteProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	// todo: decide if we need to delete the references as well.
	query := scripts.DeleteProfileReference
	result, err := dbClient.ExecuteQueryContext(ctx, query, referenceProfileID, profileID)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to delete child relationship of child: %s of parent: %s",
			referenceProfileID, profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DeleteProfile.Code,
			Message:     errors2.DeleteProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	rowsAffected := len(result)
	if rowsAffected == 0 {
		logger.Info(fmt.Sprintf("No child profile %s found under parent %s to remove.", profileID, referenceProfileID))
	}
	return nil
}

// InsertMergedMasterProfileAppData adds or updates application-specific context data.
func InsertMergedMasterProfileAppData(
	ctx context.Context,
	profileID, orgID string,
	newAppCtx model.ApplicationData,
) error {
	profile, err := GetProfile(ctx, profileID, orgID)
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch profile %s for app data update.", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	if profile == nil {
		errorMsg := fmt.Sprintf("Profile: %s not found for app data update", profileID)
		logger.Debug(errorMsg, log.Error(err))
		clientError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errorMsg,
		}, err)
		return clientError
	}

	updated := false
	var resultAppData []model.ApplicationData

	for _, existing := range profile.ApplicationData {
		if existing.AppID == newAppCtx.AppID {
			// Merge app-specific fields
			if existing.AppSpecificData == nil {
				existing.AppSpecificData = make(map[string]interface{})
			}
			for k, v := range newAppCtx.AppSpecificData {
				existing.AppSpecificData[k] = v
			}
			resultAppData = append(resultAppData, existing)
			updated = true
		} else {
			resultAppData = append(resultAppData, existing)
		}
	}

	if !updated {
		resultAppData = append(resultAppData, newAppCtx)
	}

	profile.ApplicationData = resultAppData
	// this inserts the entire application_data blob with the update.
	return InsertApplicationData(ctx, profile.ProfileID, profile.ApplicationData)
}

// InsertMergedMasterProfileTraitData replaces (PUT) the traits data inside Profile
func InsertMergedMasterProfileTraitData(ctx context.Context,
	profileID, orgID string, traitsData map[string]interface{}) error {
	profile, err := GetProfile(ctx, profileID, orgID)
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch profile %s for trait data update.", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	if profile == nil {
		errorMsg := fmt.Sprintf("Profile: %s not found for trait data update", profileID)
		logger.Debug(errorMsg, log.Error(err))
		clientError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errorMsg,
		}, err)
		return clientError
	}

	profile.Traits = traitsData
	return UpdateProfile(ctx, orgID, *profile) // Update existing profile
}

// MergeIdentityDataOfProfiles replaces or adds to identity_attributes in Profile
func MergeIdentityDataOfProfiles(
	ctx context.Context,
	profileID, orgID string,
	identityData map[string]interface{},
) error {
	profile, err := GetProfile(ctx, profileID, orgID)
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch profile %s for identity data update.", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	if profile == nil {
		errorMsg := fmt.Sprintf("Profile: %s not found for identity data update", profileID)
		logger.Debug(errorMsg, log.Error(err))
		clientError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errorMsg,
		}, err)
		return clientError
	}

	if profile.IdentityAttributes == nil {
		profile.IdentityAttributes = make(map[string]interface{})
	}
	for k, v := range identityData {
		profile.IdentityAttributes[k] = v // Overwrites or adds
	}

	return UpdateProfile(ctx, orgID, *profile)
}

// GetAllProfilesWithFilter retrieves profiles using dynamic filters and cursor-based pagination.
func GetAllProfilesWithFilter(ctx context.Context,
	orgID string,
	filters []string,
	limit int,
	cursor *model.ProfileCursor,
) ([]model.Profile, bool, error) {
	if err := validateFilterFields(filters); err != nil {
		return nil, false, err
	}

	// An organization that CDS does not know gets an empty key, which matches no row.

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := "Failed to get database client filtering profiles."
		logger.Debug(errorMsg, log.Error(err))
		return nil, false, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FilterProfile.Code,
			Message:     errors2.FilterProfile.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	if limit == 0 {
		return []model.Profile{}, false, nil
	}
	if limit < 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	// The dialect is needed here because the bind arguments differ per
	// datasource, not to select a statement.
	dbType := dbClient.DBType()
	baseSQL := scripts.GetAllProfilesWithFilterBase.GetQuery(dbType)
	likeOperator, err := scripts.LikeOperator(dbType)
	if err != nil {
		return nil, false, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FilterProfile.Code,
			Message:     errors2.FilterProfile.Message,
			Description: fmt.Sprintf("Failed to prepare the filtered query: %s", err),
		}, err)
	}

	var conditions []string
	var args []interface{}
	argID := 1
	joinedAppIDs := map[string]bool{}

	conditions = append(conditions, fmt.Sprintf("p.org_id = $%d", argID))
	args = append(args, orgID)
	argID++

	// dynamic filter conditions
	for _, f := range filters {
		parts := strings.SplitN(f, " ", 3)
		if len(parts) != 3 {
			errorMsg := fmt.Sprintf("Invalid filter format: %s", f)
			logger.Debug(errorMsg)
			return nil, false, errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.FilterProfile.Code,
				Message:     errors2.FilterProfile.Message,
				Description: errorMsg,
			}, err)
		}
		field, operator, value := parts[0], parts[1], parts[2]

		var scope, key string
		if field == fieldUserID || field == fieldProfileID {
			scope = field
			key = ""
		} else {
			scopeKey := strings.SplitN(field, ".", 2)
			if len(scopeKey) != 2 {
				continue
			}
			scope, key = scopeKey[0], scopeKey[1]
		}

		switch scope {
		case "identity_attributes", "traits":
			jsonCol := "p." + scope
			switch operator {
			case "eq":
				condition, arg, err := scripts.JSONEqCondition(dbType, jsonCol, []string{key}, value, argID)
				if err != nil {
					return nil, false, errors2.NewServerError(errors2.ErrorMessage{
						Code:        errors2.FilterProfile.Code,
						Message:     errors2.FilterProfile.Message,
						Description: fmt.Sprintf("Invalid filter value for key: %s", key),
					}, err)
				}
				conditions = append(conditions, condition)
				args = append(args, arg)
			case "co", "sw":
				condition, err := scripts.JSONLikeCondition(dbType, jsonCol, []string{key}, argID)
				if err != nil {
					return nil, false, errors2.NewServerError(errors2.ErrorMessage{
						Code:        errors2.FilterProfile.Code,
						Message:     errors2.FilterProfile.Message,
						Description: fmt.Sprintf("Invalid filter key: %s", key),
					}, err)
				}
				conditions = append(conditions, condition)
				if operator == "co" {
					args = append(args, "%"+value+"%")
				} else {
					args = append(args, value+"%")
				}
			default:
				continue
			}
			argID++

		case fieldUserID:
			switch operator {
			case "eq":
				conditions = append(conditions, fmt.Sprintf("p.user_id = $%d", argID))
				args = append(args, value)
			case "co":
				conditions = append(conditions, fmt.Sprintf("p.user_id %s $%d", likeOperator, argID))
				args = append(args, "%"+value+"%")
			case "sw":
				conditions = append(conditions, fmt.Sprintf("p.user_id %s $%d", likeOperator, argID))
				args = append(args, value+"%")
			default:
				continue
			}
			argID++

		case fieldProfileID:
			switch operator {
			case "eq":
				conditions = append(conditions, fmt.Sprintf("p.profile_id = $%d", argID))
				args = append(args, value)
			case "co":
				conditions = append(conditions, fmt.Sprintf("p.profile_id %s $%d", likeOperator, argID))
				args = append(args, "%"+value+"%")
			case "sw":
				conditions = append(conditions, fmt.Sprintf("p.profile_id %s $%d", likeOperator, argID))
				args = append(args, value+"%")
			default:
				continue
			}
			argID++

		case "application_data":
			var appAlias, appKey string

			if strings.Contains(key, ".") {
				appScope := strings.SplitN(key, ".", 2)
				appID := appScope[0]
				appKey = appScope[1]
				appAlias = "a_" + sanitizeForAlias(appID)

				if !joinedAppIDs[appID] {
					baseSQL += fmt.Sprintf(`
                INNER JOIN application_data %s
                  ON %s.profile_id = p.profile_id AND %s.app_id = $%d`, appAlias, appAlias, appAlias, argID)
					args = append(args, appID)
					argID++
					joinedAppIDs[appID] = true
				}
			} else {
				appKey = key
				appAlias = "a"
				if !joinedAppIDs["__generic"] {
					baseSQL += `
                        INNER JOIN application_data a ON a.profile_id = p.profile_id
                    `
					joinedAppIDs["__generic"] = true
				}
			}

			appDataCol := appAlias + ".application_data"
			appDataPath := []string{"app_specific_data", appKey}

			switch operator {
			case "eq":
				condition, arg, err := scripts.JSONEqCondition(dbType, appDataCol, appDataPath, value, argID)
				if err != nil {
					return nil, false, errors2.NewServerError(errors2.ErrorMessage{
						Code:        errors2.FilterProfile.Code,
						Message:     errors2.FilterProfile.Message,
						Description: fmt.Sprintf("Invalid filter value for key: %s", appKey),
					}, err)
				}
				conditions = append(conditions, condition)
				args = append(args, arg)
				argID++
			case "co", "sw":
				condition, err := scripts.JSONLikeCondition(dbType, appDataCol, appDataPath, argID)
				if err != nil {
					return nil, false, errors2.NewServerError(errors2.ErrorMessage{
						Code:        errors2.FilterProfile.Code,
						Message:     errors2.FilterProfile.Message,
						Description: fmt.Sprintf("Invalid filter key: %s", appKey),
					}, err)
				}
				conditions = append(conditions, condition)
				if operator == "co" {
					args = append(args, "%"+value+"%")
				} else {
					args = append(args, value+"%")
				}
				argID++
			default:
				continue
			}
		}
	}

	// cursor seek (created_at + profile_id)
	var cursorTime interface{} = nil
	var cursorProfileID string = ""
	direction := model.CursorNext
	if cursor != nil {
		cursorTime = cursor.CreatedAt
		cursorProfileID = cursor.ProfileID
		if strings.TrimSpace(cursor.Direction) != "" {
			direction = strings.TrimSpace(cursor.Direction)
		}
		if direction != model.CursorNext && direction != model.CursorPrev {
			direction = model.CursorNext
		}
	}

	// Add cursor predicates as parameterized conditions
	// Params: $argID = cursorTime, $(argID+1)=cursorProfileId
	if cursor != nil {
		operator := "<"
		if direction != model.CursorNext {
			operator = ">"
		}
		keysetCondition, err := scripts.KeysetCondition(dbType, operator, argID, argID+1)
		if err != nil {
			return nil, false, errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.FilterProfile.Code,
				Message:     errors2.FilterProfile.Message,
				Description: fmt.Sprintf("Failed to prepare the cursor condition: %s", err),
			}, err)
		}
		conditions = append(conditions, keysetCondition)
		args = append(args, cursorTime, cursorProfileID)
		argID += 2
	}

	conditions = append(conditions, "r.profile_status = 'REFERENCE_PROFILE'")

	whereClause := "WHERE " + strings.Join(conditions, " AND ")

	// Direction-specific ORDER
	orderClause := "ORDER BY p.created_at DESC, p.profile_id DESC"
	if direction == model.CursorPrev {
		// fetch "newer" rows closest to cursor
		orderClause = "ORDER BY p.created_at ASC, p.profile_id ASC"
	}

	rowLimit, err := scripts.RowLimit(dbType, argID)
	if err != nil {
		return nil, false, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FilterProfile.Code,
			Message:     errors2.FilterProfile.Message,
			Description: fmt.Sprintf("Failed to prepare the row limit: %s", err),
		}, err)
	}

	limitPlusOne := limit + 1
	finalSQL := fmt.Sprintf("%s\n%s\n%s\n%s", baseSQL, whereClause, orderClause, rowLimit)
	args = append(args, limitPlusOne)

	results, err := dbClient.ExecuteQueryContext(ctx, scripts.GetAllProfilesWithFilterBase.WithSQL(finalSQL), args...)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to execute filtered query: %s", err)
		logger.Debug(errorMsg, log.Error(err))
		return nil, false, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FilterProfile.Code,
			Message:     errors2.FilterProfile.Message,
			Description: errorMsg,
		}, err)
	}

	hasMore := false
	if len(results) > limit {
		hasMore = true
		results = results[:limit]
	}

	profiles := make([]model.Profile, 0, len(results))
	profileIDs := make([]string, 0, len(results))
	for _, row := range results {
		profile, err := scanProfileRow(row)
		if err != nil {
			return nil, false, err
		}
		profiles = append(profiles, profile)
		profileIDs = append(profileIDs, profile.ProfileID)
	}

	appDataMap, err := FetchApplicationDataBatch(ctx, profileIDs)
	if err != nil {
		errorMsg := "Failed fetching application data for profiles."
		logger.Debug(errorMsg, log.Error(err))
		return nil, false, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
	}

	for i := range profiles {
		profiles[i].ApplicationData = appDataMap[profiles[i].ProfileID]
	}

	// reverse back to DESC order for API
	if direction == model.CursorPrev {
		for i, j := 0, len(profiles)-1; i < j; i, j = i+1, j-1 {
			profiles[i], profiles[j] = profiles[j], profiles[i]
		}
	}

	return profiles, hasMore, nil
}

// GetProfileIDsWithFilters returns distinct profile IDs that match the given deterministic filters.
// Used by the hybrid fuzzy+deterministic search to narrow the fuzzy candidate set.
// GetProfileIDsWithFilters returns the profiles in an org matching the given filters,
// restricted to restrictTo when it is non-empty.
//
// The restriction exists so hybrid search can intersect in the database rather than in Go.
// Fetching every matching id and intersecting in memory made a loose filter a full-tenant
// scan whose result was then mostly discarded; the fuzzy candidate set is already capped,
// so pushing it down bounds both the query and the memory it returns.
func GetProfileIDsWithFilters(ctx context.Context, orgID string, filters []string,
	restrictTo []string) ([]string, error) {
	if err := validateFilterFields(filters); err != nil {
		return nil, err
	}

	// An organization that CDS does not know gets an empty key, which matches no row.

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := "Failed to get database client for GetProfileIDsWithFilters."
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FilterProfile.Code,
			Message:     errors2.FilterProfile.Message,
			Description: errorMsg,
		}, err)
	}
	defer func() { _ = dbClient.Close() }()

	finalSQL, args, err := buildProfileIDsQuery(dbClient.DBType(), orgID, filters, restrictTo)
	if err != nil {
		return nil, err
	}

	results, err := dbClient.ExecuteQueryContext(ctx, scripts.GetProfileIDsWithFiltersBase.WithSQL(finalSQL), args...)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to execute GetProfileIDsWithFilters query: %s", err)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FilterProfile.Code,
			Message:     errors2.FilterProfile.Message,
			Description: errorMsg,
		}, err)
	}

	ids := make([]string, 0, len(results))
	for _, row := range results {
		if id, ok := row["profile_id"].(string); ok {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func buildProfileIDsQuery(dbType, orgID string, filters, restrictTo []string) (string, []interface{}, error) {
	filterError := func(description string, cause error) error {
		return errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FilterProfile.Code,
			Message:     errors2.FilterProfile.Message,
			Description: description,
		}, cause)
	}

	baseSQL := scripts.GetProfileIDsWithFiltersBase.GetQuery(dbType)
	likeOperator, err := scripts.LikeOperator(dbType)
	if err != nil {
		return "", nil, filterError(fmt.Sprintf("Failed to prepare the filter query: %s", err), err)
	}

	var conditions []string
	var args []interface{}
	argID := 1
	joinedAppIDs := map[string]bool{}

	conditions = append(conditions, fmt.Sprintf("p.org_id = $%d", argID))
	args = append(args, orgID)
	argID++

	for _, f := range filters {
		parts := strings.SplitN(f, " ", 3)
		if len(parts) != 3 {
			continue
		}
		field, operator, value := parts[0], parts[1], parts[2]

		var scope, key string
		if field == fieldUserID || field == fieldProfileID {
			scope = field
			key = ""
		} else {
			scopeKey := strings.SplitN(field, ".", 2)
			if len(scopeKey) != 2 {
				continue
			}
			scope, key = scopeKey[0], scopeKey[1]
		}

		switch scope {
		case "identity_attributes", "traits":
			jsonCol := "p." + scope
			switch operator {
			case "eq":
				condition, arg, err := scripts.JSONEqCondition(dbType, jsonCol, []string{key}, value, argID)
				if err != nil {
					return "", nil, filterError(fmt.Sprintf("Invalid filter value for key: %s", key), err)
				}
				conditions = append(conditions, condition)
				args = append(args, arg)
			case "co", "sw", "ew":
				condition, err := scripts.JSONLikeCondition(dbType, jsonCol, []string{key}, argID)
				if err != nil {
					return "", nil, filterError(fmt.Sprintf("Invalid filter key: %s", key), err)
				}
				conditions = append(conditions, condition)
				args = append(args, likePattern(operator, value))
			default:
				continue
			}
			argID++

		case fieldUserID, fieldProfileID:
			column := "p." + scope
			switch operator {
			case "eq":
				conditions = append(conditions, fmt.Sprintf("%s = $%d", column, argID))
				args = append(args, value)
			case "co", "sw", "ew":
				conditions = append(conditions, fmt.Sprintf("%s %s $%d", column, likeOperator, argID))
				args = append(args, likePattern(operator, value))
			default:
				continue
			}
			argID++

		case "application_data":
			var appAlias, appKey string

			if strings.Contains(key, ".") {
				appScope := strings.SplitN(key, ".", 2)
				appID := appScope[0]
				appKey = appScope[1]
				appAlias = "a_" + sanitizeForAlias(appID)

				if !joinedAppIDs[appID] {
					baseSQL += fmt.Sprintf(`
                INNER JOIN application_data %s
                  ON %s.profile_id = p.profile_id AND %s.app_id = $%d`, appAlias, appAlias, appAlias, argID)
					args = append(args, appID)
					argID++
					joinedAppIDs[appID] = true
				}
			} else {
				appKey = key
				appAlias = "a"
				if !joinedAppIDs["__generic"] {
					baseSQL += ` INNER JOIN application_data a ON a.profile_id = p.profile_id`
					joinedAppIDs["__generic"] = true
				}
			}

			appDataCol := appAlias + ".application_data"
			appDataPath := []string{"app_specific_data", appKey}

			switch operator {
			case "eq":
				condition, arg, err := scripts.JSONEqCondition(dbType, appDataCol, appDataPath, value, argID)
				if err != nil {
					return "", nil, filterError(fmt.Sprintf("Invalid filter value for key: %s", appKey), err)
				}
				conditions = append(conditions, condition)
				args = append(args, arg)
				argID++
			case "co", "sw", "ew":
				condition, err := scripts.JSONLikeCondition(dbType, appDataCol, appDataPath, argID)
				if err != nil {
					return "", nil, filterError(fmt.Sprintf("Invalid filter key: %s", appKey), err)
				}
				conditions = append(conditions, condition)
				args = append(args, likePattern(operator, value))
				argID++
			default:
				continue
			}
		}
	}

	conditions = append(conditions, "r.profile_status = 'REFERENCE_PROFILE'")

	if len(restrictTo) > 0 {
		placeholders := make([]string, 0, len(restrictTo))
		for _, id := range restrictTo {
			placeholders = append(placeholders, fmt.Sprintf("$%d", argID))
			args = append(args, id)
			argID++
		}
		conditions = append(conditions, "p.profile_id IN ("+strings.Join(placeholders, ", ")+")")
	}

	whereClause := "WHERE " + strings.Join(conditions, " AND ")
	finalSQL := fmt.Sprintf("%s\n%s", baseSQL, whereClause)

	return finalSQL, args, nil
}

// likePattern builds the pattern a "contains", "starts with" or "ends with" filter matches
// against. The operator must be one of co, sw and ew.
func likePattern(operator, value string) string {
	switch operator {
	case "sw":
		return value + "%"
	case "ew":
		return "%" + value
	default:
		return "%" + value + "%"
	}
}

// sanitizeForAlias converts a string to a valid SQL alias by replacing special characters
// filterField matches the filter fields that may enter the SQL text. The filter builders put the
// attribute key of a field into a string literal (->> '<key>'), so a field must not hold a quote.
var filterField = regexp.MustCompile(constants.FilterRegex)

// validateFilterFields rejects a filter whose field is not safe to put into the SQL text. The
// service checks the fields too; this check keeps the store safe for a caller that does not. A
// filter that is not "<field> <operator> <value>" is left to the builder, which handles it.
func validateFilterFields(filters []string) error {
	for _, f := range filters {
		parts := strings.SplitN(f, " ", 3)
		if len(parts) != 3 {
			continue
		}
		field := parts[0]
		if field == fieldUserID || field == fieldProfileID || filterField.MatchString(field) {
			continue
		}
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.FilterProfile.Code,
			Message:     errors2.FilterProfile.Message,
			Description: "Invalid filter key: " + field,
		}, http.StatusBadRequest)
	}
	return nil
}

func sanitizeForAlias(input string) string {
	// Replace any non-alphanumeric character with underscore
	reg := regexp.MustCompile(`[^a-zA-Z0-9_]`)
	sanitized := reg.ReplaceAllString(input, "_")

	// Ensure it doesn't start with a number (SQL requirement)
	if len(sanitized) > 0 && sanitized[0] >= '0' && sanitized[0] <= '9' {
		sanitized = "_" + sanitized
	}

	return sanitized
}

// UpdateProfileReferences updates the references of a parent profile with the provided child profiles.
func UpdateProfileReferences(ctx context.Context, parentProfile model.Profile, children []model.Reference) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get database client for adding child profiles for parent: %s",
			parentProfile.ProfileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to begin transaction for adding child profiles for parent: %s",
			parentProfile.ProfileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	query := scripts.UpdateProfileReference

	for _, child := range children {
		_, err := tx.ExecContext(ctx, query, parentProfile.ProfileID, child.Reason, constants.MergedTo, child.ProfileID)
		if err != nil {
			errRoll := tx.Rollback()
			if errRoll != nil {
				errorMsg := fmt.Sprintf("Failed to rollback transaction after error: %s", err)
				logger.Debug(errorMsg, log.Error(err))
				serverError := errors2.NewServerError(errors2.ErrorMessage{
					Code:        errors2.UpdateProfile.Code,
					Message:     errors2.UpdateProfile.Message,
					Description: errorMsg,
				}, errRoll)
				return serverError
			}
			errorMsg := fmt.Sprintf(
				"Failed to insert referenced profile: %s for parent profile: %s",
				child.ProfileID,
				parentProfile.ProfileID,
			)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.UpdateProfile.Code,
				Message:     errors2.UpdateProfile.Message,
				Description: errorMsg,
			}, err)
			return serverError
		}
	}

	return tx.Commit()
}

// FetchReferencedProfiles returns the profiles that point at the given reference profile, with the reason
// for each link.
func FetchReferencedProfiles(ctx context.Context, referenceProfileID string) ([]model.Reference, error) {
	logger := log.GetLogger()
	logger.Info(fmt.Sprintf("Fetching referenced profiles for profile: %s", referenceProfileID))

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get database client for fetching child profiles for parent: %s",
			referenceProfileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer func() { _ = dbClient.Close() }()
	query := scripts.FetchReferencedProfiles

	results, err := dbClient.ExecuteQueryContext(ctx, query, referenceProfileID)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching referenced profiles for profile: %s", referenceProfileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}

	var children []model.Reference
	for _, row := range results {
		var reference model.Reference
		reference.ProfileID = row[fieldProfileID].(string)
		reference.Reason = row["reference_reason"].(string)
		children = append(children, reference)
	}

	if len(children) == 0 {
		logger.Info(fmt.Sprintf("No referenced profiles found for parent profile: %s", referenceProfileID))
	} else {
		logger.Info(fmt.Sprintf("Successfully fetched child profiles for parent profile: %s", referenceProfileID))
	}
	return children, nil
}

func enrichFieldValues(existingVal, incomingVal interface{}) interface{} {
	logger := log.GetLogger()
	switch incoming := incomingVal.(type) {
	case []string:
		existing := toStringSlice(existingVal)
		for _, item := range incoming {
			if !containsString(existing, item) {
				existing = append(existing, item)
			}
		}
		return existing

	case []int:
		existing := toIntSlice(existingVal)
		for _, item := range incoming {
			if !containsInt(existing, item) {
				existing = append(existing, item)
			}
		}
		return existing

	case string, int, bool:
		return incoming // overwrite simple types

	default:
		logger.Warn(fmt.Sprintf("EnrichFieldValues encountered unhandled type for incomingVal: %T", incomingVal))
		logger.Warn(fmt.Sprintf("EnrichFieldValues encountered unhandled type for existing: %T", existingVal))
		return incoming
	}
}

func toStringSlice(val interface{}) []string {
	logger := log.GetLogger()
	if val == nil {
		return []string{}
	}
	switch v := val.(type) {
	case []string:
		return v
	case []interface{}:
		var result []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			} else {
				logger.Warn(fmt.Sprintf("WARN: toStringSlice: item in []interface{} is not a string: %T", item))
			}
		}
		return result
	default:
		logger.Warn(fmt.Sprintf("WARN: toStringSlice: value is not []string or []interface{}: %T", val))
		return []string{}
	}
}

func toIntSlice(val interface{}) []int {
	logger := log.GetLogger()
	if val == nil {
		return []int{}
	}
	switch v := val.(type) {
	case []int:
		return v
	case []interface{}:
		var result []int
		for _, item := range v {
			if i, ok := toInt(item); ok {
				result = append(result, i)
			} else {
				logger.Warn(fmt.Sprintf("toIntSlice: item in []interface{} cannot be converted to int: %T", item))
			}
		}
		return result
	default:
		logger.Info(fmt.Sprintf("WARN: toIntSlice: value is not []int or []interface{}: %T", val))
		return []int{}
	}
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func containsInt(slice []int, i int) bool {
	for _, item := range slice {
		if item == i {
			return true
		}
	}
	return false
}

func toInt(val interface{}) (int, bool) {
	switch v := val.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		// Be cautious about potential overflow if int64 > max int
		return int(v), true
	case float32:
		return int(v), true // Potential loss of precision
	case float64:
		return int(v), true // Potential loss of precision
	case json.Number:
		if i64, err := v.Int64(); err == nil {
			return int(i64), true // Potential overflow
		}
		if f64, err := v.Float64(); err == nil {
			return int(f64), true // Potential loss of precision
		}
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i, true
		}
	}
	return 0, false
}

// GetProfileWithUserID returns the organization's profile for the user, with its application data, or nil
// if there is none.
func GetProfileWithUserID(ctx context.Context, userID, orgID string) (*model.Profile, error) {
	// An organization that CDS does not know gets an empty key, which matches no row.

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while fetching profile with userId: %s", userID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.GetProfileByUserID

	results, err := dbClient.ExecuteQueryContext(ctx, query, userID, orgID)

	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug(fmt.Sprintf("No profile found with the given userId: %s", userID))
		// todo: should we return a client error with 404 here?
		return nil, nil
	}
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching profile with user Id: %s", userID)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
	}
	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("No profile found with the given userId: %s", userID))
		// todo: should we return a client error with 404 here?
		return nil, nil
	}
	profile, err := scanProfileRow(results[0])
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching profile with Id: %s", userID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfile.Code,
			Message:     errors2.GetProfile.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	profile.ApplicationData, _ = FetchApplicationData(ctx, profile.ProfileID)
	return &profile, nil
}

// CreateProfileCookie creates a new profile cookie
func CreateProfileCookie(ctx context.Context, profileCookie model.ProfileCookie) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf(
			"Failed to get db client while creating profile cookie with profileId: %s",
			profileCookie.ProfileID,
		)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.CreateProfileCookie.Code,
			Message:     errors2.CreateProfileCookie.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.InsertCookie

	_, err = dbClient.ExecuteQueryContext(
		ctx,
		query,
		profileCookie.CookieID,
		profileCookie.ProfileID,
		profileCookie.IsActive,
	)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed creating the profile cookie with Id: %s", profileCookie.CookieID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.CreateProfileCookie.Code,
			Message:     errors2.CreateProfileCookie.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	return nil
}

// GetProfileCookieByProfileID retrieves a profile cookie by profileId
func GetProfileCookieByProfileID(ctx context.Context, profileID string) (*model.ProfileCookie, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while fetching profile cookie with profileId: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfileCookie.Code,
			Message:     errors2.GetProfileCookie.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.GetCookieByProfileID

	results, err := dbClient.ExecuteQueryContext(ctx, query, profileID)

	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug(fmt.Sprintf("No profile cookie found with the given profileId: %s", profileID))
		return nil, nil
	}
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching cookie of profile with Id: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfileCookie.Code,
			Message:     errors2.GetProfileCookie.Message,
			Description: errorMsg,
		}, err)
	}
	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("No profile cookie found with the given profileId: %s", profileID))
		return nil, nil
	}

	profileCookie := &model.ProfileCookie{
		CookieID:  results[0]["cookie_id"].(string),
		ProfileID: results[0][fieldProfileID].(string),
		IsActive:  results[0]["is_active"].(bool),
	}
	return profileCookie, nil
}

// GetProfileCookie retrieves a profile cookie by profileId
func GetProfileCookie(ctx context.Context, cookie, orgID string) (*model.ProfileCookie, error) {
	// An organization that CDS does not know gets an empty key, which matches no row.

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while fetching profile cookie: %s", cookie)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfileCookie.Code,
			Message:     errors2.GetProfileCookie.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.GetCookieByCookieID

	results, err := dbClient.ExecuteQueryContext(ctx, query, cookie, orgID)

	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug(fmt.Sprintf("No profile cookie found with the given cookie: %s", cookie))
		return nil, nil
	}
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching profile cookie: %s", cookie)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfileCookie.Code,
			Message:     errors2.GetProfileCookie.Message,
			Description: errorMsg,
		}, err)
	}
	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("No profile cookie found with the given cookie: %s", cookie))
		return nil, nil
	}

	profileCookie := &model.ProfileCookie{
		CookieID:  results[0]["cookie_id"].(string),
		ProfileID: results[0][fieldProfileID].(string),
		IsActive:  results[0]["is_active"].(bool),
	}
	return profileCookie, nil
}

// UpdateProfileCookieByProfileID updates the status of a profile cookie
func UpdateProfileCookieByProfileID(ctx context.Context, profileID string, isActive bool) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while updating profile cookie with profileId: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfileCookie.Code,
			Message:     errors2.UpdateProfileCookie.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.UpdateCookieStatusByProfileID

	_, err = dbClient.ExecuteQueryContext(ctx, query, isActive, profileID)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed updating the profile cookie with profile Id: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfileCookie.Code,
			Message:     errors2.UpdateProfileCookie.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	return nil
}

// UpdateProfileCookieByCookieID updates the status of a profile cookie
func UpdateProfileCookieByCookieID(ctx context.Context, cookieID string, isActive bool) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while updating profile cookie: %s", cookieID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfileCookie.Code,
			Message:     errors2.UpdateProfileCookie.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.UpdateCookieStatusByCookieID

	_, err = dbClient.ExecuteQueryContext(ctx, query, isActive, cookieID)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed updating the profile cookie: %s", cookieID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfileCookie.Code,
			Message:     errors2.UpdateProfileCookie.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	return nil
}

// DeleteProfileCookieByProfile deletes a profile cookie by profileId
func DeleteProfileCookieByProfile(ctx context.Context, profileID string) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while deleting profile cookie with profileId: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DeleteProfileCookie.Code,
			Message:     errors2.DeleteProfileCookie.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.DeleteCookieByProfileID

	_, err = dbClient.ExecuteQueryContext(ctx, query, profileID)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed deleting the profile cookie with Id: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DeleteProfileCookie.Code,
			Message:     errors2.DeleteProfileCookie.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	return nil
}

// DeleteInactiveCookieProfiles deletes inactive cookie-profile records in batches.
func DeleteInactiveCookieProfiles(ctx context.Context, batchSize int) (int, error) {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := "Failed to get db client while cleaning up inactive cookie profiles"
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DeleteProfileCookie.Code,
			Message:     errors2.DeleteProfileCookie.Message,
			Description: errorMsg,
		}, err)
		return 0, serverError
	}
	defer func() { _ = dbClient.Close() }()

	query := scripts.DeleteInactiveCookies

	results, err := dbClient.ExecuteQueryContext(ctx, query, batchSize)
	if err != nil {
		errorMsg := "Failed to delete inactive cookie profiles"
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DeleteProfileCookie.Code,
			Message:     errors2.DeleteProfileCookie.Message,
			Description: errorMsg,
		}, err)
		return 0, serverError
	}

	deleted := len(results)
	if deleted > 0 {
		logger.Info(fmt.Sprintf("Cleaned up %d inactive cookie profiles", deleted))
	}
	return deleted, nil
}

// UpdateProfileConsents updates or creates consent records for a profile
func UpdateProfileConsents(ctx context.Context, profileID string, consents []model.ConsentRecord) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf(
			"Failed to get database client for updating profile consents for profile: %s",
			profileID,
		)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer func() { _ = dbClient.Close() }()

	// Start a transaction to ensure atomicity of consent updates
	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to begin transaction for updating consents for profile: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	// First, delete existing consents for this profile to ensure a clean slate

	deleteQuery := scripts.DeleteProfileConsentsByProfileID
	_, err = tx.ExecContext(ctx, deleteQuery, profileID)
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to delete existing consents for profile: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	// Insert new consent records
	insertQuery := scripts.InsertProfileConsentsByProfileID
	for _, consent := range consents {
		_, err = tx.ExecContext(ctx, insertQuery,
			profileID,
			consent.CategoryIdentifier,
			consent.IsConsented,
			consent.ConsentedAt)

		if err != nil {
			_ = tx.Rollback()
			errorMsg := fmt.Sprintf("Failed to insert consent for profile: %s, category: %s",
				profileID, consent.CategoryIdentifier)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.UpdateProfile.Code,
				Message:     errors2.UpdateProfile.Message,
				Description: errorMsg,
			}, err)
			return serverError
		}
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		errorMsg := fmt.Sprintf("Failed to commit transaction for updating consents for profile: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	logger.Info(fmt.Sprintf("Successfully updated consents for profile: %s", profileID))
	return nil
}

// FindOldestReferenceProfileIDByUserID returns the oldest reference profile
// with the supplied user ID, excluding the supplied profile ID.
func FindOldestReferenceProfileIDByUserID(ctx context.Context, orgID, userID,
	excludedProfileID string) (string, error) {
	if userID == "" {
		return "", nil
	}
	// An organization that CDS does not know gets an empty key, which matches no row.
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return "", fmt.Errorf("get database client for user-ID profile lookup: %w", err)
	}
	defer func() { _ = dbClient.Close() }()
	rows, err := dbClient.ExecuteQueryContext(ctx, scripts.FindOldestReferenceProfileIDByUserID,
		orgID, excludedProfileID, userID)
	if err != nil {
		return "", fmt.Errorf("find reference profile by user ID: %w", err)
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0][fieldProfileID].(string), nil
}

// FindOldestReferenceProfileIDByAttributeValues returns the oldest reference
// profile whose attribute at property matches one of values and whose user ID
// is compatible with the incoming profile. The supplied profile ID is excluded
// from the search.
func FindOldestReferenceProfileIDByAttributeValues(ctx context.Context, orgID, property string,
	values []string, excludedProfileID, incomingUserID string) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	// An organization that CDS does not know gets an empty key, which matches no row.
	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return "", fmt.Errorf("get database client for attribute profile lookup: %w", err)
	}
	defer func() { _ = dbClient.Close() }()
	query, matchArgs, err := scripts.BuildFindOldestReferenceProfileIDByAttributeValuesQuery(
		dbClient.DBType(), property, values)
	if err != nil {
		return "", fmt.Errorf("build attribute profile lookup: %w", err)
	}
	args := make([]interface{}, 0, 3+len(matchArgs))
	args = append(args, orgID, excludedProfileID, incomingUserID)
	args = append(args, matchArgs...)
	rows, err := dbClient.ExecuteQueryContext(ctx, query, args...)
	if err != nil {
		return "", fmt.Errorf("find reference profile by attribute values: %w", err)
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0][fieldProfileID].(string), nil
}
