/*
 * Copyright (c) 2025-2026, WSO2 LLC. (http://www.wso2.com).
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

package errors

import "fmt"

type ErrorMessage struct {
	Code        string `json:"error_code"`
	Message     string `json:"error_message"`
	Description string `json:"error_description"`
}

type ClientError struct {
	ErrorMessage
	StatusCode int
}

type ServerError struct {
	ErrorMessage
	Err error
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
}

// Unwrap exposes the cause to errors.Is and errors.As, so that a caller can
// tell a cancelled or expired context from a real failure.
func (e *ServerError) Unwrap() error {
	return e.Err
}

func (e *ClientError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func NewServerError(msg ErrorMessage, cause error) *ServerError {
	return &ServerError{
		ErrorMessage: msg,
		Err:          cause,
	}
}

func NewClientError(msg ErrorMessage, code int) *ClientError {
	return &ClientError{
		ErrorMessage: msg,
		StatusCode:   code,
	}
}

func NewClientErrorWithoutCode(msg ErrorMessage) *ClientError {
	return &ClientError{
		ErrorMessage: msg,
	}
}
