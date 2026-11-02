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

package utils

import (
	"fmt"
	"runtime/debug"

	"github.com/wso2/identity-customer-data-service/internal/system/log"
)

// RecoverPanic turns a panic in a background goroutine into a logged error.
//
// net/http recovers panics raised while serving a request, so a handler that dereferences
// a nil profile costs one response. Nothing does that for goroutines: the unification
// consumer, the reindex jobs and the cleanup workers all run on bare `go` statements, where
// an unhandled panic terminates the entire process. Given the stores assert row types
// without checking, a single unexpected NULL would take the service down.
//
// Use as the first statement of any goroutine body:
//
//	go func() {
//	    defer utils.RecoverPanic("profile unification consumer")
//	    ...
//	}()
func RecoverPanic(context string) {
	if r := recover(); r != nil {
		log.GetLogger().Error(fmt.Sprintf("panic recovered in %s: %v\n%s", context, r, debug.Stack()))
	}
}

// SafeGo runs fn on a new goroutine that cannot take the process down with it.
func SafeGo(context string, fn func()) {
	go func() {
		defer RecoverPanic(context)
		fn()
	}()
}
