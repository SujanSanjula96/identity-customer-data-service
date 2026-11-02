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

package model

import (
	"context"
	adminStore "github.com/wso2/identity-customer-data-service/internal/admin_config/store"
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

type Thresholds struct {
	AutoMergeEnabled bool
	AutoMerge        float64
	ManualReview     float64
	// DeterministicMatchDecisive makes an agreement on any deterministic rule conclusive on
	// its own, skipping the objections the other rules could raise. This is how unification
	// behaved before typed matching, so it is the default; an organisation opts out of it.
	DeterministicMatchDecisive bool
}

func DefaultThresholds() Thresholds {
	return Thresholds{
		AutoMergeEnabled:           true,
		AutoMerge:                  constants.DefaultAutoMergeThreshold,
		ManualReview:               constants.DefaultManualReviewThreshold,
		DeterministicMatchDecisive: true,
	}
}

func LoadThresholds(ctx context.Context, orgHandle string) Thresholds {
	logger := log.GetLogger()
	defaults := DefaultThresholds()

	// A sub organization uses the settings of its primary organization.
	configHandle := orgHandle
	if primaryHandle, known := orgService.PrimaryHandleOf(ctx, orgHandle); known {
		configHandle = primaryHandle
	}
	adminCfg, err := adminStore.GetAdminConfig(ctx, configHandle)
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

func Decide(score float64, thresholds Thresholds) string {
	if thresholds.AutoMergeEnabled && score >= thresholds.AutoMerge {
		return constants.DecisionAutoMerge
	}
	if score >= thresholds.ManualReview {
		return constants.DecisionManualReview
	}
	return constants.DecisionUnique
}
