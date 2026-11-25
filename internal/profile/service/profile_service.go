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

// Package service implements the business logic for customer profiles, their consents and cookies.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"

	"github.com/google/uuid"

	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/engine"
	irModel "github.com/wso2/identity-customer-data-service/internal/identity_resolution/model"
	irStore "github.com/wso2/identity-customer-data-service/internal/identity_resolution/store"
	"github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
	"github.com/wso2/identity-customer-data-service/internal/system/workers"
	urService "github.com/wso2/identity-customer-data-service/internal/unification_rules/service"

	consentStore "github.com/wso2/identity-customer-data-service/internal/consent/store"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	"github.com/wso2/identity-customer-data-service/internal/profile_schema/identitysource"
	schemaService "github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	UnificationModel "github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
)

// ProfilesServiceInterface defines the operations on customer profiles, their consents and cookies.
type ProfilesServiceInterface interface {
	DeleteProfile(ctx context.Context, profileID string, orgID string) error
	GetAllProfilesCursor(ctx context.Context,
		orgID string, limit int, cursor *profileModel.ProfileCursor) ([]profileModel.ProfileResponse, bool, error)
	CreateProfile(ctx context.Context,
		profile profileModel.ProfileRequest, orgID string) (*profileModel.ProfileResponse, error)
	UpdateProfile(ctx context.Context,
		profileID string, orgID string, update profileModel.ProfileRequest) (*profileModel.ProfileResponse, error)
	GetProfile(ctx context.Context, profileID string, orgID string) (*profileModel.ProfileResponse, error)
	FindProfileByUserID(ctx context.Context, userID string, orgID string) (*profileModel.ProfileResponse, error)
	GetAllProfilesWithFilterCursor(
		ctx context.Context,
		orgID string,
		filters []string,
		limit int,
		cursor *profileModel.ProfileCursor,
	) ([]profileModel.ProfileResponse, bool, error)
	GetProfileConsents(ctx context.Context, profileID string, orgID string) ([]profileModel.ConsentRecord, error)
	UpdateProfileConsents(ctx context.Context,
		profileID string, orgID string, consents []profileModel.ConsentRecord) error
	PatchProfile(ctx context.Context,
		profileID string, orgID string, data map[string]interface{}) (*profileModel.ProfileResponse, error)
	GetProfileCookieByProfileID(ctx context.Context, profileID string) (*profileModel.ProfileCookie, error)
	GetProfileCookieByID(ctx context.Context, cookie string, orgID string) (*profileModel.ProfileCookie, error)
	CreateProfileCookie(ctx context.Context, profileID string) (*profileModel.ProfileCookie, error)
	UpdateCookieStatusByCookieID(ctx context.Context, cookieID string, isActive bool) error
	UpdateCookieStatusByProfileID(ctx context.Context, profileID string, isActive bool) error
	DeleteCookieByProfileID(ctx context.Context, profileID string) error
	GetProfilesWithFuzzyResolution(ctx context.Context,
		orgID string, filters []string, threshold float64, limit int) ([]profileModel.FuzzyMatchResult, error)
	GetProfilesHybrid(ctx context.Context, orgID string, fuzzyFilters []string,
		deterministicFilters []string, threshold float64, limit int) ([]profileModel.FuzzyMatchResult, error)
}

// ProfilesService is the default implementation of the ProfilesServiceInterface.
type ProfilesService struct{}

// The profile fields that are columns of their own rather than attributes.
const (
	fieldUserID    = "user_id"
	fieldProfileID = "profile_id"
)

// GetProfilesService creates a new instance of EventsService.
func GetProfilesService() ProfilesServiceInterface {
	return &ProfilesService{}
}

var safeIdentifier = regexp.MustCompile(constants.FilterRegex)

// ConvertAppData converts application data keyed by app ID into ApplicationData entries. It returns a
// client error if an entry is not an object.
func ConvertAppData(input map[string]interface{}) ([]profileModel.ApplicationData, error) {
	appDataList := make([]profileModel.ApplicationData, 0, len(input))

	for appID, val := range input {
		data, ok := val.(map[string]interface{})
		if !ok {
			return nil, errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UpdateProfile.Code,
				Message:     errors2.UpdateProfile.Message,
				Description: fmt.Sprintf("application_data entry '%s' must be of object type", appID),
			}, http.StatusBadRequest)
		}
		appSpecific := make(map[string]interface{})
		for key, value := range data {
			appSpecific[key] = value
		}
		appDataList = append(appDataList, profileModel.ApplicationData{
			AppID:           appID,
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
	profileRequest profileModel.ProfileRequest, orgID string) (*profileModel.ProfileResponse, error) {
	rawSchema, err := schemaService.GetProfileSchemaService().GetProfileSchema(ctx, orgID)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("Error fetching profile schema for organization: %s", orgID)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.AddProfile.Code,
			Message:     errors2.AddProfile.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}

	var schema model.ProfileSchema
	schemaBytes, _ := json.Marshal(rawSchema) // serialize
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		errMsg := fmt.Sprintf(
			"Invalid schema format for organization: %s while validating for profile creation.",
			orgID,
		)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.AddProfile.Code,
			Message:     errors2.AddProfile.Message,
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
	profileID := uuid.New().String()
	appData, err := ConvertAppData(profileRequest.ApplicationData)
	if err != nil {
		return nil, err
	}
	profile := profileModel.Profile{
		ProfileID:          profileID,
		OrgID:              orgID,
		UserID:             profileRequest.UserID,
		ApplicationData:    appData,
		Traits:             profileRequest.Traits,
		IdentityAttributes: profileRequest.IdentityAttributes,
		ProfileStatus: &profileModel.ProfileStatus{
			IsReferenceProfile: true,
			ListProfile:        true,
		},
		CreatedAt: createdTime,
		UpdatedAt: createdTime,
	}

	if err := profileStore.InsertProfile(ctx, orgID, profile); err != nil {
		logger.Debug(fmt.Sprintf("Error inserting profile: %s", profile.ProfileID), log.Error(err))
		return nil, err
	}
	profileFetched, errWait := ps.GetProfile(ctx, profileID, orgID)
	if errWait != nil || profileFetched == nil {
		logger.Warn(fmt.Sprintf("Profile: %s not available after insertion: %v", profile.ProfileID, errWait))
		return nil, errWait
	}

	queue := &workers.ProfileWorkerQueue{}

	config := UnificationModel.DefaultConfig()

	if config.ProfileUnificationTrigger.TriggerType == constants.SyncProfileOnUpdate {
		// Only enqueue if the profile has at least one attribute matching an active unification rule.
		activeRules, rulesErr := urService.GetUnificationRuleService().GetUnificationRules(ctx, orgID)
		if rulesErr != nil {
			logger.Warn("CreateProfile: failed to load unification rules, enqueueing anyway", log.Error(rulesErr))
			profile.OrgID = orgID
			queue.Enqueue(profile)
		} else {
			// A profile carrying a userId must always be evaluated: merging profiles that
			// share a userId is a system invariant that holds with no rules configured, so
			// gating on rule attributes alone would silently disable it.
			if profile.UserID != "" || hasAttributeMatchingAnyRule(flattenProfileAttrs(profile), activeRules) {
				profile.OrgID = orgID
				queue.Enqueue(profile)
			}
		}
	}

	logger.Info(fmt.Sprintf("Profile created successfully with profile id: %s", profile.ProfileID))
	return profileFetched, nil
}

// ValidateProfileAgainstSchema checks that every identity attribute, trait and application data attribute
// in the request is defined in the schema and matches its type and mutability; on update, mutability is
// checked against the existing profile.
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
				Code:        errors2.UpdateProfile.Code,
				Message:     errors2.UpdateProfile.Message,
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
				Code:        errors2.UpdateProfile.Code,
				Message:     errors2.UpdateProfile.Message,
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
				Code:        errors2.UpdateProfile.Code,
				Message:     errors2.UpdateProfile.Message,
				Description: fmt.Sprintf("application_data entry '%s' must be of object type", appID),
			}, http.StatusBadRequest)
		}
		for key, val := range attrs {
			if err := rejectIfFlattenedSubAttribute(key, "application_data",
				schema.ApplicationData[appID]); err != nil {
				return err
			}
			attrName := "application_data." + key
			attr, found := findAppAttributeInSchema(schema.ApplicationData, appID, attrName)
			if !found {
				clientError := errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.UpdateProfile.Code,
					Message:     errors2.UpdateProfile.Message,
					Description: fmt.Sprintf("application_data '%s.%s' not defined in schema", appID, key),
				}, http.StatusBadRequest)
				return clientError
			}

			var existingVal interface{}
			if isUpdate {
				existingVal, _ = getAppDataValue(existingProfile.ApplicationData, appID, key)
			}

			if err := validateAttributeValueAgainstSchema(attr, val, existingVal, isUpdate,
				schema.ApplicationData[appID], "application_data", appID+"."+key, false); err != nil {
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
			logger.Debug(
				fmt.Sprintf(
					"Mutability validation failed for attribute '%s' at path '%s'",
					attr.AttributeName,
					attributePath,
				),
				log.Error(err),
			)
			return err
		}
	}

	if !isValidType(val, attr.ValueType, attr.MultiValued) {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: fmt.Sprintf("%s '%s': type mismatch", attributeLabel, attributePath),
		}, http.StatusBadRequest)
	}

	if !isValidCanonicalValue(val, attr.CanonicalValues) {
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: fmt.Sprintf("%s '%s': value not in canonical values", attributeLabel, attributePath),
		}, http.StatusBadRequest)
	}

	if attr.ValueType == constants.ComplexDataType {
		if err := validateComplexSubAttributes(attr, val, existingVal, isUpdate, scopeAttrs, attributeLabel,
			attributePath); err != nil {
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
			if err := validateComplexObject(parentAttr, itemMap, existingMap, isUpdate, scopeAttrs, attributeLabel,
				indexedPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateComplexObject(
	parentAttr model.ProfileSchemaAttribute,
	objVal map[string]interface{},
	existingObj map[string]interface{},
	isUpdate bool,
	scopeAttrs []model.ProfileSchemaAttribute,
	attributeLabel, attributePath string,
) error {
	subAttrSchema := make(map[string]model.ProfileSchemaAttribute, len(parentAttr.SubAttributes))
	prefix := parentAttr.AttributeName + "."
	for _, subAttr := range parentAttr.SubAttributes {
		attr, found := findAttributeInSchema(scopeAttrs, subAttr.AttributeName)
		if !found {
			return errors2.NewServerError(errors2.ErrorMessage{
				Code:    errors2.UpdateProfile.Code,
				Message: errors2.UpdateProfile.Message,
				Description: fmt.Sprintf(
					"sub-attribute '%s' referenced by '%s' not found in schema",
					subAttr.AttributeName,
					parentAttr.AttributeName,
				),
			}, fmt.Errorf("sub-attribute '%s' not found", subAttr.AttributeName))
		}

		childKey := strings.TrimPrefix(subAttr.AttributeName, prefix)
		subAttrSchema[childKey] = attr
	}

	for childKey, childVal := range objVal {
		childAttr, ok := subAttrSchema[childKey]
		if !ok {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UpdateProfile.Code,
				Message:     errors2.UpdateProfile.Message,
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
		if appData.AppID == appID {
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
		Code:    errors2.UpdateProfile.Code,
		Message: errors2.UpdateProfile.Message,
		Description: fmt.Sprintf(
			"'%s' is a sub-attribute of complex attribute '%s'; provide '%s' as a nested object "+
				"instead of using flattened key '%s'",
			scope+"."+key,
			parentAttrName,
			key[:dotIdx],
			key,
		),
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

func findAppAttributeInSchema(
	appSchema map[string][]model.ProfileSchemaAttribute,
	appID, name string,
) (model.ProfileSchemaAttribute, bool) {
	attrs, ok := appSchema[appID]
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
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: "field is read-only or computed",
		}, http.StatusBadRequest)
	case constants.MutabilityImmutable:
		if isUpdate && !valuesEqualForMutability(oldVal, newVal) {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UpdateProfile.Code,
				Message:     errors2.UpdateProfile.Message,
				Description: "immutable field cannot be updated",
			}, http.StatusBadRequest)
		}
	case constants.MutabilityWriteOnce:
		if isUpdate && hasExistingValue(oldVal) && !valuesEqualForMutability(oldVal, newVal) {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.UpdateProfile.Code,
				Message:     errors2.UpdateProfile.Message,
				Description: "write-once field cannot be changed after being set",
			}, http.StatusBadRequest)
		}
	case constants.MutabilityReadWrite, constants.MutabilityWriteOnly:
		return nil
	default:
		log.GetLogger().Warn("Unknown mutability type: " + mutability)
		return errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
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

func isValidType(value interface{}, expected string, multiValued bool) bool {
	// The value is personal data, so the record names only the expected type.
	log.GetLogger().Debug("Validating value type", log.String("expected", expected))
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
	profileID string, orgID string, updatedProfile profileModel.ProfileRequest) (*profileModel.ProfileResponse, error) {
	profile, err := profileStore.GetProfile(ctx, profileID, orgID)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("Error fetching profile for updatedProfile: %s", profileID)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}
	return ps.updateProfile(ctx, profileID, orgID, profile, updatedProfile)
}

// updateProfile updates a profile that the caller has read already, so that a caller which needs
// the stored profile itself, as PatchProfile does, reads it once rather than twice.
func (ps *ProfilesService) updateProfile(ctx context.Context, profileID string, orgID string,
	profile *profileModel.Profile, updatedProfile profileModel.ProfileRequest) (*profileModel.ProfileResponse, error) {
	logger := log.GetLogger()
	if profile == nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ProfileNotFound.Code,
			Message:     errors2.ProfileNotFound.Message,
			Description: errors2.ProfileNotFound.Description,
		}, http.StatusNotFound)
		return nil, clientError
	}

	rawSchema, err := schemaService.GetProfileSchemaService().GetProfileSchema(ctx, orgID)
	if err != nil {
		return nil, err
	}

	// Convert map[string]interface{} → model.ProfileSchema
	var schema model.ProfileSchema
	schemaBytes, _ := json.Marshal(rawSchema) // serialize
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		errMsg := fmt.Sprintf(
			"Invalid schema format for profile: %s while validating for profile update.",
			profile.ProfileID,
		)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
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
			ProfileID:          profileID,
			OrgID:              profile.OrgID,
			UserID:             updatedProfile.UserID,
			ApplicationData:    appData,
			Traits:             updatedProfile.Traits,
			IdentityAttributes: updatedProfile.IdentityAttributes,
			UpdatedAt:          updatedTime,
			CreatedAt:          profile.CreatedAt,
			ProfileStatus:      profile.ProfileStatus,
		}
	} else {
		// If it is a child profile, we need to update the master profile
		masterProfile, err := profileStore.GetProfile(ctx, profile.ProfileStatus.ReferenceProfileID, orgID)
		if err != nil {
			errMsg := fmt.Sprintf("Error fetching master profile for updatedProfile: %s", profile.ProfileID)
			logger.Debug(errMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.UpdateProfile.Code,
				Message:     errors2.UpdateProfile.Message,
				Description: errMsg,
			}, err)
			return nil, serverError
		}

		profileToUpDate = profileModel.Profile{
			ProfileID:          masterProfile.ProfileID,
			OrgID:              masterProfile.OrgID,
			UserID:             updatedProfile.UserID,
			ApplicationData:    appData,
			Traits:             updatedProfile.Traits,
			IdentityAttributes: updatedProfile.IdentityAttributes,
			UpdatedAt:          updatedTime,
			CreatedAt:          masterProfile.CreatedAt,
			ProfileStatus:      masterProfile.ProfileStatus,
		}
	}

	if err := profileStore.UpdateProfile(ctx, orgID, profileToUpDate); err != nil {
		logger.Error(fmt.Sprintf("Error updating profile: %s", profileToUpDate.ProfileID), log.Error(err))
		return nil, err
	}

	profileFetched, errWait := ps.GetProfile(ctx, profile.ProfileID, orgID)
	if errWait != nil || profileFetched == nil {
		return nil, errWait
	}

	config := UnificationModel.DefaultConfig()
	queue := &workers.ProfileWorkerQueue{}
	if config.ProfileUnificationTrigger.TriggerType == constants.SyncProfileOnUpdate {
		// Only enqueue if the profile has at least one attribute matching an active unification rule.
		activeRules, rulesErr := urService.GetUnificationRuleService().GetUnificationRules(ctx, orgID)
		if rulesErr != nil {
			logger.Warn("UpdateProfile: failed to load unification rules, enqueueing anyway", log.Error(rulesErr))
			profileToUpDate.OrgID = orgID
			queue.Enqueue(profileToUpDate)
		} else {
			before := flattenProfileAttrs(*profile)
			after := flattenProfileAttrs(profileToUpDate)
			matchedValuesChanged := ruleValuesChanged(before, after, activeRules)

			// Rejections are deliberately not cleared here. One records a human deciding
			// two profiles are different people, which does not stop being true because
			// an attribute changed — and discarding it on a value change put the same
			// dismissed pair back in front of the administrator whenever anything moved.
			// The evidence stored with the rejection is compared against each fresh
			// evaluation instead, so only a materially stronger match reopens it.

			if shouldResolveAfterUpdate(*profile, profileToUpDate, matchedValuesChanged, activeRules) {
				profileToUpDate.OrgID = orgID
				queue.Enqueue(profileToUpDate)
			}
		}
	}
	logger.Info("Successfully updated profile: " + profileFetched.ProfileID)
	return profileFetched, nil
}

// ProfileUnificationQueue is an interface for the profile unification queue.
type ProfileUnificationQueue interface {
	Enqueue(profile profileModel.Profile)
}

// ConvertAppDataToMap converts ApplicationData entries into a map of application-specific data keyed by app ID.
func ConvertAppDataToMap(appDataList []profileModel.ApplicationData) map[string]map[string]interface{} {
	result := make(map[string]map[string]interface{})

	for _, appData := range appDataList {
		appMap := make(map[string]interface{})

		// Add all app-specific key-values
		for k, v := range appData.AppSpecificData {
			appMap[k] = v
		}

		result[appData.AppID] = appMap
	}

	return result
}

// GetProfile retrieves a profile
func (ps *ProfilesService) GetProfile(
	ctx context.Context,
	profileID string,
	orgID string,
) (*profileModel.ProfileResponse, error) {
	profile, err := profileStore.GetProfile(ctx, profileID, orgID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ProfileNotFound.Code,
			Message:     errors2.ProfileNotFound.Message,
			Description: errors2.ProfileNotFound.Description,
		}, http.StatusNotFound)
		return nil, clientError
	}
	orgHandle, err := orgService.GetOrganizationHandle(ctx, orgID)
	if err != nil {
		return nil, err
	}

	if profile.ProfileStatus.IsReferenceProfile {
		alias, err := profileStore.FetchReferencedProfiles(ctx, profileID)

		if err != nil {
			errorMsg := fmt.Sprintf("Error fetching references for profile: %s", profileID)
			logger := log.GetLogger()
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.GetProfile.Code,
				Message:     errors2.GetProfile.Message,
				Description: errorMsg,
			}, err)
			return nil, serverError
		}
		if len(alias) == 0 {
			alias = nil
		}

		profileResponse := &profileModel.ProfileResponse{
			ProfileID:          profile.ProfileID,
			UserID:             profile.UserID,
			ApplicationData:    ConvertAppDataToMap(profile.ApplicationData),
			Traits:             profile.Traits,
			IdentityAttributes: profile.IdentityAttributes,
			Meta: profileModel.Meta{
				CreatedAt: profile.CreatedAt,
				UpdatedAt: profile.UpdatedAt,
				Location:  utils.BuildProfileLocation(ctx, orgHandle, profile.ProfileID),
			},
			MergedFrom: alias,
		}
		return profileResponse, nil
	} else {
		// fetching merged master profile
		masterProfile, err := profileStore.GetProfile(ctx, profile.ProfileStatus.ReferenceProfileID, orgID)

		if err != nil {
			return nil, err
		}
		if masterProfile != nil {
			masterProfile.ApplicationData, err = profileStore.FetchApplicationData(ctx, masterProfile.ProfileID)
			if err != nil {
				return nil, err
			}

			alias := &profileModel.Reference{
				ProfileID: profile.ProfileStatus.ReferenceProfileID,
				Reason:    profile.ProfileStatus.ReferenceReason,
			}

			profileResponse := &profileModel.ProfileResponse{
				ProfileID:          profile.ProfileID,
				UserID:             masterProfile.UserID,
				ApplicationData:    ConvertAppDataToMap(masterProfile.ApplicationData),
				Traits:             masterProfile.Traits,
				IdentityAttributes: masterProfile.IdentityAttributes,
				Meta: profileModel.Meta{
					CreatedAt: masterProfile.CreatedAt,
					UpdatedAt: masterProfile.UpdatedAt,
					Location:  utils.BuildProfileLocation(ctx, orgHandle, masterProfile.ProfileID),
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
	profileID string, orgID string) ([]profileModel.ConsentRecord, error) {
	profile, err := profileStore.GetProfile(ctx, profileID, orgID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, profileNotFoundError()
	}
	consentRecords, err := profileStore.GetProfileConsents(ctx, profileID, orgID)
	if err != nil {
		return nil, err
	}
	if consentRecords == nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ProfileNotFound.Code,
			Message:     errors2.ProfileNotFound.Message,
			Description: errors2.ProfileNotFound.Description,
		}, http.StatusNotFound)
		return nil, clientError
	}

	return consentRecords, nil
}

// UpdateProfileConsents updates the consent records for a profile
func (ps *ProfilesService) UpdateProfileConsents(ctx context.Context,
	profileID string, orgID string, consents []profileModel.ConsentRecord) error {
	logger := log.GetLogger()

	profile, err := profileStore.GetProfile(ctx, profileID, orgID)
	if err != nil {
		return err
	}
	if profile == nil {
		return profileNotFoundError()
	}

	// Reject any attempt to modify a mandatory consent category.
	mandatoryIDs, err := consentStore.GetMandatoryConsentCategoryIDs(ctx, identitysource.Of(ctx, orgID))
	if err != nil {
		return err
	}
	mandatorySet := make(map[string]bool, len(mandatoryIDs))
	for _, id := range mandatoryIDs {
		mandatorySet[id] = true
	}
	for _, c := range consents {
		if mandatorySet[c.CategoryIdentifier] {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:    errors2.ConsentCatMandatory.Code,
				Message: errors2.ConsentCatMandatory.Message,
				Description: fmt.Sprintf(
					"Consent for mandatory category '%s' cannot be modified.",
					c.CategoryIdentifier,
				),
			}, http.StatusForbidden)
		}
	}

	// Reject categories that are not defined in the org.
	orgCategorySet, err := consentCategoryIDSet(ctx, orgID)
	if err != nil {
		return err
	}
	for _, c := range consents {
		if !orgCategorySet[c.CategoryIdentifier] {
			return errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.ConsentCatValidation.Code,
				Message:     errors2.ConsentCatValidation.Message,
				Description: fmt.Sprintf("Consent category '%s' not found.", c.CategoryIdentifier),
			}, http.StatusBadRequest)
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
	err = profileStore.UpdateProfileConsents(ctx, profileID, consents)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to update consents for profile: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		return err
	}

	return nil
}

// DeleteProfile removes a profile from MongoDB by `perma_id`
func (ps *ProfilesService) DeleteProfile(ctx context.Context, profileID string, orgID string) error {
	// Fetch the existing profile before deletion
	profile, err := profileStore.GetProfile(ctx, profileID, orgID)
	logger := log.GetLogger()
	if err == nil && profile == nil {
		logger.Debug(fmt.Sprintf("Profile with profile_id: %s that is requested for deletion is not found",
			profileID))
		return nil
	}

	// Drop the resolution index entries first. They are not reached by any cascade, so a
	// profile left indexed keeps surfacing as a merge candidate after deletion.
	if err := irStore.DeleteBlockingKeys(ctx, orgID, profileID); err != nil {
		logger.Warn(fmt.Sprintf("DeleteProfile: failed to remove blocking keys for '%s'", profileID),
			log.Error(err))
	}
	if err := irStore.DeleteRejectionPairsForProfile(ctx, orgID, profileID); err != nil {
		logger.Warn(fmt.Sprintf("DeleteProfile: failed to remove rejection pairs for '%s'", profileID),
			log.Error(err))
	}
	if err != nil {
		errorMsg := fmt.Sprintf("Error deleting profile with profile_id: %s", profileID)
		logger.Debug(errorMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DeleteProfile.Code,
			Message:     errors2.DeleteProfile.Message,
			Description: errorMsg,
		}, err)
		return serverError
	}

	if profile.ProfileStatus.IsReferenceProfile {
		// fetching the child if its parent
		profile.ProfileStatus.References, _ = profileStore.FetchReferencedProfiles(ctx, profile.ProfileID)
	}

	if profile.ProfileStatus.IsReferenceProfile && len(profile.ProfileStatus.References) == 0 {
		logger.Info(fmt.Sprintf("Deleting parent profile: %s with no children", profileID))
		// Delete the parent with no children
		err = profileStore.DeleteProfile(ctx, profileID)
		if err != nil {
			errorMsg := fmt.Sprintf(
				"Error deleting profile with profile_id: %s which is a parent and no children",
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
		return nil
	}

	if profile.ProfileStatus.IsReferenceProfile && len(profile.ProfileStatus.References) > 0 {
		// get all child profiles and delete
		for _, childProfile := range profile.ProfileStatus.References {
			err = profileStore.DeleteProfile(ctx, childProfile.ProfileID)
			logger.Info(fmt.Sprintf("Deleting child  profile: %s with of parent: %s",
				childProfile.ProfileID, profileID))

			if err != nil {
				errorMsg := fmt.Sprintf("Error while deleting profile with profile_id: %s ", childProfile.ProfileID)
				logger.Debug(errorMsg, log.Error(err))
				serverError := errors2.NewServerError(errors2.ErrorMessage{
					Code:        errors2.DeleteProfile.Code,
					Message:     errors2.DeleteProfile.Message,
					Description: errorMsg,
				}, err)
				return serverError
			}
		}
		// now delete master
		err = profileStore.DeleteProfile(ctx, profileID)
		logger.Info(fmt.Sprintf("Deleting parent profile: %s with children", profileID))
		if err != nil {
			errorMsg := fmt.Sprintf("Error while deleting parent profile: %s ", profileID)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.DeleteProfile.Code,
				Message:     errors2.DeleteProfile.Message,
				Description: errorMsg,
			}, err)
			return serverError
		}
		return nil
	}

	// If it is a child profile, delete it.
	//
	// A standalone profile also reports IsReferenceProfile false, but has no parent to
	// look up. Reading its empty ReferenceProfileID as an id returns no profile, and
	// GetProfile signals that as (nil, nil) — so without this guard the nil parent is
	// dereferenced and deleting any never-merged profile panics.
	if !profile.ProfileStatus.IsReferenceProfile && profile.ProfileStatus.ReferenceProfileID != "" {
		logger.Info(fmt.Sprintf("Deleting child profile: %s with parent: %s", profileID,
			profile.ProfileStatus.ReferenceProfileID))
		parentProfile, err := profileStore.GetProfile(ctx, profile.ProfileStatus.ReferenceProfileID, orgID)
		if err != nil {
			errorMsg := fmt.Sprintf("Error while deleting the child profile: %s ", profileID)
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.DeleteProfile.Code,
				Message:     errors2.DeleteProfile.Message,
				Description: errorMsg,
			}, err)
			return serverError
		}
		parentProfile.ProfileStatus.References, _ = profileStore.FetchReferencedProfiles(ctx, parentProfile.ProfileID)

		if len(parentProfile.ProfileStatus.References) == 1 {
			// delete the parent as this is the only child
			logger.Info(fmt.Sprintf("Deleting parent profile: %s with of current : %s",
				profile.ProfileStatus.ReferenceProfileID, profileID))
			err = profileStore.DeleteProfile(ctx, profile.ProfileStatus.ReferenceProfileID)
			if err != nil {
				errorMsg := fmt.Sprintf("Error while deleting the master profile: %s ", profileID)
				logger.Debug(errorMsg, log.Error(err))
				serverError := errors2.NewServerError(errors2.ErrorMessage{
					Code:        errors2.DeleteProfile.Code,
					Message:     errors2.DeleteProfile.Message,
					Description: errorMsg,
				}, err)
				return serverError
			}
			// todo: Ensure the need to detach the referer profile from the reference
			// err = profileStore.DetachRefererProfileFromReference(profile.ProfileStatus.ReferenceProfileID, profileID)
			// todo: Ensure the need to detach the referer profile from the reference
			// err = profileStore.DetachRefererProfileFromReference(profile.ProfileStatus.ReferenceProfileId, ProfileId)
			err = profileStore.DeleteProfile(ctx, profileID)
			if err != nil {
				errorMsg := fmt.Sprintf("Error while deleting the  profile: %s ", profileID)
				logger.Debug(errorMsg, log.Error(err))
				serverError := errors2.NewServerError(errors2.ErrorMessage{
					Code:        errors2.DeleteProfile.Code,
					Message:     errors2.DeleteProfile.Message,
					Description: errorMsg,
				}, err)
				return serverError
			}
			logger.Info(fmt.Sprintf("Deleted current profile: %s with parent: %s", profileID,
				profile.ProfileStatus.ReferenceProfileID))
		} else {
			err = profileStore.DetachRefererProfileFromReference(ctx, profile.ProfileStatus.ReferenceProfileID,
				profileID)
			if err != nil {
				errorMsg := fmt.Sprintf("Error while current profile from parent: %s ", profileID)
				logger.Debug(errorMsg, log.Error(err))
				serverError := errors2.NewServerError(errors2.ErrorMessage{
					Code:        errors2.DeleteProfile.Code,
					Message:     errors2.DeleteProfile.Message,
					Description: errorMsg,
				}, err)
				return serverError
			}
			logger.Debug(fmt.Sprintf("Detaching current profile: %s from parent: %s", profileID,
				profile.ProfileStatus.ReferenceProfileID))
			err = profileStore.DeleteProfile(ctx, profileID)
			if err != nil {
				errorMsg := fmt.Sprintf("Error while deleting the current profile: %s ", profileID)
				logger.Debug(errorMsg, log.Error(err))
				serverError := errors2.NewServerError(errors2.ErrorMessage{
					Code:        errors2.DeleteProfile.Code,
					Message:     errors2.DeleteProfile.Message,
					Description: errorMsg,
				}, err)
				return serverError
			}
			logger.Info(fmt.Sprintf("Deleted current profile: %s with parent: %s",
				profileID, profile.ProfileStatus.ReferenceProfileID))
		}
	}

	return nil
}

// GetAllProfilesCursor retrieves all master profiles with pagination using cursor.
// Merged profiles are not included in list but provided in the reference
func (ps *ProfilesService) GetAllProfilesCursor(ctx context.Context,
	orgID string,
	limit int,
	cursor *profileModel.ProfileCursor,
) ([]profileModel.ProfileResponse, bool, error) {
	existingProfiles, hasMore, err := profileStore.GetAllProfiles(ctx, orgID, limit, cursor)
	if err != nil {
		return nil, false, err
	}
	if existingProfiles == nil {
		return []profileModel.ProfileResponse{}, false, nil
	}
	orgHandle, err := orgService.GetOrganizationHandle(ctx, orgID)
	if err != nil {
		return nil, false, err
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
			Location:  utils.BuildProfileLocation(ctx, orgHandle, profile.ProfileID),
		}

		alias, err := profileStore.FetchReferencedProfiles(ctx, profile.ProfileID)
		if err != nil {
			errorMsg := fmt.Sprintf("Error fetching references for profile: %s", profile.ProfileID)
			logger := log.GetLogger()
			logger.Debug(errorMsg, log.Error(err))
			serverError := errors2.NewServerError(errors2.ErrorMessage{
				Code:        errors2.GetProfile.Code,
				Message:     errors2.GetProfile.Message,
				Description: errorMsg,
			}, err)
			return nil, false, serverError
		}
		if len(alias) == 0 {
			alias = nil
		}

		if profile.ProfileStatus.IsReferenceProfile {
			result = append(result, profileModel.ProfileResponse{
				ProfileID:          profile.ProfileID,
				UserID:             profile.UserID,
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
	orgID string,
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
				Code:        errors2.FilterProfile.Code,
				Message:     errors2.FilterProfile.Message,
				Description: "Invalid filter format when filtering profiles.",
			}, http.StatusBadRequest)
		}

		field, operator, rawValue := parts[0], parts[1], parts[2]

		// Validate operator
		switch operator {
		case "eq", "co", "sw":
		default:
			return nil, false, errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.FilterProfile.Code,
				Message:     errors2.FilterProfile.Message,
				Description: fmt.Sprintf("Unsupported operator: %s", operator),
			}, http.StatusBadRequest)
		}

		// Validate field/key
		if field != fieldUserID && field != fieldProfileID {
			if !isValidFilterKey(field) {
				return nil, false, errors2.NewClientError(errors2.ErrorMessage{
					Code:        errors2.FilterProfile.Code,
					Message:     errors2.FilterProfile.Message,
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
	filteredProfiles, hasMore, err := profileStore.GetAllProfilesWithFilter(ctx, orgID, rewrittenFilters, limit, cursor)
	if err != nil {
		return nil, false, err
	}
	if filteredProfiles == nil {
		return []profileModel.ProfileResponse{}, false, nil
	}
	orgHandle, err := orgService.GetOrganizationHandle(ctx, orgID)
	if err != nil {
		return nil, false, err
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
			Location:  utils.BuildProfileLocation(ctx, orgHandle, profile.ProfileID),
		}

		alias, err := profileStore.FetchReferencedProfiles(ctx, profile.ProfileID)
		if err != nil {
			return nil, false, err
		}
		if profile.ProfileStatus.IsReferenceProfile {
			result = append(result, profileModel.ProfileResponse{
				ProfileID:          profile.ProfileID,
				UserID:             profile.UserID,
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

// FindProfileByUserID retrieves a profile by user_id
func (ps *ProfilesService) FindProfileByUserID(ctx context.Context,
	userID string, orgID string) (*profileModel.ProfileResponse, error) {
	profile, err := profileStore.GetProfileWithUserID(ctx, userID, orgID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, profileNotFoundError()
	}
	orgHandle, err := orgService.GetOrganizationHandle(ctx, orgID)
	if err != nil {
		return nil, err
	}

	alias, err := profileStore.FetchReferencedProfiles(ctx, profile.ProfileID)

	if err != nil {
		return nil, err
	}
	if len(alias) == 0 {
		alias = nil
	}
	profileResponse := &profileModel.ProfileResponse{
		ProfileID:          profile.ProfileID,
		UserID:             profile.UserID,
		ApplicationData:    ConvertAppDataToMap(profile.ApplicationData),
		Traits:             profile.Traits,
		IdentityAttributes: profile.IdentityAttributes,
		Meta: profileModel.Meta{
			CreatedAt: profile.CreatedAt,
			UpdatedAt: profile.UpdatedAt,
			Location:  utils.BuildProfileLocation(ctx, orgHandle, profile.ProfileID),
		},
		MergedFrom: alias,
	}
	return profileResponse, nil
}

// PatchProfile applies a partial update to an existing profile
func (ps *ProfilesService) PatchProfile(ctx context.Context,
	profileID string, orgID string, patch map[string]interface{}) (*profileModel.ProfileResponse, error) {
	existingProfile, err := profileStore.GetProfile(ctx, profileID, orgID)
	if err != nil {
		return nil, err
	}
	if existingProfile == nil {
		return nil, errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ProfileNotFound.Code,
			Message:     errors2.ProfileNotFound.Message,
			Description: fmt.Sprintf("Profile %s not found", profileID),
		}, http.StatusNotFound)
	}

	// For a child profile (merged into a master), the patch must be applied to the
	// master's current data. Using the child's raw stored document as the base would
	// cause UpdateProfile to overwrite the master with incomplete data, losing all
	// accumulated merged data that only exists on the master.
	profileForBase := existingProfile
	if existingProfile.ProfileStatus != nil && !existingProfile.ProfileStatus.IsReferenceProfile &&
		existingProfile.ProfileStatus.ReferenceProfileID != "" {
		masterProfile, err := profileStore.GetProfile(ctx, existingProfile.ProfileStatus.ReferenceProfileID, orgID)
		if err != nil {
			return nil, err
		}
		if masterProfile != nil {
			masterProfile.ApplicationData, err = profileStore.FetchApplicationData(ctx, masterProfile.ProfileID)
			if err != nil {
				return nil, err
			}
			profileForBase = masterProfile
		} else {
			logger := log.GetLogger()
			logger.Warn(fmt.Sprintf("Master profile %s not found for child profile %s. Using the child profile",
				existingProfile.ProfileStatus.ReferenceProfileID, profileID))
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

	// Now apply top-level scalar fields. The user ID cannot be changed through a patch, so the stored one is kept.
	for k, v := range patch {
		if k == "traits" || k == "identity_attributes" || k == "application_data" || k == "user_id" {
			continue
		}
		merged[k] = v
	}
	// Convert merged data back to ProfileRequest
	mergedBytes, _ := json.Marshal(merged)
	var updatedProfileReq profileModel.ProfileRequest
	if err := json.Unmarshal(mergedBytes, &updatedProfileReq); err != nil {
		logger := log.GetLogger()
		errMsg := fmt.Sprintf("Error unmarshalling merged profile data for profile_id: %s", profileID)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateProfile.Code,
			Message:     errors2.UpdateProfile.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}

	// Reuse the PUT logic to update the profile, with the profile read above.
	return ps.updateProfile(ctx, profileID, orgID, existingProfile, updatedProfileReq)
}

// GetProfileCookieByProfileID returns the cookie of a profile, or a not-found client error if it has none.
func (ps *ProfilesService) GetProfileCookieByProfileID(ctx context.Context,
	profileID string) (*profileModel.ProfileCookie, error) {
	cookie, err := profileStore.GetProfileCookieByProfileID(ctx, profileID)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("Error fetching profile cookie by profile_id: %s", profileID)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfileCookie.Code,
			Message:     errors2.GetProfileCookie.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}
	if cookie == nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ProfileCookieNotFound.Code,
			Message:     errors2.ProfileCookieNotFound.Message,
			Description: fmt.Sprintf("Profile cookie for profile_id %s not found", profileID),
		}, http.StatusNotFound)
		return nil, clientError
	}
	return cookie, nil
}

// GetProfileCookieByID returns the profile cookie with the given cookie ID in the organization, or a
// not-found client error.
func (ps *ProfilesService) GetProfileCookieByID(ctx context.Context,
	cookie string, orgID string) (*profileModel.ProfileCookie, error) {
	cookieObj, err := profileStore.GetProfileCookie(ctx, cookie, orgID)
	logger := log.GetLogger()
	if err != nil {
		errMsg := "Error fetching a profile cookie."
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetProfileCookie.Code,
			Message:     errors2.GetProfileCookie.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}
	if cookieObj == nil {
		clientError := errors2.NewClientError(errors2.ErrorMessage{
			Code:        errors2.ProfileCookieNotFound.Code,
			Message:     errors2.ProfileCookieNotFound.Message,
			Description: "Profile cookie not found.",
		}, http.StatusNotFound)
		return nil, clientError
	}
	return cookieObj, nil
}

// CreateProfileCookie creates a new profile cookie
func (ps *ProfilesService) CreateProfileCookie(ctx context.Context,
	profileID string) (*profileModel.ProfileCookie, error) {
	cookie := profileModel.ProfileCookie{
		ProfileID: profileID,
		CookieID:  uuid.New().String(),
		IsActive:  true,
	}
	err := profileStore.CreateProfileCookie(ctx, cookie)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("Error creating profile cookie by profile_id: %s", cookie.ProfileID)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.GetCookie.Code,
			Message:     errors2.GetCookie.Message,
			Description: errMsg,
		}, err)
		return nil, serverError
	}
	return &cookie, nil
}

// UpdateCookieStatusByProfileID updates the status of a profile cookie by profile_id
func (ps *ProfilesService) UpdateCookieStatusByProfileID(ctx context.Context, profileID string, isActive bool) error {
	err := profileStore.UpdateProfileCookieByProfileID(ctx, profileID, isActive)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("Error updating profile cookie by profile_id: %s", profileID)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateCookie.Code,
			Message:     errors2.UpdateCookie.Message,
			Description: errMsg,
		}, err)
		return serverError
	}
	return nil
}

// UpdateCookieStatusByCookieID updates the status of a profile cookie by cookie id
func (ps *ProfilesService) UpdateCookieStatusByCookieID(ctx context.Context, cookieID string, isActive bool) error {
	err := profileStore.UpdateProfileCookieByCookieID(ctx, cookieID, isActive)
	logger := log.GetLogger()
	if err != nil {
		errMsg := "Error updating a profile cookie."
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.UpdateCookie.Code,
			Message:     errors2.UpdateCookie.Message,
			Description: errMsg,
		}, err)
		return serverError
	}
	return nil
}

// DeleteCookieByProfileID deletes a profile cookie by profile_id
func (ps *ProfilesService) DeleteCookieByProfileID(ctx context.Context, profileID string) error {
	err := profileStore.DeleteProfileCookieByProfile(ctx, profileID)
	logger := log.GetLogger()
	if err != nil {
		errMsg := fmt.Sprintf("Error deleting profile cookie by profile_id: %s", profileID)
		logger.Debug(errMsg, log.Error(err))
		serverError := errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.DeleteCookie.Code,
			Message:     errors2.DeleteCookie.Message,
			Description: errMsg,
		}, err)
		return serverError
	}
	return nil
}

func profileNotFoundError() error {
	return errors2.NewClientError(errors2.ErrorMessage{
		Code:        errors2.ProfileNotFound.Code,
		Message:     errors2.ProfileNotFound.Message,
		Description: errors2.ProfileNotFound.Description,
	}, http.StatusNotFound)
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

// GetProfilesWithFuzzyResolution uses the identity resolution engine to find
// profiles that fuzzy-match the attributes extracted from query filters.
// It returns profiles scored above the given threshold, sorted by score descending.
func (ps *ProfilesService) GetProfilesWithFuzzyResolution(
	ctx context.Context,
	orgID string,
	filters []string,
	threshold float64,
	limit int,
) ([]profileModel.FuzzyMatchResult, error) {
	logger := log.GetLogger()

	flatAttrs := parseFuzzyFiltersToFlatAttrs(filters)
	if len(flatAttrs) == 0 {
		logger.Warn("Service: no searchable attributes found in filters for fuzzy resolution")
		return []profileModel.FuzzyMatchResult{}, nil
	}

	rawRules, err := urService.GetUnificationRuleService().GetUnificationRules(ctx, orgID)
	if err != nil {
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRSearchFailed.Code,
			Message:     errors2.IRSearchFailed.Message,
			Description: fmt.Sprintf("Failed to load unification rules for org: %s", orgID),
		}, err)
	}
	rules := filterActiveRules(rawRules)
	if len(rules) == 0 {
		logger.Warn("Service: no active unification rules, fuzzy resolution returning empty",
			log.String("orgId", orgID))
		return []profileModel.FuzzyMatchResult{}, nil
	}

	blockingKeys := engine.GenerateBlockingKeysFromRules(flatAttrs, rules)
	candidateIDs := engine.FindCandidatesByIndex(blockingKeys, orgID, "",
		func(keyOrgId, attr string, values []string, exclude string, max int) ([]string, error) {
			return irStore.FindCandidateIDsByKeys(ctx, keyOrgId, attr, values, exclude, max)
		})
	if len(candidateIDs) == 0 {
		return []profileModel.FuzzyMatchResult{}, nil
	}

	candidateIDs, profileMap, err := resolveAndLoadCandidates(ctx, candidateIDs)
	if err != nil {
		return nil, err
	}

	results, err := scoreAndBuildFuzzyResults(ctx, candidateIDs, profileMap, flatAttrs, rules, threshold, limit, orgID)
	if err != nil {
		return nil, err
	}

	return results, nil
}

// GetProfilesHybrid combines fuzzy matching (blocking + scoring) with deterministic SQL
// filtering. A result must appear in BOTH the fuzzy blocking candidate set AND the
// deterministic SQL result set i.e. it is similar to the fuzzy values AND exactly
// satisfies the deterministic conditions.
func (ps *ProfilesService) GetProfilesHybrid(
	ctx context.Context,
	orgID string,
	fuzzyFilters []string,
	deterministicFilters []string,
	threshold float64,
	limit int,
) ([]profileModel.FuzzyMatchResult, error) {
	logger := log.GetLogger()

	for _, f := range deterministicFilters {
		parts := strings.SplitN(f, " ", 3)
		if len(parts) != 3 {
			continue
		}
		field := parts[0]
		if field != fieldUserID && field != fieldProfileID && !isValidFilterKey(field) {
			return nil, errors2.NewClientError(errors2.ErrorMessage{
				Code:        errors2.FilterProfile.Code,
				Message:     errors2.FilterProfile.Message,
				Description: "Invalid filter key: " + field,
			}, http.StatusBadRequest)
		}
	}

	// Step 1: Parse fuzzy filters into flat attributes for the IR engine.
	flatAttrs := parseFuzzyFiltersToFlatAttrs(fuzzyFilters)
	if len(flatAttrs) == 0 {
		logger.Warn("Service: no searchable attributes in fuzzy filters for hybrid search")
		return []profileModel.FuzzyMatchResult{}, nil
	}

	// Step 2: Load unification rules.
	rawRules, err := urService.GetUnificationRuleService().GetUnificationRules(ctx, orgID)
	if err != nil {
		return nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRSearchFailed.Code,
			Message:     errors2.IRSearchFailed.Message,
			Description: fmt.Sprintf("Failed to load unification rules for org: %s", orgID),
		}, err)
	}
	rules := filterActiveRules(rawRules)
	if len(rules) == 0 {
		logger.Warn("Service: no active unification rules, hybrid search returning empty")
		return []profileModel.FuzzyMatchResult{}, nil
	}

	// Step 3: Get fuzzy candidates via blocking.
	blockingKeys := engine.GenerateBlockingKeysFromRules(flatAttrs, rules)
	fuzzyIDs := engine.FindCandidatesByIndex(blockingKeys, orgID, "",
		func(keyOrgId, attr string, values []string, exclude string, max int) ([]string, error) {
			return irStore.FindCandidateIDsByKeys(ctx, keyOrgId, attr, values, exclude, max)
		})
	if len(fuzzyIDs) == 0 {
		return []profileModel.FuzzyMatchResult{}, nil
	}

	// Step 4: Get deterministic candidates via SQL.
	// Step 4/5: the deterministic filter is applied to the fuzzy candidates rather than to
	// the whole organization, so the database performs the intersection and returns only
	// what survives both paths.
	candidateIDs, err := profileStore.GetProfileIDsWithFilters(ctx, orgID, deterministicFilters, fuzzyIDs)
	if err != nil {
		return nil, err
	}
	if len(candidateIDs) == 0 {
		return []profileModel.FuzzyMatchResult{}, nil
	}

	// Step 6: Resolve children to master profiles and load profiles.
	candidateIDs, profileMap, err := resolveAndLoadCandidates(ctx, candidateIDs)
	if err != nil {
		return nil, err
	}

	// Step 7: Score, threshold, sort, and build results.
	results, err := scoreAndBuildFuzzyResults(ctx, candidateIDs, profileMap, flatAttrs, rules, threshold, limit, orgID)
	if err != nil {
		return nil, err
	}

	return results, nil
}

// parseFuzzyFiltersToFlatAttrs extracts identity_attributes, traits, and user_id values
// from fuzzy filter strings and returns a flat map keyed by fully-qualified attribute name.
// The operator is ignored — fuzzy matching uses only the value.
func parseFuzzyFiltersToFlatAttrs(filters []string) map[string]interface{} {
	identityAttrs := make(map[string]interface{})
	traits := make(map[string]interface{})
	var userID string

	for _, f := range filters {
		parts := strings.SplitN(f, " ", 3)
		if len(parts) != 3 {
			continue
		}
		field := parts[0]
		value := parts[2]

		if field == fieldUserID {
			userID = value
			continue
		}

		dotIdx := strings.Index(field, ".")
		if dotIdx < 0 {
			continue
		}
		scope := field[:dotIdx]
		key := field[dotIdx+1:]

		switch scope {
		case "identity_attributes":
			identityAttrs[key] = value
		case "traits":
			traits[key] = value
		}
	}

	flatAttrs := make(map[string]interface{})
	irModel.FlattenMap("identity_attributes", identityAttrs, flatAttrs)
	irModel.FlattenMap("traits", traits, flatAttrs)
	if userID != "" {
		flatAttrs[fieldUserID] = userID
	}
	return flatAttrs
}

// resolveAndLoadCandidates loads the given profile IDs into a map and resolves any child
// profile IDs to their master reference profile ID. Returns the deduplicated master IDs
// and the profile map (keyed by profile ID).
func resolveAndLoadCandidates(
	ctx context.Context,
	candidateIDs []string,
) ([]string, map[string]*irModel.ProfileData, error) {
	logger := log.GetLogger()

	candidateProfiles, err := irStore.GetProfilesByIDs(ctx, candidateIDs)
	if err != nil {
		return nil, nil, errors2.NewServerError(errors2.ErrorMessage{
			Code:        errors2.IRSearchFailed.Code,
			Message:     errors2.IRSearchFailed.Message,
			Description: "Failed to load candidate profiles.",
		}, err)
	}

	profileMap := make(map[string]*irModel.ProfileData, len(candidateProfiles))
	for i := range candidateProfiles {
		profileMap[candidateProfiles[i].ProfileID] = &candidateProfiles[i]
	}

	resolvedIDs := make([]string, 0, len(candidateIDs))
	seen := make(map[string]bool)
	for _, cid := range candidateIDs {
		candidate, exists := profileMap[cid]
		if !exists {
			continue
		}
		resolvedID := cid
		if candidate.IsChild() {
			masterID := candidate.ReferenceProfileID
			resolvedID = masterID
			if _, ok := profileMap[masterID]; !ok {
				masterProfiles, loadErr := irStore.GetProfilesByIDs(ctx, []string{masterID})
				if loadErr != nil || len(masterProfiles) == 0 {
					logger.Warn(
						fmt.Sprintf("resolveAndLoadCandidates: could not load master '%s' for child '%s', skipping",
							masterID, cid),
					)
					continue
				}
				profileMap[masterID] = &masterProfiles[0]
			}
		}
		if !seen[resolvedID] {
			seen[resolvedID] = true
			resolvedIDs = append(resolvedIDs, resolvedID)
		}
	}
	return resolvedIDs, profileMap, nil
}

// scoreAndBuildFuzzyResults scores the given candidates against flatAttrs using the provided
// rules, filters by threshold, sorts by score descending, applies limit, and builds the
// final FuzzyMatchResult slice.
func scoreAndBuildFuzzyResults(ctx context.Context,
	candidateIDs []string,
	profileMap map[string]*irModel.ProfileData,
	flatAttrs map[string]interface{},
	rules []UnificationModel.UnificationRule,
	threshold float64,
	limit int,
	orgID string,
) ([]profileModel.FuzzyMatchResult, error) {
	logger := log.GetLogger()
	orgHandle, err := orgService.GetOrganizationHandle(ctx, orgID)
	if err != nil {
		return nil, err
	}

	thresholds := irModel.LoadThresholds(ctx, orgID)
	scoringCtx := engine.ScoringContext{
		OrgID:      orgID,
		Thresholds: thresholds,
		ValueFrequency: func(keyOrgId, attr, keyValue string) (int, error) {
			return irStore.CountProfilesByBlockingKey(ctx, keyOrgId, attr, keyValue)
		},
	}
	if threshold <= 0 {
		threshold = thresholds.ManualReview
	}
	if threshold > 1.0 {
		threshold = 1.0
	}

	type scoredMatch struct {
		candidateID    string
		similarity     float64
		scoreBreakdown map[string]float64
	}

	var scoredMatches []scoredMatch
	for _, candidateID := range candidateIDs {
		candidate, exists := profileMap[candidateID]
		if !exists {
			continue
		}
		// A search asks how alike profiles are, so it ranks by similarity. The capped score
		// answers a different question — whether to merge unattended — and would rank a pair
		// held back by a disagreeing rule above a closer one that was not.
		evaluation := engine.EvaluateCandidate(flatAttrs, candidate, rules, scoringCtx)
		if evaluation.Similarity >= threshold {
			scoredMatches = append(scoredMatches, scoredMatch{
				candidateID:    candidateID,
				similarity:     evaluation.Similarity,
				scoreBreakdown: evaluation.Breakdown,
			})
		}
	}

	sort.Slice(scoredMatches, func(i, j int) bool {
		return scoredMatches[i].similarity > scoredMatches[j].similarity
	})
	if limit > 0 && len(scoredMatches) > limit {
		scoredMatches = scoredMatches[:limit]
	}

	results := make([]profileModel.FuzzyMatchResult, 0, len(scoredMatches))
	for _, sm := range scoredMatches {
		// GetProfile reports "no such profile" as (nil, nil), so the nil profile has to be
		// checked separately from the error — a candidate can disappear between the
		// blocking lookup and this read.
		profile, err := profileStore.GetProfile(ctx, sm.candidateID, orgID)
		if err != nil || profile == nil {
			logger.Warn(fmt.Sprintf("scoreAndBuildFuzzyResults: could not load profile '%s', skipping", sm.candidateID))
			continue
		}

		baseMeta := profileModel.Meta{
			CreatedAt: profile.CreatedAt,
			UpdatedAt: profile.UpdatedAt,
			Location:  utils.BuildProfileLocation(ctx, orgHandle, profile.ProfileID),
		}
		alias, _ := profileStore.FetchReferencedProfiles(ctx, profile.ProfileID)

		profileResp := profileModel.ProfileResponse{
			ProfileID:          profile.ProfileID,
			UserID:             profile.UserID,
			ApplicationData:    ConvertAppDataToMap(profile.ApplicationData),
			Traits:             profile.Traits,
			IdentityAttributes: profile.IdentityAttributes,
			Meta:               baseMeta,
		}
		if profile.ProfileStatus.IsReferenceProfile {
			profileResp.MergedFrom = alias
		}

		results = append(results, profileModel.FuzzyMatchResult{
			Profile:        profileResp,
			MatchScore:     sm.similarity,
			ScoreBreakdown: sm.scoreBreakdown,
		})
	}
	return results, nil
}

// flattenProfileAttrs builds a flat map of a profile's attributes keyed by their
// fully-qualified property name (e.g. "identity_attributes.email") so we can
// quickly check whether the profile has a value for a given unification rule.
func flattenProfileAttrs(p profileModel.Profile) map[string]interface{} {
	flat := make(map[string]interface{})
	for k, v := range p.IdentityAttributes {
		flat["identity_attributes."+k] = v
	}
	for k, v := range p.Traits {
		flat["traits."+k] = v
	}
	if p.UserID != "" {
		flat[fieldUserID] = p.UserID
	}
	return flat
}

// filterActiveRules returns the active rules in evaluation order for the synchronous search
// path, with tolerant matching removed where the server has it switched off.
//
// It delegates rather than sorting and defaulting again: this used to be a second copy of
// ActiveSortedByPriority, and a copy is how the search path came to keep matching tolerantly
// after the server-wide switch was added to the worker. One implementation is what stops the
// two disagreeing about what a rule means.
func filterActiveRules(rules []UnificationModel.UnificationRule) []UnificationModel.UnificationRule {
	active := UnificationModel.ActiveSortedByPriority(rules, constants.AttributeTypePrimitiveExact,
		constants.UnificationMethodDeterministic, constants.DefaultMatchStrength,
		constants.DefaultMismatchStrength)

	if !config.GetCDSRuntime().Config.IdentityResolution.FuzzyMatchingEnabled {
		active = UnificationModel.AsExactMatching(active)
	}

	return active
}

// hasAttributeMatchingAnyRule reports whether the profile carries a value for any active
// unification rule. Used to skip enqueuing profiles that no rule could ever match.
func hasAttributeMatchingAnyRule(flatAttrs map[string]interface{}, rules []UnificationModel.UnificationRule) bool {
	for _, rule := range rules {
		if !rule.IsActive {
			continue
		}
		if value, ok := flatAttrs[rule.PropertyName]; ok && value != nil {
			return true
		}
	}
	return false
}

// ruleValuesChanged reports whether any value a unification rule matches on differs
// between two versions of a profile. Values are compared after flattening so nested
// attributes are handled, and order within a multi-valued attribute is ignored.
func ruleValuesChanged(before, after map[string]interface{}, rules []UnificationModel.UnificationRule) bool {
	for _, rule := range rules {
		if !rule.IsActive {
			continue
		}
		if !sameAttributeValues(before[rule.PropertyName], after[rule.PropertyName]) {
			return true
		}
	}
	return false
}

func sameAttributeValues(before, after interface{}) bool {
	beforeValues := toComparableStrings(before)
	afterValues := toComparableStrings(after)
	if len(beforeValues) != len(afterValues) {
		return false
	}

	sort.Strings(beforeValues)
	sort.Strings(afterValues)
	for i := range beforeValues {
		if beforeValues[i] != afterValues[i] {
			return false
		}
	}
	return true
}

func toComparableStrings(value interface{}) []string {
	if value == nil {
		return nil
	}
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []string:
		return typed
	case []interface{}:
		values := make([]string, 0, len(typed))
		for _, element := range typed {
			values = append(values, fmt.Sprintf("%v", element))
		}
		return values
	default:
		return []string{fmt.Sprintf("%v", value)}
	}
}

// shouldResolveAfterUpdate reports whether an updated profile needs re-evaluating for
// merges.
//
// Resolution is expensive — it rewrites the profile's whole blocking-key set and runs a
// candidate query per key group — and an update that left every matched value untouched
// would reach exactly the conclusion the previous write already reached. Gating on "does
// this profile have matchable data" rather than "did that data change" meant any edit to
// any attribute paid that cost, which on a profile carrying a userId was every edit.
//
// A changed userId still forces re-evaluation on its own: merging profiles that share a
// userId is a system invariant that holds with no rules configured, so a profile that has
// just gained or changed one must be re-examined even if no rule value moved.
func shouldResolveAfterUpdate(before, after profileModel.Profile, matchedValuesChanged bool,
	activeRules []UnificationModel.UnificationRule) bool {
	if before.UserID != after.UserID {
		return true
	}

	// A rule created or edited since this profile was last written has never been applied
	// to it: the profile carries no blocking keys for that attribute, so nothing can match
	// it until it is processed again. Skipping here on the grounds that no matched value
	// changed would leave the profile invisible to the new rule until one of its values
	// happens to change — which for a dormant profile may be never.
	//
	// The backfill that runs when a rule is activated covers this too. This is the second
	// chance for the profiles it missed, and it costs nothing: the rules are already loaded.
	if anyRuleNewerThan(before.UpdatedAt, activeRules) {
		return true
	}

	if !matchedValuesChanged {
		return false
	}
	return after.UserID != "" || hasAttributeMatchingAnyRule(flattenProfileAttrs(after), activeRules)
}

// anyRuleNewerThan reports whether an active rule changed after the given time.
func anyRuleNewerThan(profileUpdatedAt time.Time, activeRules []UnificationModel.UnificationRule) bool {
	for _, rule := range activeRules {
		if !rule.IsActive {
			continue
		}
		if rule.UpdatedAt.After(profileUpdatedAt) {
			return true
		}
	}
	return false
}
