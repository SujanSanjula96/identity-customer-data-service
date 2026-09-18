/*
 * Copyright (c) 2025-2026, WSO2 LLC. (http://www.wso2.com).
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

package workers

import (
	"context"
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
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/queue"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/provider"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// activeProfileQueue is the queue implementation used for profile unification.
// It is initialised by StartProfileWorker. All access is guarded by
// profileQueueMu to prevent data races between concurrent Enqueue calls and
// shutdown.
var (
	profileQueueMu     sync.RWMutex
	activeProfileQueue queue.ProfileUnificationQueue
	// profileLifecycle counts the jobs that run, so that shutdown waits for
	// them before the database pool closes.
	profileLifecycle *jobLifecycle
)

// StartProfileWorker initialises the profile unification queue (using the
// provider configured in the runtime config) and starts the consumer
// goroutine. An error is returned when the queue cannot be created or
// started; the caller should treat this as a fatal startup failure.
func StartProfileWorker() error {
	cfg := config.GetCDSRuntime().Config
	q, err := queue.NewProfileUnificationQueue(cfg)
	if err != nil {
		return fmt.Errorf("workers: failed to create profile unification queue: %w", err)
	}
	lifecycle := newJobLifecycle()

	if err := q.Start(func(profile profileModel.Profile) error {
		return lifecycle.run(func(ctx context.Context) error {
			p, err := profileStore.GetProfile(ctx, profile.ProfileId)
			if err != nil {
				// The read failed, so the message is not processed and a
				// broker keeps it.
				return fmt.Errorf("workers: failed to read profile %s: %w", profile.ProfileId, err)
			}
			if p == nil {
				// The profile is gone, so there is nothing left to unify.
				return nil
			}
			// The result decides whether the message is acknowledged. A merge
			// that did not finish must come back.
			return unifyProfiles(ctx, *p)
		})
	}); err != nil {
		_ = q.Close(context.Background())
		return fmt.Errorf("workers: failed to start profile unification queue: %w", err)
	}
	profileQueueMu.Lock()
	activeProfileQueue = q
	profileLifecycle = lifecycle
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
func StopProfileWorker(ctx context.Context) error {
	profileQueueMu.Lock()
	q := activeProfileQueue
	lifecycle := profileLifecycle
	activeProfileQueue, profileLifecycle = nil, nil
	profileQueueMu.Unlock()

	if q == nil {
		return nil
	}
	if lifecycle == nil {
		return q.Close(ctx)
	}
	// Returns only when no unification job is still at work, so that the
	// caller can close the database pool.
	return lifecycle.stop(ctx, q.Close)
}

// unifyProfiles unifies profiles based on unification rules
// unifyProfiles unifies profiles based on unification rules.
//
// It reports whether the work is done. A profile that matches nothing is done,
// and so is a match the rules deliberately refuse to merge. A merge that is
// left half finished is not, and the caller must not acknowledge its message.
func unifyProfiles(ctx context.Context, newProfile profileModel.Profile) error {

	logger := log.GetLogger()

	// Step 1: Fetch all unification rules
	ruleProvider := provider.NewUnificationRuleProvider()
	ruleService := ruleProvider.GetUnificationRuleService()
	unificationRules, err := ruleService.GetUnificationRules(ctx, newProfile.OrgHandle)
	if err != nil {
		// Without the rules there is no way to know what matches. This used to
		// log and carry on with no rules, which looked like "nothing matched".
		return fmt.Errorf("failed to read the unification rules for profile %s: %w",
			newProfile.ProfileId, err)
	}
	if len(unificationRules) == 0 {
		logger.Info(fmt.Sprintf("No unification rules found for tenant: %s", newProfile.OrgHandle))
	}

	logger.Info(fmt.Sprintf("Beginning to evaluate unification for profile: %s", newProfile.ProfileId))

	// Step 2: Fetch all existing profiles from DB
	existingMasterProfiles, err := profileStore.GetAllReferenceProfilesExceptForCurrent(ctx, newProfile)
	if err != nil {
		return fmt.Errorf("failed to read the existing master profiles for profile %s: %w",
			newProfile.ProfileId, err)
	}

	// Step 3a: Direct userId match — system-level invariant, no rule needed.
	// Profiles with the same userId must always be merged.
	if newProfile.UserId != "" {
		for _, existingMasterProfile := range existingMasterProfiles {
			if existingMasterProfile.ProfileId == newProfile.ProfileStatus.ReferenceProfileId {
				continue
			}
			if existingMasterProfile.UserId == newProfile.UserId {
				logger.Info(fmt.Sprintf("Profiles %s and %s share the same userId %s. Proceeding with merge.",
					existingMasterProfile.ProfileId, newProfile.ProfileId, newProfile.UserId))
				return mergeMatchedProfiles(ctx, existingMasterProfile, newProfile,
					constants.SystemUserIdMatchReason)
			}
		}
	}

	// Step 3b: Rule-based matching (email, phone, etc.)
	unificationRules = filterActiveRulesAndSortByPriority(unificationRules)
	for _, rule := range unificationRules {
		for _, existingMasterProfile := range existingMasterProfiles {
			if existingMasterProfile.ProfileId == newProfile.ProfileStatus.ReferenceProfileId {
				// The existing master is already the parent of this profile,
				// so there is nothing left to do.
				return nil
			}
			if doesProfileMatch(existingMasterProfile, newProfile, rule) {
				return mergeMatchedProfiles(ctx, existingMasterProfile, newProfile, rule.RuleName)
			}
		}
	}

	// Nothing matched, which is a complete answer rather than a failure.
	return nil
}

// mergeMatchedProfiles handles all merge scenarios for two matched profiles.
// It determines the master/child relationship based on permanent (has userId) vs temporary,
// and whether the existing profile already has child references.
func mergeMatchedProfiles(ctx context.Context,
	existingMasterProfile profileModel.Profile, newProfile profileModel.Profile, reason string) error {

	logger := log.GetLogger()

	// Fetch references for the existing master profile
	refs, err := profileStore.FetchReferencedProfiles(ctx, existingMasterProfile.ProfileId)
	if err != nil {
		// The children of the master decide which branch below runs. Carrying
		// on with an empty list, as this used to, re-parents the wrong
		// profiles.
		return fmt.Errorf("failed to read the references of master profile %s: %w",
			existingMasterProfile.ProfileId, err)
	}
	existingMasterProfile.ProfileStatus.References = refs

	// Merge profile data using schema rules
	schemaRules, err := schemaStore.GetProfileSchemaAttributesForOrg(ctx, newProfile.OrgHandle)
	if err != nil {
		// The schema decides how each attribute combines. Merging without it,
		// as this used to, writes the wrong data to the master.
		return fmt.Errorf("failed to read the profile schema of organization %s: %w",
			newProfile.OrgHandle, err)
	}
	newMasterProfile := MergeProfiles(existingMasterProfile, newProfile, schemaRules)

	hasUserIDExisting := existingMasterProfile.UserId != ""
	hasUserIDNew := newProfile.UserId != ""
	hasExistingChildren := len(existingMasterProfile.ProfileStatus.References) > 0 &&
		existingMasterProfile.ProfileStatus.IsReferenceProfile

	// ── Case: Both permanent with different userIds — do not merge ──
	if hasUserIDExisting && hasUserIDNew && existingMasterProfile.UserId != newProfile.UserId {
		logger.Info(fmt.Sprintf("Not merging profiles %s and %s — different userIds (%s vs %s)",
			existingMasterProfile.ProfileId, newProfile.ProfileId,
			existingMasterProfile.UserId, newProfile.UserId))
		// A deliberate refusal to merge is a complete answer.
		return nil
	}

	// ── Case: perm-temp or temp-perm ──
	if hasUserIDExisting != hasUserIDNew {
		return mergePermanentAndTemporary(ctx, existingMasterProfile, newProfile, newMasterProfile,
			reason, hasExistingChildren)
	}

	// ── Case: Both permanent with same userId OR both temporary ──
	return mergeSameKindProfiles(ctx, existingMasterProfile, newProfile, newMasterProfile, reason,
		hasUserIDExisting, hasExistingChildren)
}

// mergePermanentAndTemporary merges a permanent profile (has userId) with a temporary one.
// The permanent profile always becomes the master.
func mergePermanentAndTemporary(ctx context.Context,
	existingMasterProfile profileModel.Profile,
	newProfile profileModel.Profile,
	newMasterProfile profileModel.Profile,
	reason string,
	hasExistingChildren bool,
) error {
	logger := log.GetLogger()

	hasUserIDExisting := existingMasterProfile.UserId != ""

	if hasUserIDExisting {
		// Existing is permanent — it stays as master, new becomes child
		logger.Info(fmt.Sprintf("Stitching temporary profile %s to permanent profile %s",
			newProfile.ProfileId, existingMasterProfile.ProfileId))

		newMasterProfile.ProfileId = existingMasterProfile.ProfileId
		newMasterProfile.UserId = existingMasterProfile.UserId

		newChild := profileModel.Reference{
			ProfileId: newProfile.ProfileId,
			Reason:    reason,
		}
		children := []profileModel.Reference{newChild}

		if err := profileStore.UpdateProfileReferences(ctx, newMasterProfile, children); err != nil {
			return fmt.Errorf("failed to make profile %s a child of master %s: %w",
				newProfile.ProfileId, newMasterProfile.ProfileId, err)
		}
	} else {
		// New is permanent — it becomes master, existing becomes child
		logger.Info(fmt.Sprintf("Stitching temporary profile %s to permanent profile %s",
			existingMasterProfile.ProfileId, newProfile.ProfileId))

		newMasterProfile.ProfileId = newProfile.ProfileId
		newMasterProfile.UserId = newProfile.UserId

		// If the existing master had children, re-parent them to the new master
		if hasExistingChildren {
			if err := profileStore.UpdateProfileReferences(ctx, newMasterProfile,
				existingMasterProfile.ProfileStatus.References); err != nil {
				return fmt.Errorf("failed to move the children of %s to master %s: %w",
					existingMasterProfile.ProfileId, newMasterProfile.ProfileId, err)
			}
		}

		newChild := profileModel.Reference{
			ProfileId: existingMasterProfile.ProfileId,
			Reason:    reason,
		}
		children := []profileModel.Reference{newChild}

		if err := profileStore.UpdateProfileReferences(ctx, newMasterProfile, children); err != nil {
			return fmt.Errorf("failed to make profile %s a child of master %s: %w",
				existingMasterProfile.ProfileId, newMasterProfile.ProfileId, err)
		}
	}

	// Write merged data to the master profile
	return persistMergedProfileData(ctx, newMasterProfile, newProfile.ProfileId)
}

// mergeSameKindProfiles merges two profiles of the same kind:
// both permanent (same userId) or both temporary.
func mergeSameKindProfiles(ctx context.Context,
	existingMasterProfile profileModel.Profile,
	newProfile profileModel.Profile,
	newMasterProfile profileModel.Profile,
	reason string,
	bothPermanent bool,
	hasExistingChildren bool,
) error {
	logger := log.GetLogger()

	if hasExistingChildren {
		// Existing master already has children — merge new profile into it as another child.
		if bothPermanent {
			logger.Info(fmt.Sprintf("Both profiles are permanent with same userId. Merging %s into existing master %s",
				newProfile.ProfileId, existingMasterProfile.ProfileId))
		} else {
			logger.Info(fmt.Sprintf("Both profiles are temporary. Merging %s into existing master %s",
				newProfile.ProfileId, existingMasterProfile.ProfileId))
		}

		newMasterProfile.ProfileId = existingMasterProfile.ProfileId
		newMasterProfile.UserId = existingMasterProfile.UserId

		newChild := profileModel.Reference{
			ProfileId: newProfile.ProfileId,
			Reason:    reason,
		}
		children := []profileModel.Reference{newChild}

		if err := profileStore.UpdateProfileReferences(ctx, newMasterProfile, children); err != nil {
			return fmt.Errorf("failed to make profile %s a child of master %s: %w",
				newProfile.ProfileId, newMasterProfile.ProfileId, err)
		}
	} else if bothPermanent {
		// Both permanent, same userId, no children — promote existing as master.
		logger.Info(fmt.Sprintf("Both profiles are permanent with same userId. Merging %s into existing master %s",
			newProfile.ProfileId, existingMasterProfile.ProfileId))

		newMasterProfile.ProfileId = existingMasterProfile.ProfileId
		newMasterProfile.UserId = existingMasterProfile.UserId

		newChild := profileModel.Reference{
			ProfileId: newProfile.ProfileId,
			Reason:    reason,
		}
		children := []profileModel.Reference{newChild}

		if err := profileStore.UpdateProfileReferences(ctx, newMasterProfile, children); err != nil {
			return fmt.Errorf("failed to make profile %s a child of master %s: %w",
				newProfile.ProfileId, newMasterProfile.ProfileId, err)
		}
	} else {
		// Both temporary, no children — create a new neutral master referencing both.
		logger.Info(fmt.Sprintf("Both profiles are temporary. Creating new master for %s and %s",
			newProfile.ProfileId, existingMasterProfile.ProfileId))

		newMasterProfile.ProfileId = uuid.New().String()
		newMasterProfile.UserId = ""
		newMasterProfile.Location = utils.BuildProfileLocation(newMasterProfile.OrgHandle, newMasterProfile.ProfileId)

		childProfile1 := profileModel.Reference{
			ProfileId: newProfile.ProfileId,
			Reason:    reason,
		}
		childProfile2 := profileModel.Reference{
			ProfileId: existingMasterProfile.ProfileId,
			Reason:    reason,
		}

		newMasterProfile.ProfileStatus = &profileModel.ProfileStatus{
			IsReferenceProfile: true,
			ListProfile:        false,
			References:         []profileModel.Reference{childProfile1, childProfile2},
		}

		if err := profileStore.InsertProfile(ctx, newMasterProfile); err != nil {
			_ = profileStore.DeleteProfile(ctx, newMasterProfile.ProfileId) // cleanup
			return fmt.Errorf("failed to insert a new master profile for %s and %s: %w",
				newProfile.ProfileId, existingMasterProfile.ProfileId, err)
		}

		children := []profileModel.Reference{childProfile1, childProfile2}
		if err := profileStore.UpdateProfileReferences(ctx, newMasterProfile, children); err != nil {
			// This branch mints a new identifier on every attempt, so a retry
			// would leave the master just inserted behind for ever. Remove it,
			// and the retry starts from where this one began.
			if cleanupErr := profileStore.DeleteProfile(ctx, newMasterProfile.ProfileId); cleanupErr != nil {
				logger.Error(fmt.Sprintf("Failed to remove the half-built master profile %s",
					newMasterProfile.ProfileId), log.Error(cleanupErr))
			}
			return fmt.Errorf("failed to attach the children to new master %s: %w",
				newMasterProfile.ProfileId, err)
		}
	}

	// Write merged data to the master profile
	return persistMergedProfileData(ctx, newMasterProfile, newProfile.ProfileId)
}

// persistMergedProfileData writes the merged application data, traits, and identity attributes
// to the master profile in the store.
func persistMergedProfileData(ctx context.Context, masterProfile profileModel.Profile,
	triggerProfileId string) error {

	// Update ApplicationData
	for _, appCtx := range masterProfile.ApplicationData {
		if err := profileStore.InsertMergedMasterProfileAppData(ctx, masterProfile.ProfileId, appCtx); err != nil {
			return fmt.Errorf("failed to write the merged application data of master %s, for profile %s: %w",
				masterProfile.ProfileId, triggerProfileId, err)
		}
	}

	// Update Traits
	if masterProfile.Traits != nil {
		if err := profileStore.InsertMergedMasterProfileTraitData(ctx, masterProfile.ProfileId,
			masterProfile.Traits); err != nil {
			return fmt.Errorf("failed to write the merged traits of master %s, for profile %s: %w",
				masterProfile.ProfileId, triggerProfileId, err)
		}
	}

	// Update Identity Attributes
	if masterProfile.IdentityAttributes != nil {
		if err := profileStore.MergeIdentityDataOfProfiles(ctx, masterProfile.ProfileId,
			masterProfile.IdentityAttributes); err != nil {
			return fmt.Errorf("failed to write the merged identity data of master %s, for profile %s: %w",
				masterProfile.ProfileId, triggerProfileId, err)
		}
	}

	return nil
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

// MergeProfiles merges two profiles based on schema rules and returns the merged profile.
func MergeProfiles(existingProfile profileModel.Profile, incomingProfile profileModel.Profile, schemaRules []schemaModel.ProfileSchemaAttribute) profileModel.Profile {
	logger := log.GetLogger()
	logger.Info("Merging profiles, " + existingProfile.ProfileId + " and " + incomingProfile.ProfileId)

	merged := existingProfile
	ruleMap := buildSchemaRuleMap(schemaRules)
	appRuleMap := buildApplicationSchemaRuleMap(schemaRules)

	merged.Traits = mergeNamespaceMap(existingProfile.Traits, incomingProfile.Traits, "traits", ruleMap)
	merged.IdentityAttributes = mergeNamespaceMap(existingProfile.IdentityAttributes, incomingProfile.IdentityAttributes, "identity_attributes", ruleMap)
	merged.ApplicationData = mergeAppData(existingProfile.ApplicationData, incomingProfile.ApplicationData, appRuleMap)

	if incomingProfile.UserId != "" {
		merged.UserId = incomingProfile.UserId
	}
	if existingProfile.UserId != "" {
		merged.UserId = existingProfile.UserId
	}

	merged.OrgHandle = incomingProfile.OrgHandle
	merged.CreatedAt = existingProfile.CreatedAt
	merged.UpdatedAt = time.Now().UTC()

	return merged
}

func buildSchemaRuleMap(rules []schemaModel.ProfileSchemaAttribute) map[string]schemaModel.ProfileSchemaAttribute {
	ruleMap := make(map[string]schemaModel.ProfileSchemaAttribute, len(rules))
	for _, rule := range rules {
		ruleMap[rule.AttributeName] = rule
	}
	return ruleMap
}

func mergeNamespaceMap(existingMap, incomingMap map[string]interface{}, namespace string,
	ruleMap map[string]schemaModel.ProfileSchemaAttribute) map[string]interface{} {

	if existingMap == nil && incomingMap == nil {
		return nil
	}

	result := make(map[string]interface{})

	for k, v := range existingMap {
		result[k] = v
	}

	for k, incomingVal := range incomingMap {
		path := namespace + "." + k
		existingVal := result[k]
		result[k] = mergeByPath(existingVal, incomingVal, path, ruleMap)
	}

	return result
}

func mergeByPath(existing, incoming interface{}, currentPath string,
	ruleMap map[string]schemaModel.ProfileSchemaAttribute) interface{} {

	if rule, ok := ruleMap[currentPath]; ok {
		if strings.EqualFold(rule.ValueType, constants.ComplexDataType) && !rule.MultiValued {
			existingMap, okE := toStringKeyedMap(existing)
			incomingMap, okI := toStringKeyedMap(incoming)

			if okE && okI {
				result := make(map[string]interface{})

				for k, v := range existingMap {
					result[k] = v
				}

				for k, incomingChild := range incomingMap {
					childPath := currentPath + "." + k
					existingChild := result[k]
					result[k] = mergeByPath(existingChild, incomingChild, childPath, ruleMap)
				}

				return result
			}
		}

		return MergeAttributeValue(existing, incoming, rule.MergeStrategy, rule.ValueType, rule.MultiValued)
	}

	existingMap, okE := toStringKeyedMap(existing)
	incomingMap, okI := toStringKeyedMap(incoming)

	if okE && okI {
		result := make(map[string]interface{})

		for k, v := range existingMap {
			result[k] = v
		}

		for k, incomingChild := range incomingMap {
			childPath := currentPath + "." + k
			existingChild := result[k]
			result[k] = mergeByPath(existingChild, incomingChild, childPath, ruleMap)
		}

		return result
	}

	if isEmptyValue(incoming) {
		return existing
	}

	return incoming
}

// doesProfileMatch checks if two profiles have matching attributes based on a unification rule
func doesProfileMatch(existingProfile profileModel.Profile, newProfile profileModel.Profile, rule model.UnificationRule) bool {

	log.GetLogger().Debug(fmt.Sprintf("Checking if profiles match for existing id: %s, new id: %s for the rule: %s",
		existingProfile.ProfileId, newProfile.ProfileId, rule.RuleName))
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

// extractFieldFromJSON extracts a nested field from raw JSON (`[]byte`) without pre-converting to a map
func extractFieldFromJSON(jsonData []byte, fieldPath string) []interface{} {
	var jsonObj interface{}
	err := json.Unmarshal(jsonData, &jsonObj)
	if err != nil {
		return nil
	}
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
		return list
	}

	return []interface{}{value}
}

// checkForMatch checks if at least one value from `newProfile` exists in `existingProfile`
func checkForMatch(existingValues, newValues []interface{}) bool {
	existingSet := make(map[string]bool)
	for _, val := range existingValues {
		if str, ok := val.(string); ok {
			existingSet[str] = true
		}
	}

	for _, val := range newValues {
		if str, ok := val.(string); ok {
			if existingSet[str] {
				return true
			}
		}
	}
	return false
}

func MergeAttributeValue(existing interface{}, incoming interface{}, strategy string, valueType string, multiValued bool) interface{} {

	switch strings.ToLower(strategy) {
	case "overwrite":
		if isEmptyValue(incoming) {
			return existing
		}
		return incoming
	case "ignore":
		if existing != nil {
			return existing
		}
		return incoming

	case "combine":
		if !multiValued {
			// For non-multiValued complex fields (e.g. product_interests map), deep-merge the maps.
			existingMap, okE := toStringKeyedMap(existing)
			incomingMap, okI := toStringKeyedMap(incoming)
			if okE && okI {
				return deepMergeMaps(existingMap, incomingMap)
			}
			if incoming == nil {
				return existing
			}
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
			a := toBoolSlice(existing)
			b := toBoolSlice(incoming)
			return combineUniqueBools(a, b)

		case "date_time", "datetime":
			a := toStringSlice(existing)
			b := toStringSlice(incoming)
			return combineUniqueStrings(a, b)

		case "complex":
			a := toInterfaceSlice(existing)
			b := toInterfaceSlice(incoming)

			if len(a) == 0 && len(b) == 0 {
				if incoming == nil {
					return existing
				}
				return incoming
			}
			if len(a) == 0 {
				return b
			}
			if len(b) == 0 {
				return a
			}
			return append(a, b...)

		default:
			return incoming
		}
	default:
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
	case primitive.A:
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

func toStringKeyedMap(v interface{}) (map[string]interface{}, bool) {
	if v == nil {
		return nil, false
	}
	m, ok := v.(map[string]interface{})
	return m, ok
}

func toInterfaceSlice(value interface{}) []interface{} {
	switch v := value.(type) {
	case []interface{}:
		return v
	case primitive.A:
		return []interface{}(v)
	case []map[string]interface{}:
		result := make([]interface{}, 0, len(v))
		for _, item := range v {
			result = append(result, item)
		}
		return result
	default:
		return nil
	}
}

func isEmptyValue(v interface{}) bool {
	if v == nil {
		return true
	}

	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val) == ""

	case []interface{}:
		return len(val) == 0

	case []string:
		return len(val) == 0

	case []int:
		return len(val) == 0

	case []float64:
		return len(val) == 0

	case []bool:
		return len(val) == 0

	case map[string]interface{}:
		return len(val) == 0
	}

	return false
}

// deepMergeMaps merges overlay into base, keeping keys from both.
// When the same key exists in both maps:
//   - nested maps are recursed into
//   - leaf values: overlay wins (equivalent to overwrite strategy)
//
// This is intentional: a complex parent with strategy=combine means
// "keep keys from both containers". Within each shared key, sub-attribute
// strategies are expected to be "overwrite" (the common case in this schema).
// If a sub-attribute ever needs strategy=combine, this function should be
// made schema-aware, accepting the rule set and current attribute path.
func deepMergeMaps(base, overlay map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(base))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range overlay {
		if baseVal, exists := result[k]; exists {
			baseMap, okB := toStringKeyedMap(baseVal)
			overlayMap, okO := toStringKeyedMap(v)
			if okB && okO {
				result[k] = deepMergeMaps(baseMap, overlayMap)
				continue
			}
		}
		result[k] = v
	}
	return result
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

func mergeAppData(existingAppData, incomingAppData []profileModel.ApplicationData,
	appRuleMap map[string]map[string]schemaModel.ProfileSchemaAttribute) []profileModel.ApplicationData {

	logger := log.GetLogger()
	mergedMap := make(map[string]profileModel.ApplicationData)

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

		appRules := appRuleMap[newApp.AppId]
		existingApp.AppSpecificData = mergeNamespaceMap(
			existingApp.AppSpecificData,
			newApp.AppSpecificData,
			"application_data",
			appRules,
		)

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

func buildApplicationSchemaRuleMap(rules []schemaModel.ProfileSchemaAttribute) map[string]map[string]schemaModel.ProfileSchemaAttribute {
	result := make(map[string]map[string]schemaModel.ProfileSchemaAttribute)

	for _, rule := range rules {
		if !strings.HasPrefix(rule.AttributeName, "application_data.") {
			continue
		}
		appID := rule.ApplicationIdentifier
		if appID == "" {
			continue
		}
		if result[appID] == nil {
			result[appID] = make(map[string]schemaModel.ProfileSchemaAttribute)
		}
		result[appID][rule.AttributeName] = rule
	}

	return result
}
