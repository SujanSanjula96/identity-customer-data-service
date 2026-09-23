package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/profile/model"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// Unmarshal JSONB fields separately
func scanProfileRow(row map[string]interface{}) (model.Profile, error) {
	var (
		profile                       model.Profile
		traitsJSON, identityAttrsJSON []byte
	)

	profile.ProfileStatus = &model.ProfileStatus{}

	profile.ProfileId = row["profile_id"].(string)
	profile.UserId = row["user_id"].(string)
	profile.OrgHandle = row["org_handle"].(string)
	profile.CreatedAt = row["created_at"].(time.Time)
	profile.UpdatedAt = row["updated_at"].(time.Time)
	profile.Location = row["location"].(string)
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
			profile.ProfileStatus.ReferenceProfileId = row["reference_profile_id"].(string)
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
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return model.Profile{}, serverError
	}
	if err := json.Unmarshal(identityAttrsJSON, &profile.IdentityAttributes); err != nil {
		errorMsg := "Failed to unmarshal identity attributes."
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return model.Profile{}, serverError
	}
	return profile, nil
}

func scanProfileConsentRow(row map[string]interface{}) (model.ConsentRecord, error) {
	var profileConsent model.ConsentRecord

	profileConsent.CategoryIdentifier = row["category_id"].(string)
	profileConsent.IsConsented = row["consent_status"].(bool)
	profileConsent.ConsentedAt = row["consented_at"].(time.Time)
	return profileConsent, nil
}

// InsertProfile inserts a new profile into the database, together with its
// reference and its application data, in one transaction.
func InsertProfile(ctx context.Context, profile model.Profile) error {

	return RunInTransaction(ctx, func(ctx context.Context) error {
		return insertProfile(ctx, profile)
	})
}

func insertProfile(ctx context.Context, profile model.Profile) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := "Failed to get database client for adding a profile"
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.ADD_PROFILE.Code,
			Message:     errors2.ADD_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

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
		profile.ProfileId,
		profile.UserId,
		profile.OrgHandle,
		profile.CreatedAt,
		profile.UpdatedAt,
		profile.Location,
		profile.ProfileStatus.ListProfile,
		false, // delete_profile is not used in this context, set to false
		traitsJSON,
		identityJSON,
	)

	if err != nil {
		errorMsg := fmt.Sprintf("Failed to insert profile with Id: %s", profile.ProfileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.ADD_PROFILE.Code,
			Message:     errors2.ADD_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	referenceQuery := scripts.InsertProfileReference

	_, err = dbClient.ExecuteQueryContext(ctx, referenceQuery,
		profile.ProfileId,
		profileStatus,
		profile.ProfileStatus.ReferenceProfileId,
		profile.ProfileStatus.ReferenceReason,
		profile.OrgHandle,
		profile.OrgHandle,
	)

	if err != nil {
		errorMsg := fmt.Sprintf("Failed to insert profile referencea with Id: %s", profile.ProfileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.ADD_PROFILE.Code,
			Message:     errors2.ADD_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	err = InsertApplicationData(ctx, profile.ProfileId, profile.ApplicationData)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to insert profile with Id: %s", profile.ProfileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.ADD_PROFILE.Code,
			Message:     errors2.ADD_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	logger.Info("Profile added successfully: " + profile.ProfileId)
	return nil
}

func InsertApplicationData(ctx context.Context, profileId string, apps []model.ApplicationData) error {

	for _, app := range apps {
		// Construct the update map
		updateMap := make(map[string]interface{})

		// Flatten app-specific fields
		for k, v := range app.AppSpecificData {
			updateMap["application_data."+k] = v
		}

		// Use the existing upsert method
		err := UpsertAppDatum(ctx, profileId, app.AppId, updateMap)
		logger := log.GetLogger()
		if err != nil {
			errorMsg := fmt.Sprintf("Failed to insert application data for profile with Id: %s and appId: %s",
				profileId, app.AppId)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.ADD_APP_DATA.Code,
				Message:     errors2.ADD_APP_DATA.Message,
				Description: errorMsg,
			}, err)
			return serverError
		}
	}
	return nil
}

// GetProfile retrieves a profile by its Id
func GetProfile(ctx context.Context, profileId string) (*model.Profile, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while fetching profile with Id: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer dbClient.Close()

	query := scripts.GetProfileById

	results, err := dbClient.ExecuteQueryContext(ctx, query, profileId)

	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug(fmt.Sprintf("No profile found with the given Id: %s", profileId))
		// todo: should we return a client error with 404 here?
		return nil, nil
	}
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching profile with Id: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
	}
	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("No profile found with the given Id: %s", profileId))
		// todo: should we return a client error with 404 here?
		return nil, nil
	}
	profile, err := scanProfileRow(results[0])
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching profile with Id: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	profile.ApplicationData, err = FetchApplicationData(ctx, profileId)
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

// GetProfileConsents retrieves the consents of a profile by its profileId
func GetProfileConsents(ctx context.Context, profileId string) ([]model.ConsentRecord, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while fetching profile consents with Id: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer dbClient.Close()

	query := scripts.GetProfileConsentsByProfileId

	results, err := dbClient.ExecuteQueryContext(ctx, query, profileId)

	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug(fmt.Sprintf("No profile found with the given Id: %s", profileId))
		// todo: should we return a client error with 404 here?
		return nil, nil
	}
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching consents of profile with Id: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
	}
	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("No profile found with the given Id: %s", profileId))
		var profile, _ = GetProfile(ctx, profileId)
		if profile != nil {
			// If no consents found and the user exists, return an empty slice instead of nil
			return []model.ConsentRecord{}, nil
		} else {
			return nil, nil
		}
	}
	profileConsents := make([]model.ConsentRecord, 0)
	for _, row := range results {

		profileConsent, err := scanProfileConsentRow(row)
		if err != nil {
			errorMsg := fmt.Sprintf("Failed fetching profile consents with Id: %s", profileId)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.GET_PROFILE.Code,
				Message:     errors2.GET_PROFILE.Message,
				Description: errorMsg,
			}, err)
			return nil, serverError
		}
		profileConsents = append(profileConsents, profileConsent)
	}
	return profileConsents, nil
}

func FetchApplicationData(ctx context.Context, profileId string) ([]model.ApplicationData, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed getting db client for fetching application data for profile with"+
			" Id: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_APP_DATA.Code,
			Message:     errors2.GET_APP_DATA.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer dbClient.Close()
	query := scripts.GetAppDataByProfileId
	results, err := dbClient.ExecuteQueryContext(ctx, query, profileId)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching application data for profile with Id: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_APP_DATA.Code,
			Message:     errors2.GET_APP_DATA.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}

	apps := make([]model.ApplicationData, 0)
	for _, row := range results {
		var (
			appId     string
			appBlob   []byte
			appParsed model.ApplicationData
		)

		appId = row["app_id"].(string)
		appBlob = row["application_data"].([]byte)

		if err := json.Unmarshal(appBlob, &appParsed); err != nil {
			errorMsg := fmt.Sprintf("Failed to un marshal application data for profile with Id: %s", profileId)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.GET_APP_DATA.Code,
				Message:     errors2.GET_APP_DATA.Message,
				Description: errorMsg,
			}, err)
			return nil, serverError
		}

		apps = append(apps, model.ApplicationData{
			AppId:           appId,
			AppSpecificData: appParsed.AppSpecificData,
		})
	}
	return apps, nil
}

func FetchApplicationDataWithAppId(ctx context.Context, profileId string, appId string) (model.ApplicationData, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed getting db client for fetching application data of app:%s for profile: %s", appId, profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_APP_DATA.Code,
			Message:     errors2.GET_APP_DATA.Message,
			Description: errorMsg,
		}, err)
		return model.ApplicationData{}, serverError
	}
	defer dbClient.Close()
	query := scripts.GetAppDataByAppId
	results, err := dbClient.ExecuteQueryContext(ctx, query, profileId, appId)
	var app model.ApplicationData
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching application data of app:%s for profile: %s", appId, profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_APP_DATA.Code,
			Message:     errors2.GET_APP_DATA.Message,
			Description: errorMsg,
		}, err)
		return app, serverError
	}

	for _, row := range results {
		var (
			appId     string
			appBlob   []byte
			appParsed model.ApplicationData
		)

		appId = row["app_id"].(string)
		appBlob = row["application_data"].([]byte)

		if err := json.Unmarshal(appBlob, &appParsed); err != nil {
			errorMsg := fmt.Sprintf("Failed unmarshalling application data of app:%s for profile: %s", appId, profileId)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.GET_APP_DATA.Code,
				Message:     errors2.GET_APP_DATA.Message,
				Description: errorMsg,
			}, err)
			return app, serverError
		}

		app.AppId = appId
		app.AppSpecificData = appParsed.AppSpecificData
	}
	return app, nil
}

// UpdateProfile updates the profile, together with its reference and its
// application data, in one transaction.
func UpdateProfile(ctx context.Context, profile model.Profile) error {

	return RunInTransaction(ctx, func(ctx context.Context) error {
		return updateProfile(ctx, profile)
	})
}

func updateProfile(ctx context.Context, profile model.Profile) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get database client for updating profile: %s", profile.ProfileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

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

	_, err = dbClient.ExecuteQueryContext(ctx, query,
		profile.UserId,
		profile.ProfileStatus.ListProfile,
		profile.ProfileStatus.DeleteProfile,
		traitsJSON,
		identityJSON,
		profile.UpdatedAt,
		profile.ProfileId,
	)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed updating the profile: %s", profile.ProfileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	query = scripts.UpsertProfileReference

	_, err = dbClient.ExecuteQueryContext(ctx, query,
		profile.ProfileId,
		profileStatus,
		profile.ProfileStatus.ReferenceProfileId,
		profile.ProfileStatus.ReferenceReason,
		profile.ProfileId,
	)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed updating the profile: %s", profile.ProfileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	// Update application data
	err = InsertApplicationData(ctx, profile.ProfileId, profile.ApplicationData)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to insert profile with Id: %s", profile.ProfileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.ADD_PROFILE.Code,
			Message:     errors2.ADD_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	return nil
}

// GetAllProfiles retrieves profiles using cursor-based pagination.
// It returns up to `limit` profiles and a boolean indicating if more records exist.
func GetAllProfiles(ctx context.Context,
	orgHandle string, limit int, cursor *model.ProfileCursor) ([]model.Profile, bool, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := "Failed to get database client for fetching all profiles"
		logger.Debug(errorMsg, log.Error(err))
		return nil, false, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	// Defaults/safety
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	query := scripts.GetProfilesByOrgId

	var cursorTime interface{} = nil
	var cursorProfileId string = ""
	direction := "next"

	if cursor != nil {
		cursorTime = cursor.CreatedAt
		cursorProfileId = cursor.ProfileId
		if strings.TrimSpace(cursor.Direction) != "" {
			direction = strings.TrimSpace(cursor.Direction)
		}
		if direction != "next" && direction != "prev" {
			direction = "next"
		}
	}

	// lookahead
	limitPlusOne := limit + 1

	results, err := dbClient.ExecuteQueryContext(ctx, query, orgHandle, cursorTime, cursorProfileId, direction, limitPlusOne)
	if err != nil {
		errorMsg := "Failed fetching all profiles"
		logger.Debug(errorMsg, log.Error(err))
		return nil, false, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
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
		profileIDs = append(profileIDs, profile.ProfileId)
	}

	appDataMap, err := FetchApplicationDataBatch(ctx, profileIDs)
	if err != nil {
		errorMsg := "Failed fetching application data for profiles."
		logger.Debug(errorMsg, log.Error(err))
		return nil, false, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
	}

	for i := range profiles {
		profiles[i].ApplicationData = appDataMap[profiles[i].ProfileId]
	}

	// If direction=prev, SQL returned ASC to get “closest newer”.
	// Reverse so API always returns DESC order.
	if direction == "prev" {
		for i, j := 0, len(profiles)-1; i < j; i, j = i+1, j-1 {
			profiles[i], profiles[j] = profiles[j], profiles[i]
		}
	}

	return profiles, hasMore, nil
}

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
			Code:        errors2.GET_APP_DATA.Code,
			Message:     errors2.GET_APP_DATA.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

	// Build placeholders: $1,$2,... for Postgres
	placeholders := make([]string, 0, len(profileIDs))
	args := make([]interface{}, 0, len(profileIDs))
	for i, id := range profileIDs {
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
		args = append(args, id)
	}

	query := scripts.GetAppDataByProfileIds.Format(strings.Join(placeholders, ","))

	rows, err := dbClient.ExecuteQueryContext(ctx, query, args...)
	if err != nil {
		errorMsg := "Failed fetching application data batch."
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_APP_DATA.Code,
			Message:     errors2.GET_APP_DATA.Message,
			Description: errorMsg,
		}, err)
	}

	for _, row := range rows {
		pid := row["profile_id"].(string)
		appID := row["app_id"].(string)
		appBlob := row["application_data"].([]byte)

		var parsed model.ApplicationData
		if err := json.Unmarshal(appBlob, &parsed); err != nil {
			errorMsg := fmt.Sprintf("Failed to unmarshal application data for profile: %s", pid)
			logger.Debug(errorMsg, log.Error(err))
			return nil, errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.GET_APP_DATA.Code,
				Message:     errors2.GET_APP_DATA.Message,
				Description: errorMsg,
			}, err)
		}

		result[pid] = append(result[pid], model.ApplicationData{
			AppId:           appID,
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
func DeleteProfile(ctx context.Context, profileId string) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed getting db client for deleting the profile: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DELETE_PROFILE.Code,
			Message:     errors2.DELETE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

	// Step 1: Delete application_data explicitly (optional if ON DELETE CASCADE not enabled)
	_, err = dbClient.ExecuteQueryContext(ctx, scripts.DeleteProfileByProfileId, profileId)
	if err != nil {
		errorMsg := fmt.Sprintf("failed to delete application data for profile: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DELETE_PROFILE.Code,
			Message:     errors2.DELETE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	// Step 2: Delete child relationships where this is a parent (optional safety, ON DELETE CASCADE already exists)
	//:todo: Need to decide if its needed
	//_, err = dbClient.ExecuteQueryContext(ctx,
	//	`DELETE FROM profiles WHERE reference_profile_id = $1`, profileId)
	//if err != nil {
	//	errorMsg := fmt.Sprintf("failed to delete child profile links for profile: %s", profileId)
	//	logger.Debug(errorMsg, log.Error(err))
	//	serverError := errors2.NewServerError(errors2.ErrorMessage{
	//		Code:        errors2.DELETE_PROFILE.Code,
	//		Message:     errors2.DELETE_PROFILE.Message,
	//		Description: errorMsg,
	//	}, err)
	//	return serverError
	//}

	// Step 3: Delete the profile itself
	result, err := dbClient.ExecuteQueryContext(ctx, scripts.DeleteProfile, profileId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			logger.Debug(fmt.Sprintf("No profile found with the given Id: %s", profileId))
			return nil
		}
		errorMsg := fmt.Sprintf("failed to delete  profile: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DELETE_PROFILE.Code,
			Message:     errors2.DELETE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	rows := len(result)
	if rows == 0 {
		logger.Debug(fmt.Sprintf("No profile found with the given Id: %s. Probably would have been deleted already", profileId))
	}

	logger.Info(fmt.Sprintf("Profile: %s and associated data deleted successfully", profileId))
	return nil
}

// UpsertAppDatum merges the updates into the application data the profile has
// for the app. It holds the lock on the profile from the read to the write, so
// a concurrent upsert for the same profile cannot overwrite it.
func UpsertAppDatum(ctx context.Context, profileId string, appId string, updates map[string]interface{}) error {

	return WithProfilesLocked(ctx, []string{profileId}, func(ctx context.Context) error {
		return upsertAppDatum(ctx, profileId, appId, updates)
	})
}

func upsertAppDatum(ctx context.Context, profileId string, appId string, updates map[string]interface{}) error {

	// Fetch existing application_data for the given app
	appData, err := FetchApplicationDataWithAppId(ctx, profileId, appId)
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
	errorMsg := fmt.Sprintf("Failed to marshal devices for application data for profile: %s", profileId)
	if err != nil {
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_APP_DATA.Code,
			Message:     errors2.UPDATE_APP_DATA.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	// Upsert into application_data table
	query := scripts.InsertApplicationData

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get database client for upserting application data for profile: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_APP_DATA.Code,
			Message:     errors2.UPDATE_APP_DATA.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

	_, err = dbClient.ExecuteQueryContext(ctx, query, profileId, appId, jsonBytes)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to upsert application data for profile: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_APP_DATA.Code,
			Message:     errors2.UPDATE_APP_DATA.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	return nil
}

// DetachRefererProfileFromReference removes a child from a parent's child_profile_ids list
func DetachRefererProfileFromReference(ctx context.Context, referenceProfileId, profileId string) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get database client for deleting child relationship of child: %s of parent: %s", referenceProfileId, profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DELETE_PROFILE.Code,
			Message:     errors2.DELETE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

	// todo: decide if we need to delete the references as well.
	query := scripts.DeleteProfileReference
	result, err := dbClient.ExecuteQueryContext(ctx, query, referenceProfileId, profileId)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to delete child relationship of child: %s of parent: %s",
			referenceProfileId, profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DELETE_PROFILE.Code,
			Message:     errors2.DELETE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	rowsAffected := len(result)
	if rowsAffected == 0 {
		logger.Info(fmt.Sprintf("No child profile %s found under parent %s to remove.", profileId, referenceProfileId))
	}
	return nil
}

// InsertMergedMasterProfileAppData adds or updates application-specific context data.
func InsertMergedMasterProfileAppData(ctx context.Context, profileId string, newAppCtx model.ApplicationData) error {

	profile, err := GetProfile(ctx, profileId)
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch profile %s for app data update.", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	if profile == nil {
		errorMsg := fmt.Sprintf("Profile: %s not found for app data update", profileId)
		logger.Debug(errorMsg, log.Error(err))
		clientError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return clientError
	}

	updated := false
	var resultAppData []model.ApplicationData

	for _, existing := range profile.ApplicationData {
		if existing.AppId == newAppCtx.AppId {
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
	return InsertApplicationData(ctx, profile.ProfileId, profile.ApplicationData)
}

// InsertMergedMasterProfileTraitData replaces (PUT) the traits data inside Profile
func InsertMergedMasterProfileTraitData(ctx context.Context,
	profileId string, traitsData map[string]interface{}) error {

	profile, err := GetProfile(ctx, profileId)
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch profile %s for trait data update.", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	if profile == nil {
		errorMsg := fmt.Sprintf("Profile: %s not found for trait data update", profileId)
		logger.Debug(errorMsg, log.Error(err))
		clientError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return clientError
	}

	profile.Traits = traitsData
	return UpdateProfile(ctx, *profile) // Update existing profile
}

// MergeIdentityDataOfProfiles replaces or adds to identity_attributes in Profile
func MergeIdentityDataOfProfiles(ctx context.Context, profileId string, identityData map[string]interface{}) error {

	profile, err := GetProfile(ctx, profileId)
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch profile %s for identity data update.", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	if profile == nil {
		errorMsg := fmt.Sprintf("Profile: %s not found for identity data update", profileId)
		logger.Debug(errorMsg, log.Error(err))
		clientError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
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

	return UpdateProfile(ctx, *profile)
}

// GetAllProfilesWithFilter retrieves profiles using dynamic filters and cursor-based pagination.
func GetAllProfilesWithFilter(ctx context.Context,
	orgHandle string,
	filters []string,
	limit int,
	cursor *model.ProfileCursor,
) ([]model.Profile, bool, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := "Failed to get database client filtering profiles."
		logger.Debug(errorMsg, log.Error(err))
		return nil, false, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FILTER_PROFILE.Code,
			Message:     errors2.FILTER_PROFILE.Message,
			Description: errorMsg,
		}, err)
	}
	defer dbClient.Close()

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
	likeOperator := scripts.LikeOperator(dbType)

	var conditions []string
	var args []interface{}
	argID := 1
	joinedAppIDs := map[string]bool{}

	conditions = append(conditions, fmt.Sprintf("p.org_handle = $%d", argID))
	args = append(args, orgHandle)
	argID++

	// dynamic filter conditions
	for _, f := range filters {
		parts := strings.SplitN(f, " ", 3)
		if len(parts) != 3 {
			errorMsg := fmt.Sprintf("Invalid filter format: %s", f)
			logger.Debug(errorMsg)
			return nil, false, errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.FILTER_PROFILE.Code,
				Message:     errors2.FILTER_PROFILE.Message,
				Description: errorMsg,
			}, err)
		}
		field, operator, value := parts[0], parts[1], parts[2]

		var scope, key string
		if field == "user_id" || field == "profile_id" {
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
						Code:        errors2.FILTER_PROFILE.Code,
						Message:     errors2.FILTER_PROFILE.Message,
						Description: fmt.Sprintf("Invalid filter value for key: %s", key),
					}, err)
				}
				conditions = append(conditions, condition)
				args = append(args, arg)
			case "co", "sw":
				condition, err := scripts.JSONLikeCondition(dbType, jsonCol, []string{key}, argID)
				if err != nil {
					return nil, false, errors2.NewServerError(errors2.ErrorMessage{
						Code:        errors2.FILTER_PROFILE.Code,
						Message:     errors2.FILTER_PROFILE.Message,
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

		case "user_id":
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

		case "profile_id":
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
						Code:        errors2.FILTER_PROFILE.Code,
						Message:     errors2.FILTER_PROFILE.Message,
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
						Code:        errors2.FILTER_PROFILE.Code,
						Message:     errors2.FILTER_PROFILE.Message,
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
	var cursorProfileId string = ""
	direction := "next"
	if cursor != nil {
		cursorTime = cursor.CreatedAt
		cursorProfileId = cursor.ProfileId
		if strings.TrimSpace(cursor.Direction) != "" {
			direction = strings.TrimSpace(cursor.Direction)
		}
		if direction != "next" && direction != "prev" {
			direction = "next"
		}
	}

	// Add cursor predicates as parameterized conditions
	// Params: $argID = cursorTime, $(argID+1)=cursorProfileId
	if cursor != nil {
		if direction == "next" {
			conditions = append(conditions, scripts.KeysetCondition(dbType, "<", argID, argID+1))
		} else { // prev
			conditions = append(conditions, scripts.KeysetCondition(dbType, ">", argID, argID+1))
		}
		args = append(args, cursorTime, cursorProfileId)
		argID += 2
	}

	conditions = append(conditions, "r.profile_status = 'REFERENCE_PROFILE'")

	whereClause := "WHERE " + strings.Join(conditions, " AND ")

	// Direction-specific ORDER
	orderClause := "ORDER BY p.created_at DESC, p.profile_id DESC"
	if direction == "prev" {
		// fetch "newer" rows closest to cursor
		orderClause = "ORDER BY p.created_at ASC, p.profile_id ASC"
	}

	limitPlusOne := limit + 1
	finalSQL := fmt.Sprintf("%s\n%s\n%s\nLIMIT $%d", baseSQL, whereClause, orderClause, argID)
	args = append(args, limitPlusOne)

	results, err := dbClient.ExecuteQueryContext(ctx, scripts.GetAllProfilesWithFilterBase.WithSQL(finalSQL), args...)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to execute filtered query: %s", err)
		logger.Debug(errorMsg, log.Error(err))
		return nil, false, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.FILTER_PROFILE.Code,
			Message:     errors2.FILTER_PROFILE.Message,
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
		profileIDs = append(profileIDs, profile.ProfileId)
	}

	appDataMap, err := FetchApplicationDataBatch(ctx, profileIDs)
	if err != nil {
		errorMsg := "Failed fetching application data for profiles."
		logger.Debug(errorMsg, log.Error(err))
		return nil, false, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
	}

	for i := range profiles {
		profiles[i].ApplicationData = appDataMap[profiles[i].ProfileId]
	}

	// reverse back to DESC order for API
	if direction == "prev" {
		for i, j := 0, len(profiles)-1; i < j; i, j = i+1, j-1 {
			profiles[i], profiles[j] = profiles[j], profiles[i]
		}
	}

	return profiles, hasMore, nil
}

// sanitizeForAlias converts a string to a valid SQL alias by replacing special characters
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

func GetAllReferenceProfilesExceptForCurrent(ctx context.Context,
	currentProfile model.Profile) ([]model.Profile, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get database client for fetching master profiles for profile: %s",
			currentProfile.ProfileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer dbClient.Close()

	query := scripts.GetAllReferenceProfileExceptCurrent

	results, err := dbClient.ExecuteQueryContext(ctx, query, currentProfile.ProfileId, currentProfile.OrgHandle)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching all master profiles except for current profile: %s", currentProfile.ProfileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}

	var profiles []model.Profile
	for _, row := range results {
		var (
			profile                                                      model.Profile
			traitsJSON, identityJSON                                     []byte
			isReferenceProfile, isWaitOnUser, isWaitOnAdmin, listProfile bool
			referenceProfileId, profileStatus                            string
		)

		profile.UserId = row["user_id"].(string)
		profile.ProfileId = row["profile_id"].(string)
		referenceProfileId = row["reference_profile_id"].(string)
		listProfile = row["list_profile"].(bool)
		deleteProfile := row["delete_profile"].(bool)
		traitsJSON = row["traits"].([]byte)
		identityJSON = row["identity_attributes"].([]byte)
		profileStatus = row["profile_status"].(string) // Assuming profile_status is a boolean field
		if profileStatus == constants.ReferenceProfile {
			isReferenceProfile = true
		}
		if profileStatus == constants.WaitOnUser {
			isWaitOnUser = true
		}
		if profileStatus == constants.WaitOnAdmin {
			isWaitOnAdmin = true
		}

		profile.ProfileStatus = &model.ProfileStatus{
			IsReferenceProfile: isReferenceProfile,
			IsWaitingOnAdmin:   isWaitOnAdmin,
			IsWaitingOnUser:    isWaitOnUser,
			ReferenceProfileId: referenceProfileId,
			ListProfile:        listProfile,
			DeleteProfile:      deleteProfile,
		}

		if err := json.Unmarshal(traitsJSON, &profile.Traits); err != nil {
			errMsg := fmt.Sprintf("Failed to unmarshal traits for profile: %s", profile.ProfileId)
			logger.Debug(errMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.GET_PROFILE.Code,
				Message:     errors2.GET_PROFILE.Message,
				Description: errMsg,
			}, err)
			return nil, serverError
		}
		if err := json.Unmarshal(identityJSON, &profile.IdentityAttributes); err != nil {
			errMsg := fmt.Sprintf("Failed to unmarshal identity attributes for profile: %s", profile.ProfileId)
			logger.Debug(errMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.GET_PROFILE.Code,
				Message:     errors2.GET_PROFILE.Message,
				Description: errMsg,
			}, err)
			return nil, serverError
		}

		profile.ApplicationData, err = FetchApplicationData(ctx, profile.ProfileId)
		if err != nil {
			return nil, err
		}

		profiles = append(profiles, profile)
	}

	return profiles, nil
}

// UpdateProfileReferences updates the references of a parent profile with the
// provided child profiles, in one transaction.
func UpdateProfileReferences(ctx context.Context, parentProfile model.Profile, children []model.Reference) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get database client for adding child profiles for parent: %s",
			parentProfile.ProfileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

	query := scripts.UpdateProfileReference

	return dbClient.RunInTransaction(ctx, func(ctx context.Context) error {
		for _, child := range children {
			_, err := dbClient.ExecuteQueryContext(ctx, query, parentProfile.ProfileId, child.Reason,
				constants.MergedTo, child.ProfileId)
			if err != nil {
				errorMsg := fmt.Sprintf("Failed to insert referenced profile: %s for parent profile: %s",
					child.ProfileId, parentProfile.ProfileId)
				logger.Debug(errorMsg, log.Error(err))
				serverError := errors2.NewServerError(errors2.ErrorMessage{
					Code:        errors2.UPDATE_PROFILE.Code,
					Message:     errors2.UPDATE_PROFILE.Message,
					Description: errorMsg,
				}, err)
				return serverError
			}
		}
		return nil
	})
}

func FetchReferencedProfiles(ctx context.Context, referenceProfileId string) ([]model.Reference, error) {

	logger := log.GetLogger()
	logger.Info(fmt.Sprintf("Fetching referenced profiles for profile: %s", referenceProfileId))

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get database client for fetching child profiles for parent: %s",
			referenceProfileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer dbClient.Close()
	query := scripts.FetchReferencedProfiles

	results, err := dbClient.ExecuteQueryContext(ctx, query, referenceProfileId)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching referenced profiles for profile: %s", referenceProfileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}

	var children []model.Reference
	for _, row := range results {
		var reference model.Reference
		reference.ProfileId = row["profile_id"].(string)
		reference.Reason = row["reference_reason"].(string)
		children = append(children, reference)
	}

	if len(children) == 0 {
		logger.Info(fmt.Sprintf("No referenced profiles found for parent profile: %s", referenceProfileId))
	} else {
		logger.Info(fmt.Sprintf("Successfully fetched child profiles for parent profile: %s", referenceProfileId))
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

func GetProfileWithUserId(ctx context.Context, userId string) (*model.Profile, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while fetching profile with userId: %s", userId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer dbClient.Close()

	query := scripts.GetProfileByUserId

	results, err := dbClient.ExecuteQueryContext(ctx, query, userId)

	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug(fmt.Sprintf("No profile found with the given userId: %s", userId))
		// todo: should we return a client error with 404 here?
		return nil, nil
	}
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching profile with user Id: %s", userId)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
	}
	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("No profile found with the given userId: %s", userId))
		// todo: should we return a client error with 404 here?
		return nil, nil
	}
	profile, err := scanProfileRow(results[0])
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching profile with Id: %s", userId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE.Code,
			Message:     errors2.GET_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	profile.ApplicationData, _ = FetchApplicationData(ctx, profile.ProfileId)
	return &profile, nil
}

// CreateProfileCookie creates a new profile cookie
func CreateProfileCookie(ctx context.Context, profileCookie model.ProfileCookie) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while creating profile cookie with profileId: %s", profileCookie.ProfileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.CREATE_PROFILE_COOKIE.Code,
			Message:     errors2.CREATE_PROFILE_COOKIE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

	query := scripts.InsertCookie

	_, err = dbClient.ExecuteQueryContext(ctx, query, profileCookie.CookieId, profileCookie.ProfileId, profileCookie.IsActive)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed creating the profile cookie with Id: %s", profileCookie.CookieId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.CREATE_PROFILE_COOKIE.Code,
			Message:     errors2.CREATE_PROFILE_COOKIE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	return nil
}

// GetProfileCookieByProfileId retrieves a profile cookie by profileId
func GetProfileCookieByProfileId(ctx context.Context, profileId string) (*model.ProfileCookie, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while fetching profile cookie with profileId: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE_COOKIE.Code,
			Message:     errors2.GET_PROFILE_COOKIE.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer dbClient.Close()

	query := scripts.GetCookieByProfileId

	results, err := dbClient.ExecuteQueryContext(ctx, query, profileId)

	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug(fmt.Sprintf("No profile cookie found with the given profileId: %s", profileId))
		return nil, nil
	}
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching cookie of profile with Id: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE_COOKIE.Code,
			Message:     errors2.GET_PROFILE_COOKIE.Message,
			Description: errorMsg,
		}, err)
	}
	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("No profile cookie found with the given profileId: %s", profileId))
		return nil, nil
	}

	profileCookie := &model.ProfileCookie{
		CookieId:  results[0]["cookie_id"].(string),
		ProfileId: results[0]["profile_id"].(string),
		IsActive:  results[0]["is_active"].(bool),
	}
	return profileCookie, nil
}

// GetProfileCookie retrieves a profile cookie by profileId
func GetProfileCookie(ctx context.Context, cookie string) (*model.ProfileCookie, error) {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while fetching profile cookie: %s", cookie)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE_COOKIE.Code,
			Message:     errors2.GET_PROFILE_COOKIE.Message,
			Description: errorMsg,
		}, err)
		return nil, serverError
	}
	defer dbClient.Close()

	query := scripts.GetCookieByCookieId

	results, err := dbClient.ExecuteQueryContext(ctx, query, cookie)

	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug(fmt.Sprintf("No profile cookie found with the given cookie: %s", cookie))
		return nil, nil
	}
	if err != nil {
		errorMsg := fmt.Sprintf("Failed fetching profile cookie: %s", cookie)
		logger.Debug(errorMsg, log.Error(err))
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE_COOKIE.Code,
			Message:     errors2.GET_PROFILE_COOKIE.Message,
			Description: errorMsg,
		}, err)
	}
	if len(results) == 0 {
		logger.Debug(fmt.Sprintf("No profile cookie found with the given cookie: %s", cookie))
		return nil, nil
	}

	profileCookie := &model.ProfileCookie{
		CookieId:  results[0]["cookie_id"].(string),
		ProfileId: results[0]["profile_id"].(string),
		IsActive:  results[0]["is_active"].(bool),
	}
	return profileCookie, nil
}

// UpdateProfileCookieByProfileId updates the status of a profile cookie
func UpdateProfileCookieByProfileId(ctx context.Context, profileId string, isActive bool) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while updating profile cookie with profileId: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE_COOKIE.Code,
			Message:     errors2.UPDATE_PROFILE_COOKIE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

	query := scripts.UpdateCookieStatusByProfileId

	_, err = dbClient.ExecuteQueryContext(ctx, query, isActive, profileId)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed updating the profile cookie with profile Id: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE_COOKIE.Code,
			Message:     errors2.UPDATE_PROFILE_COOKIE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	return nil
}

// UpdateProfileCookieByCookieId updates the status of a profile cookie
func UpdateProfileCookieByCookieId(ctx context.Context, cookieId string, isActive bool) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while updating profile cookie: %s", cookieId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE_COOKIE.Code,
			Message:     errors2.UPDATE_PROFILE_COOKIE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

	query := scripts.UpdateCookieStatusByCookieId

	_, err = dbClient.ExecuteQueryContext(ctx, query, isActive, cookieId)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed updating the profile cookie: %s", cookieId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE_COOKIE.Code,
			Message:     errors2.UPDATE_PROFILE_COOKIE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	return nil
}

// DeleteProfileCookieByProfile deletes a profile cookie by profileId
func DeleteProfileCookieByProfile(ctx context.Context, profileId string) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get db client while deleting profile cookie with profileId: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DELETE_PROFILE_COOKIE.Code,
			Message:     errors2.DELETE_PROFILE_COOKIE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

	query := scripts.DeleteCookieByProfileId

	_, err = dbClient.ExecuteQueryContext(ctx, query, profileId)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed deleting the profile cookie with Id: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DELETE_PROFILE_COOKIE.Code,
			Message:     errors2.DELETE_PROFILE_COOKIE.Message,
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
			Code:        errors2.DELETE_PROFILE_COOKIE.Code,
			Message:     errors2.DELETE_PROFILE_COOKIE.Message,
			Description: errorMsg,
		}, err)
		return 0, serverError
	}
	defer dbClient.Close()

	query := scripts.DeleteInactiveCookies

	results, err := dbClient.ExecuteQueryContext(ctx, query, batchSize)
	if err != nil {
		errorMsg := "Failed to delete inactive cookie profiles"
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DELETE_PROFILE_COOKIE.Code,
			Message:     errors2.DELETE_PROFILE_COOKIE.Message,
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
func UpdateProfileConsents(ctx context.Context, profileId string, consents []model.ConsentRecord) error {
	dbClient, err := provider.NewDBProvider().GetDBClient()
	logger := log.GetLogger()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to get database client for updating profile consents for profile: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}
	defer dbClient.Close()

	// Start a transaction to ensure atomicity of consent updates
	tx, err := dbClient.BeginTxContext(ctx)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to begin transaction for updating consents for profile: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	// First, delete existing consents for this profile to ensure a clean slate

	deleteQuery := scripts.DeleteProfileConsentsByProfileId
	_, err = tx.ExecContext(ctx, deleteQuery, profileId)
	if err != nil {
		_ = tx.Rollback()
		errorMsg := fmt.Sprintf("Failed to delete existing consents for profile: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	// Insert new consent records
	insertQuery := scripts.InsertProfileConsentsByProfileId
	for _, consent := range consents {

		_, err = tx.ExecContext(ctx, insertQuery,
			profileId,
			consent.CategoryIdentifier,
			consent.IsConsented,
			consent.ConsentedAt)

		if err != nil {
			_ = tx.Rollback()
			errorMsg := fmt.Sprintf("Failed to insert consent for profile: %s, category: %s",
				profileId, consent.CategoryIdentifier)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.UPDATE_PROFILE.Code,
				Message:     errors2.UPDATE_PROFILE.Message,
				Description: errorMsg,
			}, err)
			return serverError
		}
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		errorMsg := fmt.Sprintf("Failed to commit transaction for updating consents for profile: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	logger.Info(fmt.Sprintf("Successfully updated consents for profile: %s", profileId))
	return nil
}
