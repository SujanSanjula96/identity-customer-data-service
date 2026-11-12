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

// Package servicecreds holds the service credentials CDS calls the identity
// provider with, one pair per organization.
//
// An OAuth client in the Identity Server belongs to one tenant and can only
// authenticate at that tenant's token endpoint, so a single configured pair
// cannot serve more than one organization. The identity provider sends a pair
// for each organization as it enables CDS, and CDS keeps it here. A deployment
// with no encryption key stores nothing and uses the configured pair, as before.
//
// The store does not depend on the identity provider. The adapter of the
// identity provider verifies a pair before it is stored
// (idp.ServiceCredentialsVerifier), and the client of the identity provider
// reads it back with Get when it fetches a token.
package servicecreds

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/wso2/identity-customer-data-service/internal/system/config"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
	"github.com/wso2/identity-customer-data-service/internal/system/secrets"
)

// Credentials are one organization's service credentials.
type Credentials struct {
	OrgId        string
	OrgHandle    string
	ClientID     string
	ClientSecret string
}

var (
	sealerOnce   sync.Once
	cachedSealer *secrets.Sealer
	sealerErr    error
)

// sealer builds the deployment's sealer once. The configuration is fixed for the
// life of the process, so the key is resolved once and the AES key schedule is
// not redone on every token fetch.
func sealer() (*secrets.Sealer, error) {

	sealerOnce.Do(func() {
		cachedSealer, sealerErr = secrets.NewSealer(
			config.GetCDSRuntime().Config.IdP.ServiceCredentials.EncryptionKey)
	})
	return cachedSealer, sealerErr
}

// Enabled reports whether the deployment keeps per-organization credentials,
// which it does once an encryption key is configured. ValidateConfig refuses a
// key for an identity provider that does not send credentials.
func Enabled() bool {

	return config.GetCDSRuntime().Config.IdP.ServiceCredentials.EncryptionKey != ""
}

// ValidateConfig reports whether the configured encryption key is usable.
// supported tells whether the adapter of the identity provider verifies service
// credentials; a key is refused for one that does not, since nothing would send
// credentials. It is called at start so that a deployment with a broken key
// fails there, rather than at the first organization that sends credentials.
func ValidateConfig(cfg config.Config, supported bool) error {

	key := cfg.IdP.ServiceCredentials.EncryptionKey
	if key == "" {
		return nil
	}
	if !supported {
		return fmt.Errorf("idp.service_credentials.encryption_key is set, but the identity provider "+
			"'%s' does not send service credentials, so leave the key empty", cfg.IdP.Type)
	}
	if _, err := secrets.NewSealer(key); err != nil {
		return fmt.Errorf("idp.service_credentials.encryption_key is set but not usable: %w", err)
	}
	return nil
}

// CheckStore reports whether the table that holds the credentials can be read.
// It is called at start once a key is set, so that a PostgreSQL database
// without the migration fails there, rather than at every token fetch.
func CheckStore(ctx context.Context) error {

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return fmt.Errorf("failed to get a database client: %w", err)
	}
	defer dbClient.Close()

	if _, err := dbClient.ExecuteQueryContext(ctx, scripts.CheckOrgServiceCredentials); err != nil {
		return fmt.Errorf("the org_service_credentials table cannot be read; on PostgreSQL, run "+
			"dbscripts/migrations/003-service-credentials-postgres.sql: %w", err)
	}
	return nil
}

// Get returns the credentials stored for orgHandle. found is false when the
// organization has none, and the configured pair applies.
func Get(ctx context.Context, orgHandle string) (Credentials, bool, error) {

	logger := log.GetLogger()

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return Credentials{}, false, getFailed(orgHandle, "failed to get a database client", err)
	}
	defer dbClient.Close()

	results, err := dbClient.ExecuteQueryContext(ctx, scripts.GetOrgServiceCredentials, orgHandle)
	if err != nil {
		return Credentials{}, false, getFailed(orgHandle, "failed to read the credentials", err)
	}
	if len(results) == 0 {
		return Credentials{}, false, nil
	}

	row := results[0]
	sealed, _ := row["client_secret"].(string)
	sealedUnder, _ := row["key_id"].(string)

	keeper, err := sealer()
	if err != nil {
		return Credentials{}, false, getFailed(orgHandle, "no usable encryption key is configured", err)
	}
	clientSecret, err := keeper.Open(sealed, sealedUnder)
	if errors.Is(err, secrets.ErrSealedUnderAnotherKey) {
		// The key has changed since the credentials were stored. The configured
		// pair still serves the tenant it belongs to, and turning CDS off and on
		// for the organization sends credentials sealed under the new key.
		logger.Error(fmt.Sprintf("The service credentials of organization %s cannot be read with the configured "+
			"key, so the configured client is used. Turn CDS off and on for the organization to send them again.",
			orgHandle), log.Error(err))
		return Credentials{}, false, nil
	}
	if err != nil {
		return Credentials{}, false, getFailed(orgHandle, "failed to open the sealed client secret", err)
	}

	orgId, _ := row["org_id"].(string)
	clientID, _ := row["client_id"].(string)
	logger.Debug(fmt.Sprintf("Resolved the service credentials of organization: %s", orgHandle))

	return Credentials{
		OrgId:        orgId,
		OrgHandle:    orgHandle,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}, true, nil
}

// Save seals the client secret and persists the credentials, replacing whatever
// the organization held before.
func Save(ctx context.Context, creds Credentials) error {

	logger := log.GetLogger()

	keeper, err := sealer()
	if err != nil {
		return saveFailed(creds.OrgHandle, "no usable encryption key is configured", err)
	}
	sealed, err := keeper.Seal(creds.ClientSecret)
	if err != nil {
		return saveFailed(creds.OrgHandle, "failed to seal the client secret", err)
	}

	dbClient, err := provider.NewDBProvider().GetDBClient()
	if err != nil {
		return saveFailed(creds.OrgHandle, "failed to get a database client", err)
	}
	defer dbClient.Close()

	// An earlier organization with the same handle no longer exists, so its
	// credentials no longer work.
	_, err = dbClient.ExecuteQueryContext(ctx, scripts.DeleteStaleOrgServiceCredentials, creds.OrgHandle,
		creds.OrgId)
	if err != nil {
		return saveFailed(creds.OrgHandle, "failed to remove the credentials of an earlier organization", err)
	}
	_, err = dbClient.ExecuteQueryContext(ctx, scripts.UpsertOrgServiceCredentials,
		creds.OrgId, creds.OrgHandle, creds.ClientID, sealed, keeper.KeyID())
	if err != nil {
		return saveFailed(creds.OrgHandle, "failed to persist the credentials", err)
	}

	logger.Info(fmt.Sprintf("Persisted the service credentials of organization: %s", creds.OrgHandle))
	return nil
}

func getFailed(orgHandle, what string, err error) error {

	errorMsg := fmt.Sprintf("%s for the organization: %s", what, orgHandle)
	log.GetLogger().Debug(errorMsg, log.Error(err))
	return errors2.NewServerError(errors2.ErrorMessage{
		Code:        errors2.GET_SERVICE_CREDENTIALS_FAILED.Code,
		Message:     errors2.GET_SERVICE_CREDENTIALS_FAILED.Message,
		Description: errorMsg,
	}, err)
}

func saveFailed(orgHandle, what string, err error) error {

	errorMsg := fmt.Sprintf("%s for the organization: %s", what, orgHandle)
	log.GetLogger().Debug(errorMsg, log.Error(err))
	return errors2.NewServerError(errors2.ErrorMessage{
		Code:        errors2.SAVE_SERVICE_CREDENTIALS_FAILED.Code,
		Message:     errors2.SAVE_SERVICE_CREDENTIALS_FAILED.Message,
		Description: errorMsg,
	}, err)
}
