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

package models

// ConsentAttribute represents a profile attribute covered by a consent category.
// Callers provide attribute_name (e.g. "traits.age"). Scope and AttributeId are derived
// automatically from the profile schema at write time and are never supplied by the caller.
// For applicationData scope, ApplicationIdentifier identifies which app's data the attribute belongs to.
type ConsentAttribute struct {
	Scope                 string `json:"scope,omitempty" bson:"scope"`                                            // derived — "identityAttributes" | "traits" | "applicationData"
	AttributeName         string `json:"attribute_name" bson:"attribute_name"`                                    // references ProfileSchemaAttribute.attribute_name
	AttributeId           string `json:"attribute_id" bson:"attribute_id"`                                        // internal FK to profile_schema.attribute_id; enables ON DELETE CASCADE
	ApplicationIdentifier string `json:"application_identifier,omitempty" bson:"application_identifier,omitempty"` // only for applicationData scope
}
