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

// Package pagination reads page parameters from requests and describes a page of results.
package pagination

import (
	"fmt"
	"net/http"
	"strconv"
)

const (
	defaultPageSize = 5
	maxPageSize     = 200
)

// ParsePageSize reads the page_size query parameter. It returns 5 when the parameter is absent and caps
// the value at 200. Zero is allowed and asks for an empty page; a negative or non-numeric value is an
// error.
func ParsePageSize(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("page_size")

	if raw == "" {
		return defaultPageSize, nil
	}

	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid page size")
	}

	// RFC9865 allows 0 (return empty page)
	if v == 0 {
		return 0, nil
	}

	if v > maxPageSize {
		v = maxPageSize
	}
	return v, nil
}
