/*
 * Copyright (c) 2025-2026, WSO2 LLC. (http://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

package workers

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	profileModel "github.com/wso2/identity-customer-data-service/internal/profile/model"
	profileStore "github.com/wso2/identity-customer-data-service/internal/profile/store"
	schemaModel "github.com/wso2/identity-customer-data-service/internal/profile_schema/model"
	schemaStore "github.com/wso2/identity-customer-data-service/internal/profile_schema/store"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/queue"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/provider"
)

// activeProfileQueue is the queue implementation used for profile unification.
// It is initialised by StartProfileWorker. All access is guarded by
// profileQueueMu to prevent data races between concurrent Enqueue calls and
// shutdown.
var (
	profileQueueMu     sync.RWMutex
	activeProfileQueue queue.ProfileUnificationQueue
)

// StartProfileWorker initialises the profile unification queue (using the
// provider configured in the runtime config) and starts the consumer
// goroutine. An error is returned when the queue cannot be created or
// started; the caller should treat this as a fatal startup failure.
func StartProfileWorker() error {
	cfg := config.GetCDSRuntime().Config.MessageQueue
	q, err := queue.NewProfileUnificationQueue(cfg)
	if err != nil {
		return fmt.Errorf("workers: failed to create profile unification queue: %w", err)
	}
	if err := q.Start(func(profile profileModel.Profile) {
		p, err := profileStore.GetProfile(profile.ProfileId)
		if err == nil && p != nil {
			unifyProfiles(*p)
		}
	}); err != nil {
		_ = q.Close()
		return fmt.Errorf("workers: failed to start profile unification queue: %w", err)
	}
	profileQueueMu.Lock()
	activeProfileQueue = q
	profileQueueMu.Unlock()
	return nil
}

// EnqueueProfileForProcessing adds a profile to the active queue for
// asynchronous unification. It is a no-op when the worker has not been
// started or has been stopped.
func EnqueueProfileForProcessing(profile profileModel.Profile) {
	profileQueueMu.RLock()
	q := activeProfileQueue
	profileQueueMu.RUnlock()
	if q != nil {
		if err := q.Enqueue(profile); err != nil {
			log.GetLogger().Error(fmt.Sprintf(
				"workers: failed to enqueue profile %s for unification: %v",
				profile.ProfileId, err))
		}
	}
}

// ProfileWorkerQueue is a thin adapter that allows service-layer code to
// enqueue profiles without taking a direct dependency on the queue package.
type ProfileWorkerQueue struct{}

// Enqueue forwards the profile to the active queue via EnqueueProfileForProcessing.
func (q *ProfileWorkerQueue) Enqueue(profile profileModel.Profile) {
	EnqueueProfileForProcessing(profile)
}

// StopProfileWorker gracefully shuts down the profile unification queue.
// It nils out the global reference under a write lock before calling Close,
// ensuring no concurrent Enqueue can send on a closed queue. It should be
// called during application shutdown.
func StopProfileWorker() error {
	profileQueueMu.Lock()
	q := activeProfileQueue
	activeProfileQueue = nil
	profileQueueMu.Unlock()
	if q != nil {
		return q.Close()
	}
	return nil
}

// unifyProfiles unifies profiles based on unification rules
func unifyProfiles(newProfile profileModel.Profile) {

	// Step 1: Fetch all unification rules
	ruleProvider := provider.NewUnificationRuleProvider()
	ruleService := ruleProvider.GetUnificationRuleService()
	unificationRules, err := ruleService.GetUnificationRules(newProfile.OrgHandle)
	logger := log.GetLogger()
	if len(unificationRules) == 0 {
		logger.Info(fmt.Sprintf("No unification rules found for tenant: %s", newProfile.OrgHandle))
		return
	}
	logger.Info(fmt.Sprintf("Beginning to evaluate unification for profile: %s", newProfile.ProfileId))
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to fetch unification rules for unifying profile: %s",
			newProfile.ProfileId), log.Error(err))
	}

	// 🔹 Step 2: Fetch all existing profiles from DB
	existingMasterProfiles, err := profileStore.GetAllReferenceProfilesExceptForCurrent(newProfile)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to fetch existing master profiles for unification of profile: %s",
			newProfile.ProfileId), log.Error(err))
	}

	unificationRules = filterActiveRulesAndSortByPriority(unificationRules)
	// 🔹 Step 3: Loop through unification rules and compare profiles
	for _, rule := range unificationRules {

		for _, existingMasterProfile := range existingMasterProfiles {

			if existingMasterProfile.ProfileId == newProfile.ProfileStatus.ReferenceProfileId {
				// Skip if the existing master profile is the parent of the new profile
				return
			}

			if doesProfileMatch(existingMasterProfile, newProfile, rule) {

				existingMasterProfile.ProfileStatus.References, _ = profileStore.FetchReferencedProfiles(existingMasterProfile.ProfileId)

				//  Merge the existing master to the old master of current
				schemaRules, _ := schemaStore.GetProfileSchemaAttributesForOrg(newProfile.OrgHandle)
				newMasterProfile := MergeProfiles(existingMasterProfile, newProfile, schemaRules)

				if len(existingMasterProfile.ProfileStatus.References) == 0 {

					hasUserIDExisting := existingMasterProfile.UserId != ""
					hasUserIDNew := newProfile.UserId != ""

					// Case 1: perm-temp or temp-perm
					if hasUserIDExisting != hasUserIDNew {
						logger.Info(fmt.Sprintf("Stitching Temporray profile: %s to the permnanent profile:%s ",
							newProfile.ProfileId, existingMasterProfile.ProfileId))

						var newChild profileModel.Reference
						if hasUserIDExisting {
							newMasterProfile.ProfileId = existingMasterProfile.ProfileId
							newMasterProfile.UserId = existingMasterProfile.UserId
							newChild = profileModel.Reference{
								ProfileId: newProfile.ProfileId,
								Reason:    rule.RuleName,
							}
						} else {
							newMasterProfile.ProfileId = newProfile.ProfileId
							newMasterProfile.UserId = newProfile.UserId
							newChild = profileModel.Reference{
								ProfileId: existingMasterProfile.ProfileId,
								Reason:    rule.RuleName,
							}
						}

						children := []profileModel.Reference{newChild}

						err = profileStore.UpdateProfileReferences(newMasterProfile, children)
						if err != nil {
							logger.Error(fmt.Sprintf("Failed to add child profiles to the master profile: %s",
								newProfile.ProfileId), log.Error(err))
							return
						}

						// Update ApplicationData
						for _, appCtx := range newMasterProfile.ApplicationData {
							err := profileStore.InsertMergedMasterProfileAppData(newMasterProfile.ProfileId, appCtx)
							if err != nil {
								logger.Error(fmt.Sprintf("Failed to update app data for master profile: %s while unifying profile: %s",
									newMasterProfile.ProfileId, newProfile.ProfileId), log.Error(err))
								return
							}
						}

						// Update Traits
						if newMasterProfile.Traits != nil {
							err := profileStore.InsertMergedMasterProfileTraitData(newMasterProfile.ProfileId, newMasterProfile.Traits)
							if err != nil {
								logger.Error(fmt.Sprintf("Failed to update traits for master profile: %s while unifying profile: %s",
									newMasterProfile.ProfileId, newProfile.ProfileId), log.Error(err))
								return
							}
						}

						// Update Identity
						if newMasterProfile.IdentityAttributes != nil {
							err := profileStore.MergeIdentityDataOfProfiles(newMasterProfile.ProfileId, newMasterProfile.IdentityAttributes)
							if err != nil {
								logger.Error(fmt.Sprintf("Failed to update IdentityData for master profile: %s while unifying profile: %s",
									newMasterProfile.ProfileId, newProfile.ProfileId), log.Error(err))
								return
							}
						}
						return
					} else {
						userId := ""
						if hasUserIDExisting && hasUserIDNew {
							if existingMasterProfile.UserId == newProfile.UserId {
								userId = existingMasterProfile.UserId
								logger.Info(fmt.Sprintf("Both profiles are permanent profiles. Hence creating a new master profile: %s",
									newProfile.ProfileId))
							} else {
								logger.Info("We are not handling merging two permanent profiles with different userIds")
								continue
							}
						} else {
							logger.Info(fmt.Sprintf("Both profiles are temporary profiles. Hence creating a new master profile: %s",
								newProfile.ProfileId))
						}
						newMasterProfile.ProfileId = uuid.New().String()
						newMasterProfile.UserId = userId
						newMasterProfile.Location = utils.BuildProfileLocation(newMasterProfile.OrgHandle, newMasterProfile.ProfileId)
						childProfile1 := profileModel.Reference{
							ProfileId: newProfile.ProfileId,
							Reason:    rule.RuleName,
						}
						childProfile2 := profileModel.Reference{
							ProfileId: existingMasterProfile.ProfileId,
							Reason:    rule.RuleName,
						}
						newMasterProfile.ProfileStatus = &profileModel.ProfileStatus{
							IsReferenceProfile: true,
							ListProfile:        false,
							References:         []profileModel.Reference{childProfile1, childProfile2},
						}

						err := profileStore.InsertProfile(newMasterProfile)
						if err != nil {
							logger.Error(fmt.Sprintf("Failed to insert master profile while unifying profile: %s",
								newProfile.ProfileId), log.Error(err))
							return
						}

						children := []profileModel.Reference{childProfile1, childProfile2}

						err = profileStore.UpdateProfileReferences(newMasterProfile, children)

						if err != nil {
							logger.Error(fmt.Sprintf("Failed to add child profiles to the master profile: %s",
								newMasterProfile.ProfileId), log.Error(err))
							return
						}
						// Update ApplicationData
						for _, appCtx := range newMasterProfile.ApplicationData {
							err := profileStore.InsertMergedMasterProfileAppData(newMasterProfile.ProfileId, appCtx)
							if err != nil {
								logger.Error(fmt.Sprintf("Failed to update app data for master profile: %s while unifying profile: %s",
									newMasterProfile.ProfileId, newProfile.ProfileId), log.Error(err))
								return
							}
						}

						// Update Traits
						if newMasterProfile.Traits != nil {
							err := profileStore.InsertMergedMasterProfileTraitData(newMasterProfile.ProfileId, newMasterProfile.Traits)
							if err != nil {
								logger.Error(fmt.Sprintf("Failed to update traits for master profile: %s while unifying profile: %s",
									newMasterProfile.ProfileId, newProfile.ProfileId), log.Error(err))
								return
							}
						}

						// Update Identity
						if newMasterProfile.IdentityAttributes != nil {
							err := profileStore.MergeIdentityDataOfProfiles(newMasterProfile.ProfileId, newMasterProfile.IdentityAttributes)
							if err != nil {
								logger.Error(fmt.Sprintf("Failed to update IdentityData for master profile: %s while unifying profile: %s",
									newMasterProfile.ProfileId, newProfile.ProfileId), log.Error(err))
								return
							}
						}
						return
					}

				} else if (len(existingMasterProfile.ProfileStatus.References) > 0) && existingMasterProfile.ProfileStatus.IsReferenceProfile {

					hasUserID_existing := existingMasterProfile.UserId != ""
					hasUserID_new := newProfile.UserId != ""
					children := []profileModel.Reference{}

					// Case 1: perm-temp or temp-perm
					if hasUserID_existing != hasUserID_new {
						logger.Info(fmt.Sprintf("Stitching Temporray profile: %s to the permnanent profile: %s",
							newProfile.ProfileId, existingMasterProfile.ProfileId))

						var newChild profileModel.Reference
						if hasUserID_existing {
							newMasterProfile.ProfileId = existingMasterProfile.ProfileId
							newMasterProfile.UserId = existingMasterProfile.UserId
							newChild = profileModel.Reference{
								ProfileId: newProfile.ProfileId,
								Reason:    rule.RuleName,
							}
							children = append(children, newChild)
						} else {
							newMasterProfile.ProfileId = newProfile.ProfileId
							newMasterProfile.UserId = newProfile.UserId

							err = profileStore.UpdateProfileReferences(newMasterProfile, existingMasterProfile.ProfileStatus.References)

							if err != nil {
								logger.Error(fmt.Sprintf("Failed to update profile references for master profile: %s while unifying profile: %s",
									newMasterProfile.ProfileId, newProfile.ProfileId), log.Error(err))
								return
							}

							newChild = profileModel.Reference{
								ProfileId: existingMasterProfile.ProfileId,
								Reason:    rule.RuleName,
							}
							children = append(children, newChild)
						}

						err = profileStore.UpdateProfileReferences(newMasterProfile, children)
						if err != nil {
							logger.Error(fmt.Sprintf("Failed to add child profiles to the master profile: %s",
								newProfile.ProfileId), log.Error(err))
							return
						}

						// Update ApplicationData
						for _, appCtx := range newMasterProfile.ApplicationData {
							err := profileStore.InsertMergedMasterProfileAppData(newMasterProfile.ProfileId, appCtx)
							if err != nil {
								logger.Error(fmt.Sprintf("Failed to update app data for master profile: %s while unifying profile: %s",
									newMasterProfile.ProfileId, newProfile.ProfileId), log.Error(err))
								return
							}
						}

						// Update Traits
						if newMasterProfile.Traits != nil {
							err := profileStore.InsertMergedMasterProfileTraitData(newMasterProfile.ProfileId, newMasterProfile.Traits)
							if err != nil {
								logger.Error(fmt.Sprintf("Failed to update traits for master profile: %s while unifying profile: %s",
									newMasterProfile.ProfileId, newProfile.ProfileId), log.Error(err))
								return
							}
						}

						// Update Identity
						if newMasterProfile.IdentityAttributes != nil {
							err := profileStore.MergeIdentityDataOfProfiles(newMasterProfile.ProfileId, newMasterProfile.IdentityAttributes)
							if err != nil {
								logger.Error(fmt.Sprintf("Failed to update IdentityData for master profile: %s while unifying profile: %s",
									newMasterProfile.ProfileId, newProfile.ProfileId), log.Error(err))
								return
							}
						}
						return
					} else {
						// Case 2: Both temporary OR both permanent with same user_id
						// In both cases, merge into existing master (no new master creation)

						if hasUserID_existing && hasUserID_new {
							// Both permanent profiles
							if existingMasterProfile.UserId != newProfile.UserId {
								logger.Info("We are not handling merging two permanent profiles with different userIds")
								continue
							}
							logger.Info(fmt.Sprintf("Both profiles are permanent profiles with same user_id. Merging new profile %s into existing master: %s",
								newProfile.ProfileId, existingMasterProfile.ProfileId))
						} else {
							// Both temporary profiles
							logger.Info(fmt.Sprintf("Both profiles are temporary profiles. Merging new profile %s into existing master: %s",
								newProfile.ProfileId, existingMasterProfile.ProfileId))
						}

						// Use the existing master profile ID and update it with merged data
						newMasterProfile.ProfileId = existingMasterProfile.ProfileId
						newMasterProfile.UserId = existingMasterProfile.UserId

						// Add new profile as a child
						childProfile1 := profileModel.Reference{
							ProfileId: newProfile.ProfileId,
							Reason:    rule.RuleName,
						}

						children := []profileModel.Reference{childProfile1}

						err = profileStore.UpdateProfileReferences(newMasterProfile, children)
						if err != nil {
							logger.Error(fmt.Sprintf("Failed to add child profiles to the master profile: %s",
								newMasterProfile.ProfileId), log.Error(err))
							return
						}

						// Update ApplicationData
						for _, appCtx := range newMasterProfile.ApplicationData {
							err := profileStore.InsertMergedMasterProfileAppData(newMasterProfile.ProfileId, appCtx)
							if err != nil {
								logger.Error(fmt.Sprintf("Failed to update app data for master profile: %s while unifying profile: %s",
									newMasterProfile.ProfileId, newProfile.ProfileId), log.Error(err))
								return
							}
						}

						// Update Traits
						if newMasterProfile.Traits != nil {
							err := profileStore.InsertMergedMasterProfileTraitData(newMasterProfile.ProfileId, newMasterProfile.Traits)
							if err != nil {
								logger.Error(fmt.Sprintf("Failed to update traits for master profile: %s while unifying profile: %s",
									newMasterProfile.ProfileId, newProfile.ProfileId), log.Error(err))
								return
							}
						}

						// Update Identity
						if newMasterProfile.IdentityAttributes != nil {
							err := profileStore.MergeIdentityDataOfProfiles(newMasterProfile.ProfileId, newMasterProfile.IdentityAttributes)
							if err != nil {
								logger.Error(fmt.Sprintf("Failed to update IdentityData for master profile: %s while unifying profile: %s",
									newMasterProfile.ProfileId, newProfile.ProfileId), log.Error(err))
								return
							}
						}
						return
					}

				}
			}
		}
	}
}

func filterActiveRulesAndSortByPriority(rules []model.UnificationRule) []model.UnificationRule {
	activeRules := make([]model.UnificationRule, 0, len(rules))
	for _, r := range rules {
		if r.IsActive {
			activeRules = append(activeRules, r)
		}
	}
	sort.Slice(activeRules, func(i, j int) bool {
		return activeRules[i].Priority < activeRules[j].Priority
	})
	return activeRules
}

// MergeProfiles merges two profiles based on unification rules
func MergeProfiles(existingProfile profileModel.Profile, incomingProfile profileModel.Profile, schemaRules []schemaModel.ProfileSchemaAttribute) profileModel.Profile {

	logger := log.GetLogger()
	logger.Info("Merging profiles, " + existingProfile.ProfileId + " and " + incomingProfile.ProfileId)
	merged := existingProfile
	// todo: I doubt if this is fine.. we need to run through all to build a new profile
	for _, rule := range schemaRules {
		traitPath := strings.Split(rule.AttributeName, ".")
		if len(traitPath) < 2 {
			continue
		}
		traitNamespace := traitPath[0]
		propertyName := traitPath[1]

		// Gather the fields for enrichment profiles
		var existingVal, newVal interface{}
		switch traitNamespace {
		case "traits":
			if existingProfile.Traits != nil {
				existingVal = existingProfile.Traits[propertyName]
			}
			if incomingProfile.Traits != nil {
				newVal = incomingProfile.Traits[propertyName]
			}
		case "identity_attributes":
			if existingProfile.IdentityAttributes != nil {
				existingVal = existingProfile.IdentityAttributes[propertyName]
			}
			if incomingProfile.IdentityAttributes != nil {
				newVal = incomingProfile.IdentityAttributes[propertyName]
			}
		}

		// Perform merge based on strategy
		mergedVal := MergeTraitValue(existingVal, newVal, rule.MergeStrategy, rule.ValueType, rule.MultiValued)

		if mergedVal == nil || mergedVal == "" {
			// keep it absent instead of "key": null
			continue
		}
		// Apply merged result
		switch traitNamespace {
		case "traits":
			if merged.Traits == nil {
				merged.Traits = map[string]interface{}{}
			}
			merged.Traits[propertyName] = mergedVal
		case "identity_attributes":
			if merged.IdentityAttributes == nil {
				merged.IdentityAttributes = map[string]interface{}{}
			}
			merged.IdentityAttributes[propertyName] = mergedVal
		}
	}

	// Merge devices by default.
	merged.ApplicationData = mergeAppData(existingProfile.ApplicationData, incomingProfile.ApplicationData, schemaRules)

	if incomingProfile.UserId != "" {
		merged.UserId = incomingProfile.UserId // todo: need to decide on this as we are also focusing on perm-perm
	}
	if existingProfile.UserId != "" {
		merged.UserId = existingProfile.UserId // todo: need to decide on this as we are also focusing on perm-perm
	}

	merged.OrgHandle = incomingProfile.OrgHandle // todo: need to
	merged.CreatedAt = existingProfile.CreatedAt // todo: need to decide on this too.
	merged.UpdatedAt = time.Now().UTC()

	return merged
}

// doesProfileMatch checks if two profiles have matching attributes based on a unification rule
func doesProfileMatch(existingProfile profileModel.Profile, newProfile profileModel.Profile, rule model.UnificationRule) bool {

	log.GetLogger().Debug(fmt.Sprintf("Checking if profiles match for existing id: %s, new id: %s for the rule: %s",
		existingProfile.ProfileId, newProfile.ProfileId, rule.RuleName))
	if rule.PropertyName == "user_id" {
		if existingProfile.UserId != "" && newProfile.UserId != "" {
			if existingProfile.UserId == newProfile.UserId {
				log.GetLogger().Info("Profiles have same user_id. Hence proceeding to merge the profile.")
				return true
			}
			return false
		}
		return false
	} else {
		existingJSON, _ := json.Marshal(existingProfile)
		newJSON, _ := json.Marshal(newProfile)
		existingValues := extractFieldFromJSON(existingJSON, rule.PropertyName)
		newValues := extractFieldFromJSON(newJSON, rule.PropertyName)
		logger := log.GetLogger()
		if checkForMatch(existingValues, newValues) {
			logger.Info(fmt.Sprintf("Profiles %s, %s has matched for unification rule: %s ", existingProfile.ProfileId,
				newProfile.ProfileId, rule.RuleName))
			return true
		}
		return false
	}
}

// extractFieldFromJSON extracts a nested field from raw JSON (`[]byte`) without pre-converting to a map
func extractFieldFromJSON(jsonData []byte, fieldPath string) []interface{} {
	var jsonObj interface{}
	err := json.Unmarshal(jsonData, &jsonObj)
	if err != nil {
		return nil // Return nil if JSON parsing fails
	}

	// Navigate the JSON dynamically
	return getNestedJSONField(jsonObj, fieldPath)
}

// getNestedJSONField retrieves a nested field from a parsed JSON object
func getNestedJSONField(jsonObj interface{}, fieldPath string) []interface{} {
	fields := strings.Split(fieldPath, ".")
	var value interface{} = jsonObj

	for _, field := range fields {
		if nestedMap, ok := value.(map[string]interface{}); ok {
			value = nestedMap[field]
		} else if nestedSlice, ok := value.([]interface{}); ok {
			var results []interface{}
			for _, item := range nestedSlice {
				if itemMap, ok := item.(map[string]interface{}); ok {
					extracted := getNestedJSONField(itemMap, strings.Join(fields[1:], "."))
					results = append(results, extracted...)
				}
			}
			return results
		} else {
			return nil
		}
	}

	if list, ok := value.([]interface{}); ok {
		return list // Return extracted values from the list
	}

	return []interface{}{value} // Wrap a single value in a list
}

// checkForMatch checks if at least one value from `newProfile` exists in `existingProfile`
func checkForMatch(existingValues, newValues []interface{}) bool {
	existingSet := make(map[string]bool)
	for _, val := range existingValues {
		if str, ok := val.(string); ok {
			existingSet[str] = true
		}
	}

	// 🔹 Check if at least one value from `newValues` exists in `existingSet`
	for _, val := range newValues {
		if str, ok := val.(string); ok {
			if existingSet[str] {
				return true
			}
		}
	}
	return false
}

func MergeTraitValue(existing interface{}, incoming interface{}, strategy string, valueType string, multiValued bool) interface{} {

	switch strings.ToLower(strategy) {
	case "overwrite":
		if incoming == nil {
			return existing
		}
		if incoming == "" {
			return existing
		}
		// todo:  We rely on the new value. But ideally we should define more precsise rules to merge the values.
		return incoming

	case "ignore":
		if existing != nil {
			return existing
		}
		return incoming

	case "combine":
		if !multiValued {
			log.GetLogger().Warn("Merge strategy 'combine' is used for a non-multi-valued field. ")
			return incoming

		}
		switch strings.ToLower(valueType) {
		case "text", "string":
			a := toStringSlice(existing)
			b := toStringSlice(incoming)
			return combineUniqueStrings(a, b)

		case "integer":
			a := toIntSlice(existing)
			b := toIntSlice(incoming)
			return combineUniqueInts(a, b)

		case "decimal":
			a := toFloatSlice(existing)
			b := toFloatSlice(incoming)
			return combineUniqueFloats(a, b)

		case "boolean":
			// Usually not multi-valued, but if so, allow combining
			a := toBoolSlice(existing)
			b := toBoolSlice(incoming)
			return combineUniqueBools(a, b)

		case "date_time", "datetime":
			a := toStringSlice(existing) // Assuming ISO date strings
			b := toStringSlice(incoming)
			return combineUniqueStrings(a, b)

		case "object":
			// Arrays of objects not merged by default: return incoming or append as-is
			a, okA := existing.([]interface{})
			b, okB := incoming.([]interface{})
			if okA && okB {
				return append(a, b...)
			}
			return incoming

		default:
			// Unknown or unsupported type — just return incoming
			return incoming
		}
	default:
		// fallback to overwrite
		return incoming
	}
}

func toStringSlice(value interface{}) []string {
	switch v := value.(type) {
	case []string:
		return v
	case string:
		return []string{v}
	case []interface{}:
		var result []string
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	default:
		return []string{}
	}
}

func toIntSlice(value interface{}) []int {
	switch v := value.(type) {
	case []int:
		return v
	case []interface{}:
		result := make([]int, 0, len(v))
		for _, item := range v {
			if i, ok := item.(float64); ok {
				result = append(result, int(i))
			} else if i, ok := item.(int); ok {
				result = append(result, i)
			}
		}
		return result
	case int:
		return []int{v}
	case float64:
		return []int{int(v)}
	default:
		return []int{}
	}
}

func combineUniqueStrings(a, b []string) []string {
	seen := make(map[string]bool)
	var combined []string
	for _, val := range append(a, b...) {
		if !seen[val] {
			seen[val] = true
			combined = append(combined, val)
		}
	}
	return combined
}

func combineUniqueInts(a, b []int) []int {
	seen := make(map[int]bool)
	var combined []int
	for _, val := range append(a, b...) {
		if !seen[val] {
			seen[val] = true
			combined = append(combined, val)
		}
	}
	return combined
}

func toFloatSlice(value interface{}) []float64 {
	switch v := value.(type) {
	case []float64:
		return v
	case []interface{}:
		var result []float64
		for _, item := range v {
			switch i := item.(type) {
			case float64:
				result = append(result, i)
			case int:
				result = append(result, float64(i))
			}
		}
		return result
	case float64:
		return []float64{v}
	case int:
		return []float64{float64(v)}
	default:
		return []float64{}
	}
}

func toBoolSlice(value interface{}) []bool {
	switch v := value.(type) {
	case []bool:
		return v
	case []interface{}:
		var result []bool
		for _, item := range v {
			if b, ok := item.(bool); ok {
				result = append(result, b)
			}
		}
		return result
	case bool:
		return []bool{v}
	default:
		return []bool{}
	}
}

func combineUniqueFloats(a, b []float64) []float64 {
	seen := make(map[float64]bool)
	var combined []float64
	for _, val := range append(a, b...) {
		if !seen[val] {
			seen[val] = true
			combined = append(combined, val)
		}
	}
	return combined
}

func combineUniqueBools(a, b []bool) []bool {
	seen := make(map[bool]bool)
	var combined []bool
	for _, val := range append(a, b...) {
		if !seen[val] {
			seen[val] = true
			combined = append(combined, val)
		}
	}
	return combined
}

func mergeAppData(existingAppData, incomingAppData []profileModel.ApplicationData, rules []schemaModel.ProfileSchemaAttribute) []profileModel.ApplicationData {

	logger := log.GetLogger()
	mergedMap := make(map[string]profileModel.ApplicationData)

	// Initialize with existingAppData
	for _, app := range existingAppData {
		mergedMap[app.AppId] = app
		logger.Info("Merging existing application data for application: " + app.AppId)
	}

	for _, newApp := range incomingAppData {
		existingApp, found := mergedMap[newApp.AppId]
		logger.Info("Merging existing application data for application: " + newApp.AppId)
		if !found {
			mergedMap[newApp.AppId] = newApp
			continue
		}

		// Merge app-specific data using rule-based strategies
		if existingApp.AppSpecificData == nil {
			existingApp.AppSpecificData = map[string]interface{}{}
		}
		if newApp.AppSpecificData != nil {
			for key, newVal := range newApp.AppSpecificData {
				existingVal := existingApp.AppSpecificData[key]

				// Find merge strategy from enrichment rules
				strategy := ""
				valueType := ""
				multiValued := false

				for _, r := range rules {
					if r.AttributeName == fmt.Sprintf("application_data.%s", key) {
						strategy = r.MergeStrategy
						valueType = r.ValueType
						multiValued = r.MultiValued
						break
					}
				}

				mergedVal := MergeTraitValue(existingVal, newVal, strategy, valueType, multiValued)

				existingApp.AppSpecificData[key] = mergedVal

			}
		} else {
			logger.Warn(fmt.Sprintf("No app-specific data for application: %s", newApp.AppId))
		}

		mergedMap[newApp.AppId] = existingApp
	}

	var mergedList []profileModel.ApplicationData
	for appID, app := range mergedMap {
		logger.Info(fmt.Sprintf("Merged application data for application: %s", appID))
		mergedList = append(mergedList, app)
	}

	logger.Info(fmt.Sprintf("Application data merge completed for %d applications", len(mergedList)))
	return mergedList
}
