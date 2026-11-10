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

package security

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wso2/identity-customer-data-service/internal/system/reqctx"
)

func TestActorOf(t *testing.T) {
	tests := []struct {
		name   string
		claims map[string]interface{}
		actor  reqctx.Actor
	}{
		{"user token",
			map[string]interface{}{"sub": "user-1", "client_id": "console", "aut": "APPLICATION_USER"},
			reqctx.Actor{Type: reqctx.ActorUser, ID: "user-1", ClientID: "console"}},
		{"application token",
			map[string]interface{}{"sub": "user-of-app", "client_id": "app-1", "aut": "APPLICATION"},
			reqctx.Actor{Type: reqctx.ActorApplication, ID: "app-1", ClientID: "app-1"}},
		{"subject is the client",
			map[string]interface{}{"sub": "app-1", "azp": "app-1"},
			reqctx.Actor{Type: reqctx.ActorApplication, ID: "app-1", ClientID: "app-1"}},
		{"introspection response with a username only",
			map[string]interface{}{"username": "alice@example.com", "client_id": "console"},
			reqctx.Actor{Type: reqctx.ActorUser, ID: "a***************m", ClientID: "console"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.actor, actorOf(test.claims))
		})
	}
}
