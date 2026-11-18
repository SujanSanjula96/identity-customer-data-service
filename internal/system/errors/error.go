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

// Package errors defines the client and server errors CDS returns, and their error messages.
package errors

import "fmt"

// ErrorMessage is the code, message and description of an error, as the API returns it.
type ErrorMessage struct {
	Code        string `json:"error_code"`
	Message     string `json:"error_message"`
	Description string `json:"error_description"`
}

// ClientError is an error caused by the request, with the HTTP status to return.
type ClientError struct {
	ErrorMessage
	StatusCode int
}

// ServerError is an error inside CDS or a service it depends on, with the underlying cause.
type ServerError struct {
	ErrorMessage
	Err error
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
}

func (e *ClientError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// NewServerError returns a ServerError with the message and its cause.
func NewServerError(msg ErrorMessage, cause error) *ServerError {
	return &ServerError{
		ErrorMessage: msg,
		Err:          cause,
	}
}

// NewClientError returns a ClientError with the message and HTTP status.
func NewClientError(msg ErrorMessage, code int) *ClientError {
	return &ClientError{
		ErrorMessage: msg,
		StatusCode:   code,
	}
}

// NewClientErrorWithoutCode returns a ClientError with no HTTP status set.
func NewClientErrorWithoutCode(msg ErrorMessage) *ClientError {
	return &ClientError{
		ErrorMessage: msg,
	}
}
