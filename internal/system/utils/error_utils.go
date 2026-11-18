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

package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/pkg/errors"

	cdsErrors "github.com/wso2/identity-customer-data-service/internal/system/errors"
)

// HandleDecodeError turns a JSON decode error of a request body into a message for the client. It
// returns an empty string for a nil error.
func HandleDecodeError(err error, resourceName string) string {
	if err == nil {
		return ""
	}

	var ute *json.UnmarshalTypeError
	var se *json.SyntaxError

	switch {
	case errors.Is(err, io.EOF):
		return fmt.Sprintf("Request body for %s is empty.", resourceName)

	case errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Sprintf("Malformed JSON in %s request body (unexpected end of input).", resourceName)

	case strings.HasPrefix(err.Error(), "json: unknown field "):
		field := strings.TrimPrefix(err.Error(), "json: unknown field ")
		return fmt.Sprintf("Unknown field %s in %s request body.", field, resourceName)

	case errors.As(err, &ute):
		// Top-level type mismatch (e.g., expected array but got object)
		if ute.Field == "" {
			if ute.Type != nil && ute.Type.Kind() == reflect.Slice {
				return fmt.Sprintf("Request body for %s must be a JSON array.", resourceName)
			}
			return fmt.Sprintf("Invalid JSON type for %s request body.", resourceName)
		}

		return fmt.Sprintf(
			"Invalid type for field '%s'. Expected %s in %s request body.", ute.Field, ute.Type, resourceName,
		)

	case errors.As(err, &se):
		return fmt.Sprintf(
			"Malformed JSON in %s request body.", resourceName,
		)

	default:
		return fmt.Sprintf("Invalid JSON payload for %s.", resourceName)
	}
}

// HasClientErrorCode checks whether the given error is a ClientError with the given code.
func HasClientErrorCode(err error, code string) bool {
	if err == nil {
		return false
	}
	var clientErr *cdsErrors.ClientError
	if errors.As(err, &clientErr) {
		return clientErr.ErrorMessage.Code == code
	}
	return false
}
