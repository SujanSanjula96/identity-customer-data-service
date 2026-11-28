/*
 * Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
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

package server

import (
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
)

// secretWarnings names the secrets that are empty although the server starts
// without them. Each one leaves CDD running but refusing requests it is meant
// to serve, which otherwise shows up only as a 401 on the caller's side.
// credentialsSupported tells whether the identity provider sends service
// credentials.
func secretWarnings(cfg config.Config, credentialsSupported bool) []string {
	var warnings []string
	if strings.TrimSpace(cfg.AuthServer.AdminUsername) == "" || strings.TrimSpace(cfg.AuthServer.AdminPassword) == "" {
		warnings = append(warnings, "auth_server.admin_username or auth_server.admin_password is empty, so every "+
			"call to the synchronization endpoints is refused, including the identity provider's. Set "+
			"AUTH_SERVER_ADMIN_PASSWORD, in repository/conf/secrets.env or the environment, to the password "+
			"the identity provider presents.")
	}
	if credentialsSupported && cfg.IDP.ServiceCredentials.EncryptionKey == "" {
		warnings = append(warnings, "idp.service_credentials.encryption_key is empty, so CDD refuses the "+
			"service credentials the identity provider sends, and calls it only with a client pair configured "+
			"under auth_server. Set SERVICE_CREDENTIALS_ENCRYPTION_KEY, in repository/conf/secrets.env or the "+
			"environment, to the output of `openssl rand -base64 32`.")
	}
	return warnings
}
