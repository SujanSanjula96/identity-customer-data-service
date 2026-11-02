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

// MethodOption describes a single matching method available for an attribute type.
type MethodOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// AttributeTypeOption describes an attribute type and the matching methods it supports.
//
// Evidence strengths are deliberately not offered here. They are set per rule and are
// currently derived from the attribute type unless the server is configured to accept an
// override, so presenting them as a form choice would imply a control most deployments do
// not have. Add them back alongside that setting when the override is opened up.
type AttributeTypeOption struct {
	Value          string         `json:"value"`
	Label          string         `json:"label"`
	AllowedMethods []MethodOption `json:"allowed_methods"`
}

// UnificationOptionsResponse is the payload returned by GET /unification-rules/options.
type UnificationOptionsResponse struct {
	AttributeTypes []AttributeTypeOption `json:"attribute_types"`
}
