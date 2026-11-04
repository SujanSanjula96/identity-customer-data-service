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

package config

import (
	"fmt"
	"time"

	"github.com/wso2/identity-customer-data-service/internal/system/constants"
)

// ResolveShutdownGracePeriod returns the deadline that bounds the whole
// shutdown sequence. Zero, which is also what an omitted setting gives, returns
// the default. A negative value is refused.
func ResolveShutdownGracePeriod(cfg ShutdownConfig) (time.Duration, error) {

	if cfg.GracePeriodSeconds < 0 {
		return 0, fmt.Errorf("shutdown.grace_period_seconds must not be negative, got %d",
			cfg.GracePeriodSeconds)
	}
	if cfg.GracePeriodSeconds == 0 {
		return constants.DefaultShutdownGracePeriod, nil
	}
	return time.Duration(cfg.GracePeriodSeconds) * time.Second, nil
}
