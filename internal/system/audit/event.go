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

package audit

// Action is what the actor tried to do. The set is small on purpose: a new operation uses one of
// these actions before it adds a new one.
type Action string

const (
	ActionCreate  Action = "create"
	ActionRead    Action = "read"
	ActionList    Action = "list"
	ActionUpdate  Action = "update"
	ActionDelete  Action = "delete"
	ActionSync    Action = "sync"
	ActionLink    Action = "link"
	ActionMerge   Action = "merge"
	ActionResolve Action = "resolve"
)

// Target types. A target type names the kind of resource that an operation works on.
const (
	TargetProfile                      = "profile"
	TargetProfileConsent               = "profile_consent"
	TargetProfileSchema                = "profile_schema"
	TargetProfileSchemaAttribute       = "profile_schema_attribute"
	TargetProfileSchemaSharingPolicy   = "profile_schema_sharing_policy"
	TargetUnificationRule              = "unification_rule"
	TargetUnificationRuleOption        = "unification_rule_option"
	TargetUnificationRuleSharingPolicy = "unification_rule_sharing_policy"
	TargetConsentCategory              = "consent_category"
	TargetAdminConfig                  = "admin_config"
	TargetReviewTask                   = "review_task"
	TargetOrganization                 = "organization"
	TargetOrganizationTree             = "organization_tree"
	TargetOrganizationAccessPolicy     = "organization_access_policy"
)

// Event describes one audited operation. Its name is always "<target type>.<action>", so the name
// of an event follows from what it does.
type Event struct {
	// TargetType is the kind of resource.
	TargetType string
	// Action is what the actor tried to do.
	Action Action
	// IDParam is the path parameter that holds the ID of the target. It is empty when the path
	// has no ID, for example for a create operation.
	IDParam string
	// DetailParams are the path parameters that the record copies to its details.
	DetailParams []string
}

// Name returns the name of the event, for example profile.update.
func (e Event) Name() string {
	return e.TargetType + "." + string(e.Action)
}

// IsRead reports whether the operation only reads data.
func (e Event) IsRead() bool {
	return e.Action == ActionRead || e.Action == ActionList
}

// The event catalog. Each operation of CDS has one event. The routes and the workers use these
// variables, so the catalog is the one place that defines what CDS audits.
var (
	// Profiles.
	ProfileCreate = Event{TargetType: TargetProfile, Action: ActionCreate}
	ProfileList   = Event{TargetType: TargetProfile, Action: ActionList}
	ProfileRead   = Event{TargetType: TargetProfile, Action: ActionRead, IDParam: "profileId"}
	ProfileUpdate = Event{TargetType: TargetProfile, Action: ActionUpdate, IDParam: "profileId"}
	ProfileDelete = Event{TargetType: TargetProfile, Action: ActionDelete, IDParam: "profileId"}
	ProfileSync   = Event{TargetType: TargetProfile, Action: ActionSync}
	ProfileLink   = Event{TargetType: TargetProfile, Action: ActionLink}
	ProfileMerge  = Event{TargetType: TargetProfile, Action: ActionMerge}

	ProfileConsentRead   = Event{TargetType: TargetProfileConsent, Action: ActionRead, IDParam: "profileId"}
	ProfileConsentUpdate = Event{TargetType: TargetProfileConsent, Action: ActionUpdate, IDParam: "profileId"}

	// Profile schema.
	ProfileSchemaRead   = Event{TargetType: TargetProfileSchema, Action: ActionRead}
	ProfileSchemaDelete = Event{TargetType: TargetProfileSchema, Action: ActionDelete}
	ProfileSchemaSync   = Event{TargetType: TargetProfileSchema, Action: ActionSync}

	ProfileSchemaAttributeCreate = Event{TargetType: TargetProfileSchemaAttribute, Action: ActionCreate,
		DetailParams: []string{"scope"}}
	ProfileSchemaAttributeList = Event{TargetType: TargetProfileSchemaAttribute, Action: ActionList,
		DetailParams: []string{"scope"}}
	ProfileSchemaAttributeRead = Event{TargetType: TargetProfileSchemaAttribute, Action: ActionRead,
		IDParam: "attrID", DetailParams: []string{"scope"}}
	ProfileSchemaAttributeUpdate = Event{TargetType: TargetProfileSchemaAttribute, Action: ActionUpdate,
		IDParam: "attrID", DetailParams: []string{"scope"}}
	// ProfileSchemaAttributeDelete deletes one attribute, or all attributes of a scope when the
	// record has no target ID.
	ProfileSchemaAttributeDelete = Event{TargetType: TargetProfileSchemaAttribute, Action: ActionDelete,
		IDParam: "attrID", DetailParams: []string{"scope"}}

	ProfileSchemaSharingPolicyCreate = Event{TargetType: TargetProfileSchemaSharingPolicy, Action: ActionCreate,
		DetailParams: []string{"scope", "attrID"}}
	ProfileSchemaSharingPolicyList = Event{TargetType: TargetProfileSchemaSharingPolicy, Action: ActionList,
		DetailParams: []string{"scope", "attrID"}}
	ProfileSchemaSharingPolicyRead = Event{TargetType: TargetProfileSchemaSharingPolicy, Action: ActionRead,
		IDParam: "policyId", DetailParams: []string{"scope", "attrID"}}
	ProfileSchemaSharingPolicyUpdate = Event{TargetType: TargetProfileSchemaSharingPolicy, Action: ActionUpdate,
		IDParam: "policyId", DetailParams: []string{"scope", "attrID"}}
	ProfileSchemaSharingPolicyDelete = Event{TargetType: TargetProfileSchemaSharingPolicy, Action: ActionDelete,
		IDParam: "policyId", DetailParams: []string{"scope", "attrID"}}

	// Unification rules.
	UnificationRuleCreate     = Event{TargetType: TargetUnificationRule, Action: ActionCreate}
	UnificationRuleList       = Event{TargetType: TargetUnificationRule, Action: ActionList}
	UnificationRuleRead       = Event{TargetType: TargetUnificationRule, Action: ActionRead, IDParam: "ruleId"}
	UnificationRuleUpdate     = Event{TargetType: TargetUnificationRule, Action: ActionUpdate, IDParam: "ruleId"}
	UnificationRuleDelete     = Event{TargetType: TargetUnificationRule, Action: ActionDelete, IDParam: "ruleId"}
	UnificationRuleOptionList = Event{TargetType: TargetUnificationRuleOption, Action: ActionList}

	UnificationRuleSharingPolicyCreate = Event{TargetType: TargetUnificationRuleSharingPolicy,
		Action: ActionCreate, DetailParams: []string{"ruleId"}}
	UnificationRuleSharingPolicyList = Event{TargetType: TargetUnificationRuleSharingPolicy,
		Action: ActionList, DetailParams: []string{"ruleId"}}
	UnificationRuleSharingPolicyRead = Event{TargetType: TargetUnificationRuleSharingPolicy,
		Action: ActionRead, IDParam: "policyId", DetailParams: []string{"ruleId"}}
	UnificationRuleSharingPolicyUpdate = Event{TargetType: TargetUnificationRuleSharingPolicy,
		Action: ActionUpdate, IDParam: "policyId", DetailParams: []string{"ruleId"}}
	UnificationRuleSharingPolicyDelete = Event{TargetType: TargetUnificationRuleSharingPolicy,
		Action: ActionDelete, IDParam: "policyId", DetailParams: []string{"ruleId"}}

	// Consent categories.
	ConsentCategoryCreate = Event{TargetType: TargetConsentCategory, Action: ActionCreate}
	ConsentCategoryList   = Event{TargetType: TargetConsentCategory, Action: ActionList}
	ConsentCategoryRead   = Event{TargetType: TargetConsentCategory, Action: ActionRead, IDParam: "categoryId"}
	ConsentCategoryUpdate = Event{TargetType: TargetConsentCategory, Action: ActionUpdate, IDParam: "categoryId"}
	ConsentCategoryDelete = Event{TargetType: TargetConsentCategory, Action: ActionDelete, IDParam: "categoryId"}

	// Admin configuration.
	AdminConfigRead   = Event{TargetType: TargetAdminConfig, Action: ActionRead}
	AdminConfigUpdate = Event{TargetType: TargetAdminConfig, Action: ActionUpdate}

	// Identity resolution.
	ReviewTaskList    = Event{TargetType: TargetReviewTask, Action: ActionList}
	ReviewTaskResolve = Event{TargetType: TargetReviewTask, Action: ActionResolve, IDParam: "taskId"}

	// Organizations.
	OrganizationSync     = Event{TargetType: TargetOrganization, Action: ActionSync}
	OrganizationTreeSync = Event{TargetType: TargetOrganizationTree, Action: ActionSync}
	OrganizationTreeRead = Event{TargetType: TargetOrganizationTree, Action: ActionRead}

	OrganizationAccessPolicyCreate = Event{TargetType: TargetOrganizationAccessPolicy, Action: ActionCreate}
	OrganizationAccessPolicyList   = Event{TargetType: TargetOrganizationAccessPolicy, Action: ActionList}
	OrganizationAccessPolicyRead   = Event{TargetType: TargetOrganizationAccessPolicy, Action: ActionRead,
		IDParam: "orgPolicyId"}
	OrganizationAccessPolicyUpdate = Event{TargetType: TargetOrganizationAccessPolicy, Action: ActionUpdate,
		IDParam: "orgPolicyId"}
	OrganizationAccessPolicyDelete = Event{TargetType: TargetOrganizationAccessPolicy, Action: ActionDelete,
		IDParam: "orgPolicyId"}
)
