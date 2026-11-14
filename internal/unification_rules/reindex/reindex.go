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

// Package reindex keeps the blocking index of each sub organization in line with the shared
// unification rules that run in it.
//
// The blocking index is per organization. A change to a rule, to a sharing policy, or to the
// organization access of a primary organization can change the set of active rules in a sub
// organization that the request does not name. The caller takes a snapshot of the active rules of
// the sub organizations before the change, and gives it to Reconcile after the change. Reconcile
// takes a second snapshot and changes the index of each sub organization by the difference.
package reindex

import (
	"context"
	"fmt"
	"sort"

	"github.com/wso2/identity-customer-data-service/internal/identity_resolution/worker"
	orgService "github.com/wso2/identity-customer-data-service/internal/organization/service"
	shareStore "github.com/wso2/identity-customer-data-service/internal/sharing/store"
	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/constants"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/utils"
	"github.com/wso2/identity-customer-data-service/internal/unification_rules/model"
	urService "github.com/wso2/identity-customer-data-service/internal/unification_rules/service"
)

// Snapshot holds the active rules of each sub organization of a primary organization, by
// organization ID and then by property name. A nil snapshot means that there is nothing to
// reconcile.
type Snapshot struct {
	primaryOrgId string
	rules        map[string]map[string]model.UnificationRule
}

// Plan is the index work for one organization.
type Plan struct {
	// Remove holds the properties whose keys go out of the index.
	Remove []string
	// Rebuild holds the rules whose keys change shape. Their keys go out, and then come back.
	Rebuild []model.UnificationRule
	// Add holds the rules whose keys come into the index.
	Add []model.UnificationRule
}

// IsEmpty reports whether the plan has no work.
func (p Plan) IsEmpty() bool {
	return len(p.Remove) == 0 && len(p.Rebuild) == 0 && len(p.Add) == 0
}

// Hooks for the tests.
var (
	listSubOrgIds    = orgService.GetSubOrganizationIds
	activeRulesOf    = loadActiveRules
	removeIndex      = worker.RemoveAttributeIndex
	indexNewProperty = worker.IndexNewAttribute
)

// Take returns the active rules of each sub organization of the primary organization. It returns
// nil when organization management is disabled or when the primary organization has no sub
// organization.
func Take(ctx context.Context, primaryOrgId string) (*Snapshot, error) {

	if !config.OrganizationManagementEnabled() || primaryOrgId == "" {
		return nil, nil
	}
	orgIds, err := listSubOrgIds(ctx, primaryOrgId)
	if err != nil {
		return nil, err
	}
	if len(orgIds) == 0 {
		return nil, nil
	}
	snapshot := &Snapshot{
		primaryOrgId: primaryOrgId,
		rules:        make(map[string]map[string]model.UnificationRule, len(orgIds)),
	}
	for _, orgId := range orgIds {
		rules, err := activeRulesOf(ctx, orgId)
		if err != nil {
			return nil, err
		}
		snapshot.rules[orgId] = rules
	}
	return snapshot, nil
}

// TakeOrLog returns the snapshot of Take. It logs an error and returns nil when Take fails, so the
// request continues without the re-index.
func TakeOrLog(ctx context.Context, primaryOrgId string) *Snapshot {

	snapshot, err := Take(ctx, primaryOrgId)
	if err != nil {
		log.GetLogger().Error(fmt.Sprintf("Reindexer: failed to read the active rules of the sub "+
			"organizations of '%s'. Their blocking index does not change.", primaryOrgId), log.Error(err))
		return nil
	}
	return snapshot
}

// TakeForRule returns the snapshot of the tree of the organization when the organization shares
// the rule. It returns nil when the rule has no sharing policy, because a change to the rule then
// does not change a sub organization.
func TakeForRule(ctx context.Context, orgId, ruleId string) *Snapshot {

	if !config.OrganizationManagementEnabled() {
		return nil
	}
	policy, err := shareStore.GetPolicy(ctx, model.SharingResourceType, ruleId, orgId)
	if err != nil {
		log.GetLogger().Error(fmt.Sprintf("Reindexer: failed to read the sharing policy of rule '%s'. "+
			"The blocking index of the sub organizations does not change.", ruleId), log.Error(err))
		return nil
	}
	if policy == nil {
		return nil
	}
	primary, ok := orgService.PrimaryOf(ctx, orgId)
	if !ok {
		return nil
	}
	return TakeOrLog(ctx, primary.ID)
}

// ReconcileAsync runs Reconcile in the background. The context of the background work keeps the
// values of the request context, but the end of the request does not cancel it.
func ReconcileAsync(ctx context.Context, before *Snapshot) {

	if before == nil {
		return
	}
	bgCtx := context.WithoutCancel(ctx)
	utils.SafeGo("sub organization index reconcile", func() { Reconcile(bgCtx, before) })
}

// Reconcile compares the active rules of each sub organization now with the snapshot from before
// the change, and changes the blocking index of each sub organization by the difference.
func Reconcile(ctx context.Context, before *Snapshot) {

	if before == nil {
		return
	}
	logger := log.GetLogger()
	after, err := Take(ctx, before.primaryOrgId)
	if err != nil {
		logger.Error(fmt.Sprintf("Reindexer: failed to read the active rules of the sub organizations "+
			"of '%s'. Their blocking index does not change.", before.primaryOrgId), log.Error(err))
		return
	}
	afterRules := map[string]map[string]model.UnificationRule{}
	if after != nil {
		afterRules = after.rules
	}

	// A sub organization that is in only one snapshot was created or removed between the two.
	// Compare the snapshots for the organizations of both.
	orgIds := make(map[string]bool, len(before.rules)+len(afterRules))
	for orgId := range before.rules {
		orgIds[orgId] = true
	}
	for orgId := range afterRules {
		orgIds[orgId] = true
	}
	for _, orgId := range sortedKeys(orgIds) {
		plan := Diff(before.rules[orgId], afterRules[orgId])
		if plan.IsEmpty() {
			continue
		}
		logger.Info(fmt.Sprintf("Reindexer: sub organization '%s' — %d properties to remove, %d to "+
			"rebuild, %d to add", orgId, len(plan.Remove), len(plan.Rebuild), len(plan.Add)))
		apply(ctx, orgId, plan)
	}
}

// Diff returns the index work that changes the index for the rules before into the index for the
// rules after. Both maps hold the active rules of one organization by property name.
func Diff(before, after map[string]model.UnificationRule) Plan {

	var plan Plan
	for _, property := range sortedKeys(before) {
		if _, ok := after[property]; !ok {
			plan.Remove = append(plan.Remove, property)
		}
	}
	for _, property := range sortedKeys(after) {
		rule := after[property]
		old, ok := before[property]
		switch {
		case !ok:
			plan.Add = append(plan.Add, rule)
		case old.AttributeType != rule.AttributeType || old.UnificationMethod != rule.UnificationMethod:
			plan.Rebuild = append(plan.Rebuild, rule)
		}
	}
	return plan
}

// apply runs the plan for the organization. It removes keys before it writes keys, so a rebuild
// does not delete the keys that it writes.
func apply(ctx context.Context, orgId string, plan Plan) {

	for _, property := range plan.Remove {
		removeIndex(ctx, orgId, property)
	}
	for _, rule := range plan.Rebuild {
		removeIndex(ctx, orgId, rule.PropertyName)
		indexNewProperty(ctx, orgId, rule)
	}
	for _, rule := range plan.Add {
		indexNewProperty(ctx, orgId, rule)
	}
}

// loadActiveRules returns the rules that run in the organization, by property name. The rules have
// the defaults that the resolution worker applies, so two snapshots compare the key shape that the
// worker writes.
func loadActiveRules(ctx context.Context, orgId string) (map[string]model.UnificationRule, error) {

	raw, err := urService.GetUnificationRuleService().GetUnificationRules(ctx, orgId)
	if err != nil {
		return nil, err
	}
	rules := model.ActiveSortedByPriority(raw, constants.AttributeTypePrimitiveExact,
		constants.UnificationMethodDeterministic, constants.DefaultMatchStrength,
		constants.DefaultMismatchStrength)
	if !config.GetCDSRuntime().Config.IdentityResolution.FuzzyMatchingEnabled {
		rules = model.AsExactMatching(rules)
	}
	byProperty := make(map[string]model.UnificationRule, len(rules))
	for _, rule := range rules {
		// The rules are in evaluation order. The first rule of a property is the one that runs.
		if _, ok := byProperty[rule.PropertyName]; !ok {
			byProperty[rule.PropertyName] = rule
		}
	}
	return byProperty, nil
}

func sortedKeys[V any](m map[string]V) []string {

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
