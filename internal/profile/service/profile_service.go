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

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"

	consentStore "github.com/wso2/identity-customer-data-service/internal/consent/store"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	UnificationModel "github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
)

type ProfilesServiceInterface interface {
	DeleteProfile(ctx context.Context, profileId string) error
	GetAllProfilesCursor(ctx context.Context,
		orgHandle string, limit int, cursor *profileModel.ProfileCursor) ([]profileModel.ProfileResponse, bool, error)
	CreateProfile(ctx context.Context,
		profile profileModel.ProfileRequest, orgHandle string) (*profileModel.ProfileResponse, error)
	UpdateProfile(ctx context.Context,
		profileId, orgHandle string, update profileModel.ProfileRequest) (*profileModel.ProfileResponse, error)
	GetProfile(ctx context.Context, profileId string) (*profileModel.ProfileResponse, error)
	FindProfileByUserId(ctx context.Context, userId string) (*profileModel.ProfileResponse, error)
	GetAllProfilesWithFilterCursor(ctx context.Context, orgHandle string, filters []string, limit int, cursor *profileModel.ProfileCursor) ([]profileModel.ProfileResponse, bool, error)
	GetProfileConsents(ctx context.Context, profileId string) ([]profileModel.ConsentRecord, error)
	UpdateProfileConsents(ctx context.Context,
		profileId string, orgHandle string, consents []profileModel.ConsentRecord) error
	PatchProfile(ctx context.Context,
		profileId, orgHandle string, data map[string]interface{}) (*profileModel.ProfileResponse, error)
	GetProfileCookieByProfileId(ctx context.Context, profileId string) (*profileModel.ProfileCookie, error)
	GetProfileCookieById(ctx context.Context, cookie string) (*profileModel.ProfileCookie, error)
	CreateProfileCookie(ctx context.Context, profileId string) (*profileModel.ProfileCookie, error)
	UpdateCookieStatusByCookieId(ctx context.Context, cookieId string, isActive bool) error
	UpdateCookieStatusByProfileId(ctx context.Context, profileId string, isActive bool) error
	DeleteCookieByProfileId(ctx context.Context, profileId string) error
}

// ProfilesService is the default implementation of the ProfilesServiceInterface.
type ProfilesService struct{}

// GetProfilesService creates a new instance of EventsService.
func GetProfilesService() ProfilesServiceInterface {

	return &ProfilesService{}
}

var safeIdentifier = regexp.MustCompile(constants.FilterRegex)

func ConvertAppData(input map[string]interface{}) ([]profileModel.ApplicationData, error) {

	appDataList := make([]profileModel.ApplicationData, 0, len(input))

	for appID, val := range input {
		data, ok := val.(map[string]interface{})
		if !ok {
			return nil, errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UPDATE_PROFILE.Code,
				Message:     errors2.UPDATE_PROFILE.Message,
				Description: fmt.Sprintf("application_data entry '%s' must be of object type", appID),
			}, http.StatusBadRequest)
		}
		appSpecific := make(map[string]interface{})
		for key, value := range data {
			appSpecific[key] = value
		}
		appDataList = append(appDataList, profileModel.ApplicationData{
			AppId:           appID,
			AppSpecificData: appSpecific,
		})
	}

	return appDataList, nil
}

// WideAppDataMap converts map[string]map[string]interface{} to map[string]interface{} for use in ProfileRequest.
func WideAppDataMap(input map[string]map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(input))
	for k, v := range input {
		result[k] = v
	}
	return result
}

// CreateProfile creates a new profile.
func (ps *ProfilesService) CreateProfile(ctx context.Context,
	profileRequest profileModel.ProfileRequest, orgHandle string) (*profileModel.ProfileResponse, error) {

	rawSchema, err := schemaService.GetProfileSchemaService().GetProfileSchema(ctx, orgHandle)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("Error fetching profile schema for organization: %s", orgHandle)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.ADD_PROFILE.Code,
			Message:     errors2.ADD_PROFILE.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}

	var schema model.ProfileSchema
	schemaBytes, _ := json.Marshal(rawSchema) // serialize
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		errMsg := fmt.Sprintf("Invalid schema format for organization: %s while validating for profile creation.", orgHandle)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.ADD_PROFILE.Code,
			Message:     errors2.ADD_PROFILE.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}

	err = ValidateProfileAgainstSchema(profileRequest, profileModel.Profile{}, schema, false)
	if err != nil {
		return nil, err
	}

	// convert profile request to model
	createdTime := time.Now().UTC()
	profileId := uuid.New().String()
	appData, err := ConvertAppData(profileRequest.ApplicationData)
	if err != nil {
		return nil, err
	}
	profile := profileModel.Profile{
		ProfileId:          profileId,
		OrgHandle:          orgHandle,
		UserId:             profileRequest.UserId,
		ApplicationData:    appData,
		Traits:             profileRequest.Traits,
		IdentityAttributes: profileRequest.IdentityAttributes,
		ProfileStatus: &profileModel.ProfileStatus{
			IsReferenceProfile: true,
			ListProfile:        true,
		},
		CreatedAt: createdTime,
		UpdatedAt: createdTime,
		Location:  utils.BuildProfileLocation(orgHandle, profileId),
	}

	if err := profileStore.InsertProfile(ctx, profile); err != nil {
		logger.Debug(fmt.Sprintf("Error inserting profile: %s", profile.ProfileId), log.Error(err))
		return nil, err
	}
	profileFetched, errWait := ps.GetProfile(ctx, profileId)
	if errWait != nil || profileFetched == nil {
		logger.Warn(fmt.Sprintf("Profile: %s not available after insertion: %v", profile.ProfileId, errWait))
		return nil, errWait
	}

	queue := &workers.ProfileWorkerQueue{}

	config := UnificationModel.DefaultConfig()

	if config.ProfileUnificationTrigger.TriggerType == constants.SyncProfileOnUpdate {
		// Set organization handle for the profile before enqueuing
		profile.OrgHandle = orgHandle
		queue.Enqueue(profile)
	}

	logger.Info(fmt.Sprintf("Profile created successfully with profile id: %s", profile.ProfileId))
	return profileFetched, nil
}

func ValidateProfileAgainstSchema(profile profileModel.ProfileRequest, existingProfile profileModel.Profile,
	schema model.ProfileSchema, isUpdate bool) error {

	// Validate identity attributes
	for key, val := range profile.IdentityAttributes {
		if err := rejectIfFlattenedSubAttribute(key, "identity_attributes", schema.IdentityAttributes); err != nil {
			return err
		}
		attrName := "identity_attributes." + key
		attr, found := findAttributeInSchema(schema.IdentityAttributes, attrName)
		if !found {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UPDATE_PROFILE.Code,
				Message:     errors2.UPDATE_PROFILE.Message,
				Description: fmt.Sprintf("identity attribute '%s' not defined in schema", attrName),
			}, http.StatusBadRequest)
			return clientError
		}
		var existingVal interface{}
		if isUpdate && existingProfile.IdentityAttributes != nil {
			existingVal = existingProfile.IdentityAttributes[key]
		}
		if err := validateAttributeValueAgainstSchema(attr, val, existingVal, isUpdate, schema.IdentityAttributes,
			"identity attribute", key, isSystemIdentityAttribute(attr.AttributeName)); err != nil {
			return err
		}
	}

	// Validate traits
	for key, val := range profile.Traits {
		if err := rejectIfFlattenedSubAttribute(key, "traits", schema.Traits); err != nil {
			return err
		}
		attrName := "traits." + key
		attr, found := findAttributeInSchema(schema.Traits, attrName)
		if !found {
			clientError := errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UPDATE_PROFILE.Code,
				Message:     errors2.UPDATE_PROFILE.Message,
				Description: fmt.Sprintf("trait '%s' not defined in schema", attrName),
			}, http.StatusBadRequest)
			return clientError
		}
		var existingVal interface{}
		if isUpdate && existingProfile.Traits != nil {
			existingVal = existingProfile.Traits[key]
		}
		if err := validateAttributeValueAgainstSchema(attr, val, existingVal, isUpdate, schema.Traits,
			"trait", key, false); err != nil {
			return err
		}
	}

	// Validate application data
	for appID, rawAttrs := range profile.ApplicationData {
		attrs, ok := rawAttrs.(map[string]interface{})
		if !ok {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UPDATE_PROFILE.Code,
				Message:     errors2.UPDATE_PROFILE.Message,
				Description: fmt.Sprintf("application_data entry '%s' must be of object type", appID),
			}, http.StatusBadRequest)
		}
		for key, val := range attrs {
			if err := rejectIfFlattenedSubAttribute(key, "application_data", schema.ApplicationData[appID]); err != nil {
				return err
			}
			attrName := "application_data." + key
			attr, found := findAppAttributeInSchema(schema.ApplicationData, appID, attrName)
			if !found {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.UPDATE_PROFILE.Code,
					Message:     errors2.UPDATE_PROFILE.Message,
					Description: fmt.Sprintf("application_data '%s.%s' not defined in schema", appID, key),
				}, http.StatusBadRequest)
				return clientError
			}

			var existingVal interface{}
			if isUpdate {
				existingVal, _ = getAppDataValue(existingProfile.ApplicationData, appID, key)
			}

			if err := validateAttributeValueAgainstSchema(attr, val, existingVal, isUpdate, schema.ApplicationData[appID],
				"application_data", appID+"."+key, false); err != nil {
				return err
			}
		}
	}

	return nil
}

func validateAttributeValueAgainstSchema(attr model.ProfileSchemaAttribute, val, existingVal interface{}, isUpdate bool,
	scopeAttrs []model.ProfileSchemaAttribute, attributeLabel, attributePath string, skipMutability bool) error {
	if !skipMutability {
		logger := log.GetLogger()
		if err := validateMutability(attr.Mutability, isUpdate, existingVal, val); err != nil {
			logger.Debug(fmt.Sprintf("Mutability validation failed for attribute '%s' at path '%s'", attr.AttributeName, attributePath), log.Error(err))
			return err
		}
	}

	if !isValidType(val, attr.ValueType, attr.MultiValued, nil) {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: fmt.Sprintf("%s '%s': type mismatch", attributeLabel, attributePath),
		}, http.StatusBadRequest)
	}

	if !isValidCanonicalValue(val, attr.CanonicalValues) {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: fmt.Sprintf("%s '%s': value not in canonical values", attributeLabel, attributePath),
		}, http.StatusBadRequest)
	}

	if attr.ValueType == constants.ComplexDataType {
		if err := validateComplexSubAttributes(attr, val, existingVal, isUpdate, scopeAttrs, attributeLabel, attributePath); err != nil {
			return err
		}
	}

	return nil
}

func validateComplexSubAttributes(parentAttr model.ProfileSchemaAttribute, val, existingVal interface{}, isUpdate bool,
	scopeAttrs []model.ProfileSchemaAttribute, attributeLabel, attributePath string) error {
	switch v := val.(type) {
	case map[string]interface{}:
		var existingMap map[string]interface{}
		if current, ok := existingVal.(map[string]interface{}); ok {
			existingMap = current
		}
		return validateComplexObject(parentAttr, v, existingMap, isUpdate, scopeAttrs, attributeLabel, attributePath)
	case []interface{}:
		var existingSlice []interface{}
		if current, ok := existingVal.([]interface{}); ok {
			existingSlice = current
		}
		for i, item := range v {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			var existingMap map[string]interface{}
			if i < len(existingSlice) {
				if oldMap, ok := existingSlice[i].(map[string]interface{}); ok {
					existingMap = oldMap
				}
			}

			indexedPath := fmt.Sprintf("%s[%d]", attributePath, i)
			if err := validateComplexObject(parentAttr, itemMap, existingMap, isUpdate, scopeAttrs, attributeLabel, indexedPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateComplexObject(parentAttr model.ProfileSchemaAttribute, objVal map[string]interface{}, existingObj map[string]interface{},
	isUpdate bool, scopeAttrs []model.ProfileSchemaAttribute, attributeLabel, attributePath string) error {
	subAttrSchema := make(map[string]model.ProfileSchemaAttribute, len(parentAttr.SubAttributes))
	prefix := parentAttr.AttributeName + "."
	for _, subAttr := range parentAttr.SubAttributes {
		attr, found := findAttributeInSchema(scopeAttrs, subAttr.AttributeName)
		if !found {
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.UPDATE_PROFILE.Code,
				Message:     errors2.UPDATE_PROFILE.Message,
				Description: fmt.Sprintf("sub-attribute '%s' referenced by '%s' not found in schema", subAttr.AttributeName, parentAttr.AttributeName),
			}, fmt.Errorf("sub-attribute '%s' not found", subAttr.AttributeName))
		}

		childKey := strings.TrimPrefix(subAttr.AttributeName, prefix)
		subAttrSchema[childKey] = attr
	}

	for childKey, childVal := range objVal {
		childAttr, ok := subAttrSchema[childKey]
		if !ok {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UPDATE_PROFILE.Code,
				Message:     errors2.UPDATE_PROFILE.Message,
				Description: fmt.Sprintf("%s '%s.%s' not defined in schema", attributeLabel, attributePath, childKey),
			}, http.StatusBadRequest)
		}

		var existingChildVal interface{}
		if existingObj != nil {
			existingChildVal = existingObj[childKey]
		}

		childPath := attributePath + "." + childKey
		if err := validateAttributeValueAgainstSchema(childAttr, childVal, existingChildVal, isUpdate, scopeAttrs,
			attributeLabel, childPath, false); err != nil {
			return err
		}
	}

	return nil
}

func isSystemIdentityAttribute(attributeName string) bool {
	return attributeName == "identity_attributes.modified" ||
		attributeName == "identity_attributes.created" ||
		attributeName == "identity_attributes.userid"
}

// isValidCanonicalValue checks if the value is valid against the canonical values defined in the schema.
func isValidCanonicalValue(val interface{}, values []model.CanonicalValue) bool {
	if len(values) == 0 {
		return true // no restriction
	}

	// Build a lookup set
	allowed := make(map[string]bool)
	for _, v := range values {
		allowed[v.Value] = true
	}

	switch v := val.(type) {
	case string:
		return allowed[v]
	case []interface{}:
		for _, item := range v {
			str, ok := item.(string)
			if !ok || !allowed[str] {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func getAppDataValue(appDataList []profileModel.ApplicationData, appID, key string) (interface{}, bool) {
	for _, appData := range appDataList {
		if appData.AppId == appID {
			val, ok := appData.AppSpecificData[key]
			return val, ok
		}
	}
	return nil, false
}

// rejectIfFlattenedSubAttribute returns a 400 error if the key uses flattened dot-notation
// (e.g. "address.city") to address a sub-attribute of a complex parent (e.g. "traits.address").
// Callers should provide the nested object instead: {"address": {"city": "..."}}.
func rejectIfFlattenedSubAttribute(key, scope string, scopeAttrs []model.ProfileSchemaAttribute) error {
	dotIdx := strings.Index(key, ".")
	if dotIdx < 0 {
		return nil
	}
	parentAttrName := scope + "." + key[:dotIdx]
	parent, found := findAttributeInSchema(scopeAttrs, parentAttrName)
	if !found || parent.ValueType != constants.ComplexDataType {
		return nil
	}
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:    errors2.UPDATE_PROFILE.Code,
		Message: errors2.UPDATE_PROFILE.Message,
		Description: fmt.Sprintf("'%s' is a sub-attribute of complex attribute '%s'; provide '%s' as a nested object instead of using flattened key '%s'",
			scope+"."+key, parentAttrName, key[:dotIdx], key),
	}, http.StatusBadRequest)
}

func findAttributeInSchema(attrs []model.ProfileSchemaAttribute, name string) (model.ProfileSchemaAttribute, bool) {
	for _, attr := range attrs {
		if attr.AttributeName == name {
			return attr, true
		}
	}
	return model.ProfileSchemaAttribute{}, false
}

func findAppAttributeInSchema(appSchema map[string][]model.ProfileSchemaAttribute, appId, name string) (model.ProfileSchemaAttribute, bool) {
	attrs, ok := appSchema[appId]
	if !ok {
		return model.ProfileSchemaAttribute{}, false
	}
	for _, attr := range attrs {
		if attr.AttributeName == name {
			return attr, true
		}
	}
	return model.ProfileSchemaAttribute{}, false
}

// valuesEqualForMutability compares two values for equality, normalizing numeric types
// so that values like int64(1) and float64(1.0) are considered equal.
// When both values are integers, they are compared directly as int64 to preserve
// precision for large values (> 2^53) that would lose precision in float64.
// For non-numeric types, it falls back to reflect.DeepEqual.
func valuesEqualForMutability(oldVal, newVal interface{}) bool {
	oldInt, oldIsInt := toInt64(oldVal)
	newInt, newIsInt := toInt64(newVal)

	// Both are integer types: compare directly to preserve precision for large values.
	if oldIsInt && newIsInt {
		return oldInt == newInt
	}

	// Mixed int/float or both float: convert to float64.
	// This is safe because at least one side is already float64 (from JSON),
	// so precision is already bounded by float64 representation.
	oldFloat, oldIsNum := toFloat64(oldVal)
	newFloat, newIsNum := toFloat64(newVal)
	if oldIsNum && newIsNum {
		return oldFloat == newFloat
	}

	return reflect.DeepEqual(oldVal, newVal)
}

// toInt64 attempts to convert an integer value to int64.
// Returns 0 and false for floating-point types to ensure they go through float64 comparison.
func toInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	default:
		return 0, false
	}
}

// toFloat64 attempts to convert a numeric value to float64.
// Returns the float64 value and true if the input is numeric, or 0 and false otherwise.
func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

func validateMutability(mutability string, isUpdate bool, oldVal, newVal interface{}) error {
	switch mutability {
	case constants.MutabilityReadOnly:
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: "field is read-only or computed",
		}, http.StatusBadRequest)
	case constants.MutabilityImmutable:
		if isUpdate && !valuesEqualForMutability(oldVal, newVal) {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UPDATE_PROFILE.Code,
				Message:     errors2.UPDATE_PROFILE.Message,
				Description: "immutable field cannot be updated",
			}, http.StatusBadRequest)
		}
	case constants.MutabilityWriteOnce:
		if isUpdate && hasExistingValue(oldVal) && !valuesEqualForMutability(oldVal, newVal) {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UPDATE_PROFILE.Code,
				Message:     errors2.UPDATE_PROFILE.Message,
				Description: "write-once field cannot be changed after being set",
			}, http.StatusBadRequest)
		}
	case constants.MutabilityReadWrite, constants.MutabilityWriteOnly:
		return nil
	default:
		log.GetLogger().Warn("Unknown mutability type: " + mutability)
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: fmt.Sprintf("Unknown mutability: %s", mutability),
		}, http.StatusBadRequest)
	}
	return nil
}

func hasExistingValue(v interface{}) bool {
	if v == nil {
		return false
	}
	if s, ok := v.(string); ok {
		return s != ""
	}
	return true
}

func isValidType(value interface{}, expected string, multiValued bool, subAttrs []model.ProfileSchemaAttribute) bool {
	log.GetLogger().Info("Validating value type", log.String("expected", expected), log.Any("value", value))
	switch expected {
	case constants.StringDataType:
		if multiValued {
			arr, ok := value.([]interface{})
			if !ok {
				return false
			}
			for _, v := range arr {
				if _, ok := v.(string); !ok {
					return false
				}
			}
			return true
		}
		_, ok := value.(string)
		return ok

	case constants.DecimalDataType:
		if multiValued {
			arr, ok := value.([]interface{})
			if !ok {
				return false
			}
			for _, v := range arr {
				if _, ok := v.(float64); !ok {
					return false
				}
			}
			return true
		}
		_, ok := value.(float64)
		return ok

	case constants.IntegerDataType:
		if multiValued {
			arr, ok := value.([]interface{})
			if !ok {
				return false
			}
			for _, v := range arr {
				if num, ok := v.(float64); !ok || num != float64(int(num)) {
					return false
				}
			}
			return true
		}
		switch v := value.(type) {
		case float64:
			return v == float64(int(v)) // JSON numbers are float64
		case int:
			return true
		default:
			return false
		}

	case constants.BooleanDataType:
		if multiValued {
			arr, ok := value.([]interface{})
			if !ok {
				return false
			}
			for _, v := range arr {
				if _, ok := v.(bool); !ok {
					return false
				}
			}
			return true
		}
		_, ok := value.(bool)
		return ok

	case constants.EpochDataType:
		if multiValued {
			arr, ok := value.([]interface{})
			if !ok {
				return false
			}
			for _, v := range arr {
				if _, ok := v.(string); !ok {
					return false
				}
			}
			return true
		}
		_, ok := value.(string)
		return ok

	case constants.DateTimeDataType:
		if multiValued {
			arr, ok := value.([]interface{})
			if !ok {
				return false
			}
			for _, v := range arr {
				if _, ok := v.(string); !ok {
					return false
				}
			}
			return true
		}
		_, ok := value.(string) // optionally: validate ISO 8601
		return ok

	case constants.DateDataType:
		if multiValued {
			arr, ok := value.([]interface{})
			if !ok {
				return false
			}
			for _, v := range arr {
				if _, ok := v.(string); !ok {
					return false
				}
			}
			return true
		}
		_, ok := value.(string)
		return ok

	case constants.ComplexDataType:
		if multiValued {
			arr, ok := value.([]interface{})
			if !ok {
				return false
			}
			for _, item := range arr {
				_, ok := item.(map[string]interface{})
				if !ok {
					return false
				}
			}
			return true
		} else {
			_, ok := value.(map[string]interface{})
			return ok
		}
	default:
		return false
	}
}

// UpdateProfile creates or updates a profile
func (ps *ProfilesService) UpdateProfile(ctx context.Context,
	profileId, orgHandle string, updatedProfile profileModel.ProfileRequest) (*profileModel.ProfileResponse, error) {

	profile, err := profileStore.GetProfile(ctx, profileId)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("Error fetching profile for updatedProfile: %s", profileId)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}

	if profile == nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.PROFILE_NOT_FOUND.Code,
			Message:     errors2.PROFILE_NOT_FOUND.Message,
			Description: errors2.PROFILE_NOT_FOUND.Description,
		}, http.StatusNotFound)
		return nil, clientError
	}

	rawSchema, err := schemaService.GetProfileSchemaService().GetProfileSchema(ctx, profile.OrgHandle)
	if err != nil {
		return nil, err
	}

	// Convert map[string]interface{} → model.ProfileSchema
	var schema model.ProfileSchema
	schemaBytes, _ := json.Marshal(rawSchema) // serialize
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		errMsg := fmt.Sprintf("Invalid schema format for profile: %s while validating for profile update.", profile.ProfileId)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}

	err = ValidateProfileAgainstSchema(updatedProfile, *profile, schema, true)
	if err != nil {
		return nil, err
	}

	var profileToUpDate profileModel.Profile
	updatedTime := time.Now().UTC()
	appData, err := ConvertAppData(updatedProfile.ApplicationData)
	if err != nil {
		return nil, err
	}
	if profile.ProfileStatus.IsReferenceProfile {
		// convert profile request to model
		profileToUpDate = profileModel.Profile{
			ProfileId:          profileId,
			UserId:             updatedProfile.UserId,
			ApplicationData:    appData,
			Traits:             updatedProfile.Traits,
			IdentityAttributes: updatedProfile.IdentityAttributes,
			UpdatedAt:          updatedTime,
			CreatedAt:          profile.CreatedAt,
			Location:           profile.Location,
			ProfileStatus:      profile.ProfileStatus,
		}
	} else {
		// If it is a child profile, we need to update the master profile
		masterProfile, err := profileStore.GetProfile(ctx, profile.ProfileStatus.ReferenceProfileId)
		if err != nil {
			errMsg := fmt.Sprintf("Error fetching master profile for updatedProfile: %s", profile.ProfileId)
			logger.Debug(errMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.UPDATE_PROFILE.Code,
				Message:     errors2.UPDATE_PROFILE.Message,
				Description: errMsg,
			}, err)
			return nil, serverError
		}

		profileToUpDate = profileModel.Profile{
			ProfileId:          masterProfile.ProfileId,
			UserId:             updatedProfile.UserId,
			ApplicationData:    appData,
			Traits:             updatedProfile.Traits,
			IdentityAttributes: updatedProfile.IdentityAttributes,
			UpdatedAt:          updatedTime,
			CreatedAt:          masterProfile.CreatedAt,
			Location:           masterProfile.Location,
			ProfileStatus:      masterProfile.ProfileStatus,
		}
	}

	if err := profileStore.UpdateProfile(ctx, profileToUpDate); err != nil {
		logger.Error(fmt.Sprintf("Error updating profile: %s", profileToUpDate.ProfileId), log.Error(err))
		return nil, err
	}

	profileFetched, errWait := ps.GetProfile(ctx, profile.ProfileId)
	if errWait != nil || profileFetched == nil {
		return nil, errWait
	}

	config := UnificationModel.DefaultConfig()
	queue := &workers.ProfileWorkerQueue{}
	if config.ProfileUnificationTrigger.TriggerType == constants.SyncProfileOnUpdate {
		// Set organization handle for the profile before enqueuing
		profileToUpDate.OrgHandle = orgHandle
		queue.Enqueue(profileToUpDate)
	}
	logger.Info("Successfully updated profile: " + profileFetched.ProfileId)
	return profileFetched, nil
}

// ProfileUnificationQueue is an interface for the profile unification queue.
type ProfileUnificationQueue interface {
	Enqueue(profile profileModel.Profile)
}

func ConvertAppDataToMap(appDataList []profileModel.ApplicationData) map[string]map[string]interface{} {
	result := make(map[string]map[string]interface{})

	for _, appData := range appDataList {
		appMap := make(map[string]interface{})

		// Add all app-specific key-values
		for k, v := range appData.AppSpecificData {
			appMap[k] = v
		}

		result[appData.AppId] = appMap
	}

	return result
}

// GetProfile retrieves a profile
func (ps *ProfilesService) GetProfile(ctx context.Context, ProfileId string) (*profileModel.ProfileResponse, error) {

	profile, err := profileStore.GetProfile(ctx, ProfileId)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.PROFILE_NOT_FOUND.Code,
			Message:     errors2.PROFILE_NOT_FOUND.Message,
			Description: errors2.PROFILE_NOT_FOUND.Description,
		}, http.StatusNotFound)
		return nil, clientError
	}

	if profile.ProfileStatus.IsReferenceProfile {

		alias, err := profileStore.FetchReferencedProfiles(ctx, ProfileId)

		if err != nil {
			errorMsg := fmt.Sprintf("Error fetching references for profile: %s", ProfileId)
			logger := log.GetLogger()
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.GET_PROFILE.Code,
				Message:     errors2.GET_PROFILE.Message,
				Description: errorMsg,
			}, err)
			return nil, serverError
		}
		if len(alias) == 0 {
			alias = nil
		}

		profileResponse := &profileModel.ProfileResponse{
			ProfileId:          profile.ProfileId,
			UserId:             profile.UserId,
			ApplicationData:    ConvertAppDataToMap(profile.ApplicationData),
			Traits:             profile.Traits,
			IdentityAttributes: profile.IdentityAttributes,
			Meta: profileModel.Meta{
				CreatedAt: profile.CreatedAt,
				UpdatedAt: profile.UpdatedAt,
				Location:  profile.Location,
			},
			MergedFrom: alias,
		}
		return profileResponse, nil
	} else {
		// fetching merged master profile
		masterProfile, err := profileStore.GetProfile(ctx, profile.ProfileStatus.ReferenceProfileId)

		if err != nil {
			return nil, err
		}
		if masterProfile != nil {
			masterProfile.ApplicationData, err = profileStore.FetchApplicationData(ctx, masterProfile.ProfileId)
			if err != nil {
				return nil, err
			}

			alias := &profileModel.Reference{
				ProfileId: profile.ProfileStatus.ReferenceProfileId,
				Reason:    profile.ProfileStatus.ReferenceReason,
			}

			profileResponse := &profileModel.ProfileResponse{
				ProfileId:          profile.ProfileId,
				UserId:             masterProfile.UserId,
				ApplicationData:    ConvertAppDataToMap(masterProfile.ApplicationData),
				Traits:             masterProfile.Traits,
				IdentityAttributes: masterProfile.IdentityAttributes,
				Meta: profileModel.Meta{
					CreatedAt: masterProfile.CreatedAt,
					UpdatedAt: masterProfile.UpdatedAt,
					Location:  masterProfile.Location,
				},
				MergedTo: alias,
			}

			return profileResponse, nil
		}
		return nil, err
	}
}

// GetProfileConsents retrieves a profile
func (ps *ProfilesService) GetProfileConsents(ctx context.Context,
	ProfileId string) ([]profileModel.ConsentRecord, error) {

	consentRecords, err := profileStore.GetProfileConsents(ctx, ProfileId)
	if err != nil {
		return nil, err
	}
	if consentRecords == nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.PROFILE_NOT_FOUND.Code,
			Message:     errors2.PROFILE_NOT_FOUND.Message,
			Description: errors2.PROFILE_NOT_FOUND.Description,
		}, http.StatusNotFound)
		return nil, clientError
	}

	return consentRecords, nil
}

// UpdateProfileConsents updates the consent records for a profile
func (ps *ProfilesService) UpdateProfileConsents(ctx context.Context,
	profileId string, orgHandle string, consents []profileModel.ConsentRecord) error {
	logger := log.GetLogger()

	// Reject any attempt to modify a mandatory consent category.
	mandatoryIds, err := consentStore.GetMandatoryConsentCategoryIds(ctx, orgHandle)
	if err != nil {
		return err
	}
	mandatorySet := make(map[string]bool, len(mandatoryIds))
	for _, id := range mandatoryIds {
		mandatorySet[id] = true
	}
	for _, c := range consents {
		if mandatorySet[c.CategoryIdentifier] {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.CONSENT_CAT_MANDATORY.Code,
				Message:     errors2.CONSENT_CAT_MANDATORY.Message,
				Description: fmt.Sprintf("Consent for mandatory category '%s' cannot be modified.", c.CategoryIdentifier),
			}, http.StatusForbidden)
		}
	}

	// Set the consent timestamp if not already set
	currentTime := time.Now().UTC()
	for i := range consents {
		if consents[i].ConsentedAt.IsZero() {
			consents[i].ConsentedAt = currentTime
		}
	}

	// Update the consents in the database
	err = profileStore.UpdateProfileConsents(ctx, profileId, consents)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to update consents for profile: %s", profileId)
		logger.Debug(errorMsg, log.Error(err))
		return err
	}

	return nil
}

// DeleteProfile removes a profile from MongoDB by `perma_id`
func (ps *ProfilesService) DeleteProfile(ctx context.Context, ProfileId string) error {

	// Fetch the existing profile before deletion
	profile, err := profileStore.GetProfile(ctx, ProfileId)
	logger := log.GetLogger()
	if profile == nil {
		logger.Warn(fmt.Sprintf("Profile with profile_id: %s that is requested for deletion is not found",
			ProfileId))
		return nil
	}
	if err != nil {
		errorMsg := fmt.Sprintf("Error deleting profile with profile_id: %s", ProfileId)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DELETE_PROFILE.Code,
			Message:     errors2.DELETE_PROFILE.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	if profile.ProfileStatus.IsReferenceProfile {
		// fetching the child if its parent
		profile.ProfileStatus.References, _ = profileStore.FetchReferencedProfiles(ctx, profile.ProfileId)
	}

	if profile.ProfileStatus.IsReferenceProfile && len(profile.ProfileStatus.References) == 0 {
		logger.Info(fmt.Sprintf("Deleting parent profile: %s with no children", ProfileId))
		// Delete the parent with no children
		err = profileStore.DeleteProfile(ctx, ProfileId)
		if err != nil {
			errorMsg := fmt.Sprintf("Error deleting profile with profile_id: %s which is a parent and no children", ProfileId)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.DELETE_PROFILE.Code,
				Message:     errors2.DELETE_PROFILE.Message,
				Description: errorMsg,
			}, err)
			return serverError
		}
		return nil
	}

	if profile.ProfileStatus.IsReferenceProfile && len(profile.ProfileStatus.References) > 0 {
		//get all child profiles and delete
		for _, childProfile := range profile.ProfileStatus.References {
			err = profileStore.DeleteProfile(ctx, childProfile.ProfileId)
			logger.Info(fmt.Sprintf("Deleting child  profile: %s with of parent: %s",
				childProfile.ProfileId, ProfileId))

			if err != nil {
				errorMsg := fmt.Sprintf("Error while deleting profile with profile_id: %s ", childProfile.ProfileId)
				logger.Debug(errorMsg, log.Error(err))
				serverError := errors2.NewServerError(errors2.ErrorMessage{
					Code:        errors2.DELETE_PROFILE.Code,
					Message:     errors2.DELETE_PROFILE.Message,
					Description: errorMsg,
				}, err)
				return serverError
			}
		}
		// now delete master
		err = profileStore.DeleteProfile(ctx, ProfileId)
		logger.Info(fmt.Sprintf("Deleting parent profile: %s with children", ProfileId))
		if err != nil {
			errorMsg := fmt.Sprintf("Error while deleting parent profile: %s ", ProfileId)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.DELETE_PROFILE.Code,
				Message:     errors2.DELETE_PROFILE.Message,
				Description: errorMsg,
			}, err)
			return serverError
		}
		return nil
	}

	// If it is a child profile, delete it
	if !(profile.ProfileStatus.IsReferenceProfile) {

		logger.Info(fmt.Sprintf("Deleting child profile: %s with parent: %s", ProfileId,
			profile.ProfileStatus.ReferenceProfileId))
		parentProfile, err := profileStore.GetProfile(ctx, profile.ProfileStatus.ReferenceProfileId)
		if err != nil {
			errorMsg := fmt.Sprintf("Error while deleting the child profile: %s ", ProfileId)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.DELETE_PROFILE.Code,
				Message:     errors2.DELETE_PROFILE.Message,
				Description: errorMsg,
			}, err)
			return serverError
		}
		parentProfile.ProfileStatus.References, _ = profileStore.FetchReferencedProfiles(ctx, parentProfile.ProfileId)

		if len(parentProfile.ProfileStatus.References) == 1 {
			// delete the parent as this is the only child
			logger.Info(fmt.Sprintf("Deleting parent profile: %s with of current : %s",
				profile.ProfileStatus.ReferenceProfileId, ProfileId))
			err = profileStore.DeleteProfile(ctx, profile.ProfileStatus.ReferenceProfileId)
			if err != nil {
				errorMsg := fmt.Sprintf("Error while deleting the master profile: %s ", ProfileId)
				logger.Debug(errorMsg, log.Error(err))
				serverError := errors2.NewServerError(errors2.ErrorMessage{
					Code:        errors2.DELETE_PROFILE.Code,
					Message:     errors2.DELETE_PROFILE.Message,
					Description: errorMsg,
				}, err)
				return serverError
			}
			//todo: Ensure the need to detach the referer profile from the reference
			//err = profileStore.DetachRefererProfileFromReference(profile.ProfileStatus.ReferenceProfileId, ProfileId)
			err = profileStore.DeleteProfile(ctx, ProfileId)
			if err != nil {
				errorMsg := fmt.Sprintf("Error while deleting the  profile: %s ", ProfileId)
				logger.Debug(errorMsg, log.Error(err))
				serverError := errors2.NewServerError(errors2.ErrorMessage{
					Code:        errors2.DELETE_PROFILE.Code,
					Message:     errors2.DELETE_PROFILE.Message,
					Description: errorMsg,
				}, err)
				return serverError
			}
			logger.Info(fmt.Sprintf("Deleted current profile: %s with parent: %s", ProfileId,
				profile.ProfileStatus.ReferenceProfileId))
		} else {
			err = profileStore.DetachRefererProfileFromReference(ctx, profile.ProfileStatus.ReferenceProfileId, ProfileId)
			if err != nil {
				errorMsg := fmt.Sprintf("Error while current profile from parent: %s ", ProfileId)
				logger.Debug(errorMsg, log.Error(err))
				serverError := errors2.NewServerError(errors2.ErrorMessage{
					Code:        errors2.DELETE_PROFILE.Code,
					Message:     errors2.DELETE_PROFILE.Message,
					Description: errorMsg,
				}, err)
				return serverError
			}
			logger.Debug(fmt.Sprintf("Detaching current profile: %s from parent: %s", ProfileId,
				profile.ProfileStatus.ReferenceProfileId))
			err = profileStore.DeleteProfile(ctx, ProfileId)
			if err != nil {
				errorMsg := fmt.Sprintf("Error while deleting the current profile: %s ", ProfileId)
				logger.Debug(errorMsg, log.Error(err))
				serverError := errors2.NewServerError(errors2.ErrorMessage{
					Code:        errors2.DELETE_PROFILE.Code,
					Message:     errors2.DELETE_PROFILE.Message,
					Description: errorMsg,
				}, err)
				return serverError
			}
			logger.Info(fmt.Sprintf("Deleted current profile: %s with parent: %s",
				ProfileId, profile.ProfileStatus.ReferenceProfileId))
		}

	}

	return nil
}

// GetAllProfilesCursor retrieves all master profiles with pagination using cursor.
// Merged profiles are not included in list but provided in the reference
func (ps *ProfilesService) GetAllProfilesCursor(ctx context.Context,
	orgHandle string,
	limit int,
	cursor *profileModel.ProfileCursor,
) ([]profileModel.ProfileResponse, bool, error) {

	existingProfiles, hasMore, err := profileStore.GetAllProfiles(ctx, orgHandle, limit, cursor)
	if err != nil {
		return nil, false, err
	}
	if existingProfiles == nil {
		return []profileModel.ProfileResponse{}, false, nil
	}

	if len(existingProfiles) > limit {
		hasMore = true
		existingProfiles = existingProfiles[:limit]
	}

	result := make([]profileModel.ProfileResponse, 0, len(existingProfiles))

	for _, profile := range existingProfiles {

		//  Base row meta must be preserved for cursor correctness
		baseMeta := profileModel.Meta{
			CreatedAt: profile.CreatedAt,
			UpdatedAt: profile.UpdatedAt,
			Location:  profile.Location,
		}

		alias, err := profileStore.FetchReferencedProfiles(ctx, profile.ProfileId)
		if err != nil {
			errorMsg := fmt.Sprintf("Error fetching references for profile: %s", profile.ProfileId)
			logger := log.GetLogger()
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.GET_PROFILE.Code,
				Message:     errors2.GET_PROFILE.Message,
				Description: errorMsg,
			}, err)
			return nil, false, serverError
		}
		if len(alias) == 0 {
			alias = nil
		}

		if profile.ProfileStatus.IsReferenceProfile {
			result = append(result, profileModel.ProfileResponse{
				ProfileId:          profile.ProfileId,
				UserId:             profile.UserId,
				ApplicationData:    ConvertAppDataToMap(profile.ApplicationData),
				Traits:             profile.Traits,
				IdentityAttributes: profile.IdentityAttributes,
				Meta:               baseMeta,
				MergedFrom:         alias,
			})
			continue
		}
	}

	return result, hasMore, nil
}

// GetAllProfilesWithFilterCursor retrieves filtered master profiles with pagination using cursor.
// Merged profiles are not included in list but provided in the reference
func (ps *ProfilesService) GetAllProfilesWithFilterCursor(ctx context.Context,
	orgHandle string,
	filters []string,
	limit int,
	cursor *profileModel.ProfileCursor,
) ([]profileModel.ProfileResponse, bool, error) {

	propertyTypeMap := make(map[string]string)

	// Rewrite filters (keep your logic)
	rewrittenFilters := make([]string, 0, len(filters))
	for _, f := range filters {
		parts := strings.SplitN(f, " ", 3)
		if len(parts) != 3 {
			return nil, false, errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.FILTER_PROFILE.Code,
				Message:     errors2.FILTER_PROFILE.Message,
				Description: "Invalid filter format when filtering profiles.",
			}, http.StatusBadRequest)
		}

		field, operator, rawValue := parts[0], parts[1], parts[2]

		// Validate operator
		switch operator {
		case "eq", "co", "sw":
		default:
			return nil, false, errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.FILTER_PROFILE.Code,
				Message:     errors2.FILTER_PROFILE.Message,
				Description: fmt.Sprintf("Unsupported operator: %s", operator),
			}, http.StatusBadRequest)
		}

		// Validate field/key
		if field != "user_id" && field != "profile_id" {
			if !isValidFilterKey(field) {
				return nil, false, errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.FILTER_PROFILE.Code,
					Message:     errors2.FILTER_PROFILE.Message,
					Description: "Invalid filter key: " + field,
				}, http.StatusBadRequest)
			}
		}

		valueType := propertyTypeMap[field]
		typedVal := parseTypedValueForFilters(valueType, rawValue)

		var valueStr string
		switch v := typedVal.(type) {
		case string:
			valueStr = v
		default:
			valueStr = fmt.Sprintf("%v", v)
		}

		rewrittenFilters = append(rewrittenFilters, fmt.Sprintf("%s %s %s", field, operator, valueStr))
	}

	// Fetch matching profiles WITH cursor + limit
	filteredProfiles, hasMore, err := profileStore.GetAllProfilesWithFilter(ctx, orgHandle, rewrittenFilters, limit, cursor)
	if err != nil {
		return nil, false, err
	}
	if filteredProfiles == nil {
		return []profileModel.ProfileResponse{}, false, nil
	}

	// Optional safety if store forgot trimming
	if len(filteredProfiles) > limit {
		hasMore = true
		filteredProfiles = filteredProfiles[:limit]
	}

	result := make([]profileModel.ProfileResponse, 0, len(filteredProfiles))

	for _, profile := range filteredProfiles {

		// meta data must be preserved for cursor correctness
		baseMeta := profileModel.Meta{
			CreatedAt: profile.CreatedAt,
			UpdatedAt: profile.UpdatedAt,
			Location:  profile.Location,
		}

		alias, err := profileStore.FetchReferencedProfiles(ctx, profile.ProfileId)
		if err != nil {
			return nil, false, err
		}
		if profile.ProfileStatus.IsReferenceProfile {
			result = append(result, profileModel.ProfileResponse{
				ProfileId:          profile.ProfileId,
				UserId:             profile.UserId,
				ApplicationData:    ConvertAppDataToMap(profile.ApplicationData),
				Traits:             profile.Traits,
				IdentityAttributes: profile.IdentityAttributes,
				Meta:               baseMeta,
				MergedFrom:         alias,
			})
			continue
		}
	}

	return result, hasMore, nil
}

// isValidFilterKey ensures the filter key is valid and does not contain any malicious patterns.
func isValidFilterKey(key string) bool {

	logger := log.GetLogger()

	if key == "" {
		logger.Debug("Empty filter key is not allowed while filtering profiles.")
		return false
	}
	if !safeIdentifier.MatchString(key) {
		logger.Debug(fmt.Sprintf("Filter key:%s does not match allowed pattern for filtering profiles.", key))
		return false
	}
	if strings.HasPrefix(key, ".") || strings.HasSuffix(key, ".") || strings.Contains(key, "..") {
		logger.Debug(fmt.Sprintf("Filter key:%s cannot start or end with a dot or contain consecutive dots", key))
		return false
	}
	return true
}

func parseTypedValueForFilters(valueType string, raw string) interface{} {
	switch valueType {
	case "int":
		i, _ := strconv.Atoi(raw)
		return i
	case "float", "double":
		f, _ := strconv.ParseFloat(raw, 64)
		return f
	case "boolean":
		return raw == "true"
	case "string":
		return raw
	default:
		return raw
	}
}

// FindProfileByUserId retrieves a profile by user_id
func (ps *ProfilesService) FindProfileByUserId(ctx context.Context,
	userId string) (*profileModel.ProfileResponse, error) {

	profile, err := profileStore.GetProfileWithUserId(ctx, userId)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.PROFILE_NOT_FOUND.Code,
			Message:     errors2.PROFILE_NOT_FOUND.Message,
			Description: errors2.PROFILE_NOT_FOUND.Description,
		}, http.StatusNotFound)
		return nil, clientError
	}

	alias, err := profileStore.FetchReferencedProfiles(ctx, profile.ProfileId)

	if err != nil {
		return nil, err
	}
	if len(alias) == 0 {
		alias = nil
	}
	profileResponse := &profileModel.ProfileResponse{
		ProfileId:          profile.ProfileId,
		UserId:             profile.UserId,
		ApplicationData:    ConvertAppDataToMap(profile.ApplicationData),
		Traits:             profile.Traits,
		IdentityAttributes: profile.IdentityAttributes,
		Meta: profileModel.Meta{
			CreatedAt: profile.CreatedAt,
			UpdatedAt: profile.UpdatedAt,
			Location:  profile.Location,
		},
		MergedFrom: alias,
	}
	return profileResponse, nil
}

// PatchProfile applies a partial update to an existing profile
func (ps *ProfilesService) PatchProfile(ctx context.Context,
	profileId, orgHandle string, patch map[string]interface{}) (*profileModel.ProfileResponse, error) {

	existingProfile, err := profileStore.GetProfile(ctx, profileId)
	if err != nil {
		return nil, err
	}
	if existingProfile == nil {
		return nil, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.PROFILE_NOT_FOUND.Code,
			Message:     errors2.PROFILE_NOT_FOUND.Message,
			Description: fmt.Sprintf("Profile %s not found", profileId),
		}, http.StatusNotFound)
	}

	// For a child profile (merged into a master), the patch must be applied to the
	// master's current data. Using the child's raw stored document as the base would
	// cause UpdateProfile to overwrite the master with incomplete data, losing all
	// accumulated merged data that only exists on the master.
	profileForBase := existingProfile
	if existingProfile.ProfileStatus != nil && !existingProfile.ProfileStatus.IsReferenceProfile && existingProfile.ProfileStatus.ReferenceProfileId != "" {
		masterProfile, err := profileStore.GetProfile(ctx, existingProfile.ProfileStatus.ReferenceProfileId)
		if err != nil {
			return nil, err
		}
		if masterProfile != nil {
			masterProfile.ApplicationData, err = profileStore.FetchApplicationData(ctx, masterProfile.ProfileId)
			if err != nil {
				return nil, err
			}
			profileForBase = masterProfile
		} else {
			logger := log.GetLogger()
			logger.Warn(fmt.Sprintf("Master profile %s not found for child profile %s. Using the child profile",
				existingProfile.ProfileStatus.ReferenceProfileId, profileId))
		}
	}

	// Convert the full profile to map to allow patching.
	// Marshal the profile but override application_data with the map representation
	// (the stored format is []ApplicationData — a JSON array — which cannot be unmarshalled
	// back into ProfileRequest.ApplicationData map[string]interface{}).
	fullData, _ := json.Marshal(profileForBase)
	var merged map[string]interface{}
	_ = json.Unmarshal(fullData, &merged)
	merged["application_data"] = ConvertAppDataToMap(profileForBase.ApplicationData)

	// Handle deep merge for nested objects first
	if traitsPatch, ok := patch["traits"].(map[string]interface{}); ok {
		if existingTraits, ok := merged["traits"].(map[string]interface{}); ok {
			merged["traits"] = DeepMerge(existingTraits, traitsPatch)
		} else {
			merged["traits"] = traitsPatch
		}
	}

	if identityPatch, ok := patch["identity_attributes"].(map[string]interface{}); ok {
		if existingIdentity, ok := merged["identity_attributes"].(map[string]interface{}); ok {
			merged["identity_attributes"] = DeepMerge(existingIdentity, identityPatch)
		} else {
			merged["identity_attributes"] = identityPatch
		}
	}

	if appDataPatch, ok := patch["application_data"].(map[string]interface{}); ok {
		if existingAppData, ok := merged["application_data"].(map[string]interface{}); ok {
			merged["application_data"] = DeepMerge(existingAppData, appDataPatch)
		} else {
			merged["application_data"] = appDataPatch
		}
	}

	// Now apply top-level scalar fields
	for k, v := range patch {
		if k == "traits" || k == "identity_attributes" || k == "application_data" {
			continue // already handled
		}
		merged[k] = v
	}
	// Convert merged data back to ProfileRequest
	mergedBytes, _ := json.Marshal(merged)
	var updatedProfileReq profileModel.ProfileRequest
	if err := json.Unmarshal(mergedBytes, &updatedProfileReq); err != nil {
		logger := log.GetLogger()
		errMsg := fmt.Sprintf("Error unmarshalling merged profile data for profile_id: %s", profileId)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_PROFILE.Code,
			Message:     errors2.UPDATE_PROFILE.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}

	// Reuse the PUT logic to update the profile
	return ps.UpdateProfile(ctx, profileId, orgHandle, updatedProfileReq)
}

func (ps *ProfilesService) GetProfileCookieByProfileId(ctx context.Context,
	profileId string) (*profileModel.ProfileCookie, error) {

	cookie, err := profileStore.GetProfileCookieByProfileId(ctx, profileId)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("Error fetching profile cookie by profile_id: %s", profileId)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE_COOKIE.Code,
			Message:     errors2.GET_PROFILE_COOKIE.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}
	if cookie == nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.PROFILE_COOKIE_NOT_FOUND.Code,
			Message:     errors2.PROFILE_COOKIE_NOT_FOUND.Message,
			Description: fmt.Sprintf("Profile cookie for profile_id %s not found", profileId),
		}, http.StatusNotFound)
		return nil, clientError
	}
	return cookie, nil
}

func (ps *ProfilesService) GetProfileCookieById(ctx context.Context,
	cookie string) (*profileModel.ProfileCookie, error) {

	cookieObj, err := profileStore.GetProfileCookie(ctx, cookie)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("Error fetching profile cookie : %s", cookie)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_PROFILE_COOKIE.Code,
			Message:     errors2.GET_PROFILE_COOKIE.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}
	if cookieObj == nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.PROFILE_COOKIE_NOT_FOUND.Code,
			Message:     errors2.PROFILE_COOKIE_NOT_FOUND.Message,
			Description: fmt.Sprintf("Profile cookie : %s not found", cookie),
		}, http.StatusNotFound)
		return nil, clientError
	}
	return cookieObj, nil
}

// CreateProfileCookie creates a new profile cookie
func (ps *ProfilesService) CreateProfileCookie(ctx context.Context,
	profileId string) (*profileModel.ProfileCookie, error) {

	cookie := profileModel.ProfileCookie{
		ProfileId: profileId,
		CookieId:  uuid.New().String(),
		IsActive:  true,
	}
	err := profileStore.CreateProfileCookie(ctx, cookie)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("Error creating profile cookie by profile_id: %s", cookie.ProfileId)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GET_COOKIE.Code,
			Message:     errors2.GET_COOKIE.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}
	return &cookie, nil
}

// UpdateCookieStatusByProfileId updates the status of a profile cookie by profile_id
func (ps *ProfilesService) UpdateCookieStatusByProfileId(ctx context.Context, profileId string, isActive bool) error {

	err := profileStore.UpdateProfileCookieByProfileId(ctx, profileId, isActive)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("Error updating profile cookie by profile_id: %s", profileId)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_COOKIE.Code,
			Message:     errors2.UPDATE_COOKIE.Message,
			Description: errMsg,
		}, err)
		return serverError
	}
	return nil
}

// UpdateCookieStatusByCookieId updates the status of a profile cookie by cookie id
func (ps *ProfilesService) UpdateCookieStatusByCookieId(ctx context.Context, cookieId string, isActive bool) error {

	err := profileStore.UpdateProfileCookieByCookieId(ctx, cookieId, isActive)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("Error updating profile cookie: %s", cookieId)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UPDATE_COOKIE.Code,
			Message:     errors2.UPDATE_COOKIE.Message,
			Description: errMsg,
		}, err)
		return serverError
	}
	return nil
}

// DeleteCookieByProfileId deletes a profile cookie by profile_id
func (ps *ProfilesService) DeleteCookieByProfileId(ctx context.Context, profileId string) error {

	err := profileStore.DeleteProfileCookieByProfile(ctx, profileId)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("Error deleting profile cookie by profile_id: %s", profileId)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DELETE_COOKIE.Code,
			Message:     errors2.DELETE_COOKIE.Message,
			Description: errMsg,
		}, err)
		return serverError
	}
	return nil
}

// DeepMerge merges two maps recursively, with src overwriting dst
func DeepMerge(dst, src map[string]interface{}) map[string]interface{} {
	for k, v := range src {
		if vMap, ok := v.(map[string]interface{}); ok {
			if dstMap, ok := dst[k].(map[string]interface{}); ok {
				dst[k] = DeepMerge(dstMap, vMap)
			} else {
				dst[k] = vMap
			}
		} else {
			dst[k] = v
		}
	}
	return dst
}
