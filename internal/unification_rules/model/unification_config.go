/*
 * Copyright (c) (2025-2026), Choreo Labs LLC. (https://choreolabs.com).
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

import "github.com/wso2/identity-customer-data-service/internal/system/constants"

// ProfileUnificationMode represents rules for merging user profiles
type ProfileUnificationMode struct {
	MergeType string `json:"merge_type" bson:"merge_type" binding:"required"`
	Rule      string `json:"rule" bson:"rule" binding:"required"`
}

// UnificationTrigger sets when profile unification runs.
type UnificationTrigger struct {
	TriggerType string `json:"trigger_type" bson:"trigger_type" binding:"required"`
	LastTrigger int64  `json:"last_trigger" bson:"last_trigger"`
	Duration    int64  `json:"duration" bson:"duration"`
}

// Config is the profile unification configuration: the merge rule for each kind of profile pair and the
// trigger.
type Config struct {
	ProfileUnificationMode []ProfileUnificationMode `json:"profile_unification_mode" bson:"profile_unification_mode"`

	ProfileUnificationTrigger UnificationTrigger `json:"profile_unification_trigger" bson:"profile_unification_trigger"`
}

// DefaultConfig returns the built-in unification configuration: every kind of profile pair merges on trigger,
// and unification is triggered when a profile is updated.
func DefaultConfig() Config {
	return Config{
		ProfileUnificationMode: []ProfileUnificationMode{
			{
				MergeType: constants.TempProfileTempProfileMerge,
				Rule:      constants.MergeOnTrigger,
			},
			{
				MergeType: constants.PermProfilePermProfileMerge,
				Rule:      constants.MergeOnTrigger,
			},
			{
				MergeType: constants.TempProfilePermProfileMerge,
				Rule:      constants.MergeOnTrigger,
			},
		},
		ProfileUnificationTrigger: UnificationTrigger{
			TriggerType: constants.SyncProfileOnUpdate,
		},
	}
}
