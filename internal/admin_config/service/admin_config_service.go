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
	"fmt"
	"math"
	"net/http"

	"github.com/wso2/identity-customer-data-service/internal/admin_config/model"
	"github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	appProvider "github.com/wso2/identity-customer-data-service/internal/application/provider"
	consentService "github.com/wso2/identity-customer-data-service/internal/consent/service"
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	"github.com/wso2/identity-customer-data-service/internal/organization/suborg"
	"github.com/wso2/identity-customer-data-service/internal/profile_schema/service"
	sharingService "github.com/wso2/identity-customer-data-service/internal/sharing/service"
	sysconfig "github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// AdminConfigServiceInterface defines the service interface.
type AdminConfigServiceInterface interface {
	GetAdminConfig(ctx context.Context, orgHandle string) (model.AdminConfig, error)
	IsCDSEnabled(ctx context.Context, orgHandle string) bool
	IsInitialSchemaSyncDone(ctx context.Context, orgHandle string) bool
	IsSystemApplication(ctx context.Context, orgHandle, appId string) (bool, error)
	GetCORSAllowedOrigins(ctx context.Context, orgHandle, subOrgID string) []string
	UpdateAdminConfig(ctx context.Context, updatedConfig *model.AdminConfig, orgHandle string) error
	UpdateInitialSchemaSync(ctx context.Context, state bool, orgHandle string) error
}

// AdminConfigService is the default implementation.
type AdminConfigService struct{}

// IsCDSEnabled reports whether CDS is enabled for the organization. A sub organization is enabled
// when its primary organization is enabled and the organization access includes it.
func (a AdminConfigService) IsCDSEnabled(ctx context.Context, orgHandle string) bool {
	config, err := store.GetAdminConfig(ctx, orgHandle)
	if err == nil && config != nil && config.CDSEnabled {
		return true
	}
	primaryHandle, known := orgService.PrimaryHandleOf(ctx, orgHandle)
	if !known || primaryHandle == orgHandle {
		return false
	}
	primaryConfig, err := store.GetAdminConfig(ctx, primaryHandle)
	if err != nil || primaryConfig == nil || !primaryConfig.CDSEnabled {
		return false
	}
	return sharingService.IsSubOrgEnabled(ctx, orgHandle)
}

func (a AdminConfigService) IsInitialSchemaSyncDone(ctx context.Context, orgHandle string) bool {
	config, err := store.GetAdminConfig(ctx, orgHandle)
	if err != nil || config == nil {
		return false
	}
	return config.InitialSchemaSyncDone
}

func (a AdminConfigService) IsSystemApplication(ctx context.Context, orgHandle, appId string) (bool, error) {
	config, err := store.GetAdminConfig(ctx, orgHandle)
	if err != nil {
		return false, err
	}
	if config == nil {
		return false, nil
	}
	for _, sysApp := range config.SystemApplications {
		if sysApp == appId {
			return true, nil
		}
	}
	return false, nil
}

// GetCORSAllowedOrigins returns the browser origins the organisation allows, served from an
// in-memory snapshot of every organisation's origins because the CORS middleware consults
// it on every request, before authentication. An empty result means the organisation has
// configured none and the caller should fall back to the deployment-wide list. A failed
// reload keeps the previously loaded origins, so that a database blip does not change what
// browser clients are allowed.
//
// subOrgID is the ID of the sub organisation a /t/{orgHandle}/o/{org_id}/... path addresses,
// and empty otherwise. A sub organisation cannot configure origins of its own yet, so it is
// answered with those of its primary organisation, orgHandle.
func (a AdminConfigService) GetCORSAllowedOrigins(ctx context.Context, orgHandle, subOrgID string) []string {
	return corsOrigins.get(ctx, orgHandle)
}

func (a AdminConfigService) GetAdminConfig(ctx context.Context, orgHandle string) (model.AdminConfig, error) {

	defaultConfig := model.AdminConfig{
		OrgHandle:             orgHandle,
		CDSEnabled:            false,
		InitialSchemaSyncDone: false,
		SystemApplications:    []string{},
		CORSAllowedOrigins:    []string{},
	}
	config, err := store.GetAdminConfig(ctx, orgHandle)
	if err != nil {
		return defaultConfig, err
	}
	if config == nil {
		config = &defaultConfig
	}
	// A sub organization uses the enablement of its primary organization.
	if !config.CDSEnabled {
		config.CDSEnabled = a.IsCDSEnabled(ctx, orgHandle)
	}
	// A sub organization uses the identity resolution settings of its primary organization.
	if primaryHandle, known := orgService.PrimaryHandleOf(ctx, orgHandle); known && primaryHandle != orgHandle {
		primaryConfig, err := store.GetAdminConfig(ctx, primaryHandle)
		if err != nil {
			return defaultConfig, err
		}
		if primaryConfig != nil {
			config.AutoMergeEnabled = primaryConfig.AutoMergeEnabled
			config.AutoMergeThreshold = primaryConfig.AutoMergeThreshold
			config.ManualReviewThreshold = primaryConfig.ManualReviewThreshold
			config.DeterministicMatchDecisive = primaryConfig.DeterministicMatchDecisive
			// Its requests arrive under the path of the primary organization, so the CORS
			// middleware answers them with the origins of the primary organization.
			config.CORSAllowedOrigins = primaryConfig.CORSAllowedOrigins
		}
	}
	return *config, nil
}

// validateThresholds checks the two score thresholds against each other rather than only in
// isolation, because each is individually reasonable at values that break the engine when
// combined.
//
// Both bounds protect an invariant the scorer relies on:
//
//   - A match that an objection downgrades is capped at auto_merge − ScorePenaltyOffset, and
//     that capped score has to stay at or above the review threshold. Otherwise capping
//     suppresses the match entirely instead of escalating it to a person — no merge, no
//     review task, and nothing in the logs to say a candidate was ever found.
//   - The review threshold is also the bar a rule must clear to count as agreeing. Set at or
//     below the contradiction bar, scores the engine treats as near-noise start counting as
//     agreements and can become the primary signal for a merge.
//
// Zero means the organisation has never set that threshold, so the shipped default is what
// it is actually running and is what gets validated.
// thresholdPrecision is the number of decimal places a score is expressed in, and so the
// precision these comparisons are meaningful to.
const thresholdPrecision = 10000

func roundToThresholdPrecision(value float64) float64 {
	return math.Round(value*thresholdPrecision) / thresholdPrecision
}

func validateThresholds(autoMerge, manualReview float64) error {
	// A value outside the range a score is expressed in is not applied: the store reads it
	// back as unset and the organisation keeps running on the shipped default. Accepting it
	// would report a setting that has no effect.
	if autoMerge < 0 || autoMerge > 1 {
		return errors.NewClientError(errors.ErrorMessage{
			Code:    errors.UPDATE_CONFIG_BAD_REQUEST.Code,
			Message: errors.UPDATE_CONFIG_BAD_REQUEST.Message,
			Description: fmt.Sprintf(
				"auto_merge_threshold (%g) must be between 0 and 1, the range a match score is "+
					"expressed in. To stop automatic merging, set auto_merge_enabled to false.",
				autoMerge),
		}, http.StatusBadRequest)
	}
	if manualReview < 0 || manualReview > 1 {
		return errors.NewClientError(errors.ErrorMessage{
			Code:    errors.UPDATE_CONFIG_BAD_REQUEST.Code,
			Message: errors.UPDATE_CONFIG_BAD_REQUEST.Message,
			Description: fmt.Sprintf(
				"manual_review_threshold (%g) must be between 0 and 1, the range a match score is "+
					"expressed in.", manualReview),
		}, http.StatusBadRequest)
	}

	if autoMerge == 0 {
		autoMerge = constants.DefaultAutoMergeThreshold
	}
	if manualReview == 0 {
		manualReview = constants.DefaultManualReviewThreshold
	}

	if manualReview <= constants.ScoreContradictionThreshold {
		return errors.NewClientError(errors.ErrorMessage{
			Code:    errors.UPDATE_CONFIG_BAD_REQUEST.Code,
			Message: errors.UPDATE_CONFIG_BAD_REQUEST.Message,
			Description: fmt.Sprintf(
				"manual_review_threshold must be greater than %g. It is also the score at which a rule "+
					"counts as agreeing, so a lower value lets near-unrelated values count as evidence.",
				constants.ScoreContradictionThreshold),
		}, http.StatusBadRequest)
	}

	// Rounded to the precision the thresholds are expressed in. Subtracting the offset in
	// binary floating point lands just under the decimal value — 0.82 - 0.01 is
	// 0.80999999999999994 — so an exactly representable pair like 0.82/0.81 would be refused
	// for being a ten-thousandth of a percent too high, which no caller can act on.
	if highest := roundToThresholdPrecision(autoMerge - constants.ScorePenaltyOffset); manualReview > highest {
		return errors.NewClientError(errors.ErrorMessage{
			Code:    errors.UPDATE_CONFIG_BAD_REQUEST.Code,
			Message: errors.UPDATE_CONFIG_BAD_REQUEST.Message,
			Description: fmt.Sprintf(
				"manual_review_threshold (%g) must be at most %g, which is auto_merge_threshold (%g) "+
					"less %g. A match held back from an automatic merge is scored just under "+
					"auto_merge_threshold, and it has to stay high enough to still raise a review task.",
				manualReview, highest, autoMerge, constants.ScorePenaltyOffset),
		}, http.StatusBadRequest)
	}

	return nil
}

// UpdateAdminConfig validates and stores the organisation's configuration. It normalizes
// updatedConfig in place, so the caller can report back exactly what was stored.
func (a AdminConfigService) UpdateAdminConfig(ctx context.Context,
	updatedConfig *model.AdminConfig, orgHandle string) error {
	// A sub organization cannot change the configuration.
	if err := suborg.RejectWrite(ctx, orgHandle); err != nil {
		return err
	}

	logger := log.GetLogger()

	if err := validateThresholds(updatedConfig.AutoMergeThreshold, updatedConfig.ManualReviewThreshold); err != nil {
		return err
	}
	validatedOrigins, err := validateCORSOrigins(updatedConfig.CORSAllowedOrigins)
	if err != nil {
		return err
	}
	updatedConfig.CORSAllowedOrigins = validatedOrigins

	isCDSEnabledInitialState := a.IsCDSEnabled(ctx, orgHandle)
	isInitialSchemaSyncDoneInitialState := a.IsInitialSchemaSyncDone(ctx, orgHandle)

	// Schema sync status should not be changed via this method.
	updatedConfig.InitialSchemaSyncDone = isInitialSchemaSyncDoneInitialState

	schemaService := service.GetProfileSchemaService()
	if !isCDSEnabledInitialState && !isInitialSchemaSyncDoneInitialState && updatedConfig.CDSEnabled {
		// CDS is being enabled for the first time. Trigger initial schema sync.
		err := schemaService.SyncProfileSchema(ctx, orgHandle)
		if err != nil {
			logger.Error(fmt.Sprintf("UpdateAdminConfig: schema sync failed for org=%s", orgHandle), log.Error(err))
			return err
		}
		// Seed the mandatory "Identity Data" consent category after schema is ready.
		if err := consentService.GetConsentCategoryService().SeedDefaultConsentCategory(ctx, orgHandle); err != nil {
			return err
		}
		updatedConfig.InitialSchemaSyncDone = true
		logger.Debug(fmt.Sprintf("UpdateAdminConfig: CDS enabled for org=%s", orgHandle))
	}

	// In app_id mode, register each system application so its clientId->app_id mapping exists for the GET path.
	if sysconfig.GetCDSRuntime().Config.UsesAppIDIdentifier() {
		appService := appProvider.NewApplicationProvider().GetApplicationService()
		for _, appID := range updatedConfig.SystemApplications {
			exists, err := appService.ResolveAndRegisterApplication(ctx, appID, orgHandle)
			if err != nil {
				return err
			}
			if !exists {
				return errors.NewClientError(errors.ErrorMessage{
					Code:        errors.UPDATE_CONFIG_BAD_REQUEST.Code,
					Message:     errors.UPDATE_CONFIG_BAD_REQUEST.Message,
					Description: fmt.Sprintf("System application '%s' does not exist in the identity server.", appID),
				}, http.StatusBadRequest)
			}
		}
	}

	if err := store.UpdateAdminConfig(ctx, *updatedConfig, orgHandle); err != nil {
		return err
	}

	// Reload the origins so this process serves the new list at once. Other replicas pick it
	// up on their next reload.
	corsOrigins.invalidate()

	if !sysconfig.OrganizationManagementEnabled() {
		return nil
	}

	// Disabling CDS resets the organization tree sync.
	if isCDSEnabledInitialState && !updatedConfig.CDSEnabled {
		if err := orgService.ResetTreeSync(ctx, orgHandle); err != nil {
			return err
		}
	}

	// Enabling CDS stores the primary organization.
	if !isCDSEnabledInitialState && updatedConfig.CDSEnabled {
		if _, err := orgService.EnsurePrimary(ctx, orgHandle); err != nil {
			return err
		}
	}
	return nil
}

func (a AdminConfigService) UpdateInitialSchemaSync(ctx context.Context, state bool, orgHandle string) error {

	return store.UpdateInitialSchemaSyncConfig(ctx, state, orgHandle)
}

// GetAdminConfigService returns a new instance.
func GetAdminConfigService() AdminConfigServiceInterface {
	return &AdminConfigService{}
}
