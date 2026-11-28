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

package service

import (
	"errors"
	"math"
	"net/http"
	"testing"

	"github.com/wso2/identity-customer-data-service/internal/admin_config/model"
	errors2 "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

func TestValidateTemporaryProfileCleanup(t *testing.T) {
	valid := []model.AdminConfig{
		{},
		{TemporaryProfileRetentionDays: 1, TemporaryProfileCleanupIntervalDays: 1},
		{TemporaryProfileRetentionDays: 366},
		{TemporaryProfileRetentionDays: math.MaxInt32, TemporaryProfileCleanupIntervalDays: math.MaxInt32},
	}
	for _, config := range valid {
		if err := validateTemporaryProfileCleanup(&config); err != nil {
			t.Errorf("validateTemporaryProfileCleanup(%+v) = %v, want nil", config, err)
		}
	}
	invalid := []model.AdminConfig{
		{TemporaryProfileRetentionDays: -1},
		{TemporaryProfileRetentionDays: math.MinInt32},
		{TemporaryProfileCleanupIntervalDays: -1},
	}
	for _, config := range invalid {
		err := validateTemporaryProfileCleanup(&config)
		var clientError *errors2.ClientError
		if !errors.As(err, &clientError) || clientError.StatusCode != http.StatusBadRequest {
			t.Errorf("validateTemporaryProfileCleanup(%+v) = %v, want a 400 client error", config, err)
		}
	}
}
