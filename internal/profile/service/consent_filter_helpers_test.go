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
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	consentModel "github.com/wso2/identity-customer-data-service/internal/consent/model"
)

func TestScopeFromName(t *testing.T) {
	tests := map[string]string{
		"traits.age":                   "traits",
		"identity_attributes.email":    "identity_attributes",
		"application_data.app-1.theme": "application_data",
		"traits":                       "traits",
		"something.else":               "something.else",
	}
	for name, want := range tests {
		assert.Equal(t, want, scopeFromName(name), name)
	}
}

func TestTopLevelKey(t *testing.T) {
	tests := map[string]string{
		"traits.engagement_score":                     "engagement_score",
		"traits.product_interests.api-platform.score": "product_interests",
		"identity_attributes.emails.work":             "emails",
		"plain":                                       "plain",
	}
	for name, want := range tests {
		assert.Equal(t, want, topLevelKey(name), name)
	}
}

func TestAttributeKey(t *testing.T) {
	tests := []struct {
		name string
		attr consentModel.ConsentAttribute
		want string
	}{
		{"trait", consentModel.ConsentAttribute{AttributeName: "traits.age"}, "traits::age"},
		{"nested trait uses the first segment", consentModel.ConsentAttribute{AttributeName: "traits.interests.sports"},
			"traits::interests"},
		{"identity attribute", consentModel.ConsentAttribute{AttributeName: "identity_attributes.email"},
			"identity_attributes::email"},
		{"application attribute", consentModel.ConsentAttribute{
			AttributeName: "application_data.app-1.theme", ApplicationIdentifier: "app-1"},
			"application_data::app-1::theme"},
		{"nested application attribute uses the first segment", consentModel.ConsentAttribute{
			AttributeName: "application_data.app-1.prefs.color", ApplicationIdentifier: "app-1"},
			"application_data::app-1::prefs"},
		{"whole application bucket", consentModel.ConsentAttribute{
			AttributeName: "application_data.other", ApplicationIdentifier: "app-1"}, "application_data::app-1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, attributeKey(tc.attr))
		})
	}
}

func TestUnionSets(t *testing.T) {
	got := unionSets([]map[string]bool{
		{"a": true, "b": true},
		nil,
		{"b": true, "c": true},
	})

	assert.Equal(t, map[string]bool{"a": true, "b": true, "c": true}, got)
	assert.Empty(t, unionSets(nil))
}
