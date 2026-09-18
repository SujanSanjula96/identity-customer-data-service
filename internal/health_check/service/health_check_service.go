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

package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/provider"
	"github.com/wso2/identity-customer-data-service/internal/system/database/scripts"
	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// HealthCheckServiceInterface defines the service interface.
type HealthCheckServiceInterface interface {
	CheckReadiness(ctx context.Context) error
}

// HealthCheckService is the default implementation.
type HealthCheckService struct{}

// GetHealthCheckService returns a new instance.
func GetHealthCheckService() HealthCheckServiceInterface {
	return &HealthCheckService{}
}

// CheckReadiness reports whether this instance can serve traffic.
//
// The query runs under a short deadline of its own. The connection pool is
// bounded, so a saturated instance would otherwise wait here and answer
// nothing. An instance that cannot answer quickly is not ready, which is the
// answer the probe needs.
func (h HealthCheckService) CheckReadiness(ctx context.Context) error {
	logger := log.GetLogger()
	if logger == nil {
		return errors.New("logger not initialized")
	}

	dbProvider := provider.NewDBProvider()
	dbClient, err := dbProvider.GetDBClient()
	if err != nil {
		return fmt.Errorf("failed to create database client: %v", err)
	}
	defer dbClient.Close()

	ctx, cancel := context.WithTimeout(ctx, database.DefaultReadinessTimeout)
	defer cancel()

	// Perform a lightweight query to ensure DB connectivity.
	_, err = dbClient.ExecuteQueryContext(ctx, scripts.HealthCheckPing)
	if err != nil {
		return fmt.Errorf("database connectivity check failed: %v", err)
	}

	return nil
}
