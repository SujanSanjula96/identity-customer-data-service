/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
 *
 * Choreo Labs licenses this file to you under the Apache License,
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

package model

import (
	"context"

	adminStore "github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// Thresholds are an organization's settings that turn a candidate's score into a merge decision.
type Thresholds struct {
	AutoMergeEnabled bool
	AutoMerge        float64
	ManualReview     float64
	// DeterministicMatchDecisive makes an agreement on any deterministic rule conclusive on
	// its own, skipping the objections the other rules could raise. This is how unification
	// behaved before typed matching, so it is the default; an organization opts out of it.
	DeterministicMatchDecisive bool
}

// DefaultThresholds returns the thresholds used when an organization has not set its own.
func DefaultThresholds() Thresholds {
	return Thresholds{
		AutoMergeEnabled:           true,
		AutoMerge:                  constants.DefaultAutoMergeThreshold,
		ManualReview:               constants.DefaultManualReviewThreshold,
		DeterministicMatchDecisive: true,
	}
}

// LoadThresholds returns the organization's thresholds from its admin config, taken from the primary
// organization for a sub organization. It falls back to the defaults when the config cannot be loaded, and
// keeps the default for any threshold that is not set.
func LoadThresholds(ctx context.Context, orgID string) Thresholds {
	logger := log.GetLogger()
	defaults := DefaultThresholds()

	// A sub organization uses the settings of its primary organization.
	configOrgID := orgID
	if primary, known := orgService.PrimaryOf(ctx, orgID); known {
		configOrgID = primary.ID
	}
	adminCfg, err := adminStore.GetAdminConfig(ctx, configOrgID)
	if err != nil || adminCfg == nil {
		logger.Warn("LoadThresholds: failed to load admin config, using defaults")
		return defaults
	}

	defaults.AutoMergeEnabled = adminCfg.AutoMergeEnabled
	defaults.DeterministicMatchDecisive = adminCfg.DeterministicMatchDecisive

	if adminCfg.AutoMergeThreshold > 0 {
		defaults.AutoMerge = adminCfg.AutoMergeThreshold
	}
	if adminCfg.ManualReviewThreshold > 0 {
		defaults.ManualReview = adminCfg.ManualReviewThreshold
	}

	return defaults
}

// Decide returns DecisionAutoMerge when auto-merge is enabled and score reaches the auto-merge threshold,
// DecisionManualReview when score reaches the manual review threshold, and DecisionUnique otherwise.
func Decide(score float64, thresholds Thresholds) string {
	if thresholds.AutoMergeEnabled && score >= thresholds.AutoMerge {
		return constants.DecisionAutoMerge
	}
	if score >= thresholds.ManualReview {
		return constants.DecisionManualReview
	}
	return constants.DecisionUnique
}
