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

package errors

import (
	"errors"
	"testing"
)

var testMessage = ErrorMessage{
	Code:        "CDS-TST-01",
	Message:     "The operation failed.",
	Description: "A test operation failed.",
}

func Test_AsServerError(t *testing.T) {

	cause := errors.New("the datasource failed")

	t.Run("nil stays nil", func(t *testing.T) {
		if got := AsServerError(nil, testMessage); got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})

	t.Run("a plain error takes the given code", func(t *testing.T) {
		got := AsServerError(cause, testMessage)

		var serverError *ServerError
		if !errors.As(got, &serverError) {
			t.Fatalf("got %v, want a ServerError", got)
		}
		if serverError.Code != testMessage.Code {
			t.Fatalf("got code %s, want %s", serverError.Code, testMessage.Code)
		}
		if !errors.Is(got, cause) {
			t.Fatal("the cause was lost")
		}
	})

	t.Run("an existing server error keeps its own code", func(t *testing.T) {
		want := NewServerError(ErrorMessage{Code: "CDS-TST-02"}, cause)

		got := AsServerError(want, testMessage)
		if got != error(want) {
			t.Fatalf("got %v, want the error unchanged", got)
		}
	})

	t.Run("a server error inside a join keeps its own code", func(t *testing.T) {
		inner := NewServerError(ErrorMessage{Code: "CDS-TST-03"}, cause)
		joined := errors.Join(inner, errors.New("the rollback failed"))

		got := AsServerError(joined, testMessage)

		var serverError *ServerError
		if !errors.As(got, &serverError) {
			t.Fatalf("got %v, want a ServerError", got)
		}
		if serverError.Code != "CDS-TST-03" {
			t.Fatalf("got code %s, want CDS-TST-03", serverError.Code)
		}
	})
}
