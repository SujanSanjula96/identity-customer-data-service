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

package scripts

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wso2/identity-customer-data-service/internal/system/database"
	"github.com/wso2/identity-customer-data-service/internal/system/database/model"
)

// BuildFindOldestReferenceProfileIDByAttributeValuesQuery builds a bounded
// profile lookup with string equality/array overlap semantics. Only the two
// supported column names enter SQL; paths and values are encoded and bound as
// data, including quotes and JSON metacharacters. Arguments $1-$3 are the
// organization, excluded profile ID, and incoming profile user ID.
func BuildFindOldestReferenceProfileIDByAttributeValuesQuery(
	dbType, property string, values []string) (model.DBQuery, []interface{}, error) {
	parts := strings.Split(property, ".")
	if len(parts) < 2 || (parts[0] != "traits" && parts[0] != "identity_attributes") {
		return model.DBQuery{}, nil, fmt.Errorf("unsupported profile property %q", property)
	}
	for _, part := range parts[1:] {
		if part == "" {
			return model.DBQuery{}, nil, fmt.Errorf("empty key in profile property %q", property)
		}
	}
	if len(values) == 0 {
		return model.DBQuery{}, nil, fmt.Errorf("profile lookup requires at least one string value")
	}
	column := "p." + parts[0]
	query := FindOldestReferenceProfileIDByAttributeValues.Format(column)
	if dbType == database.TypeSQLite {
		pathJSON, _ := json.Marshal(parts[1:])
		valuesJSON, _ := json.Marshal(values)
		return query,
			[]interface{}{string(pathJSON), string(valuesJSON)}, nil
	}

	// @? supplies an indexable prefilter. The exact check unwraps the leaf
	// once because lax JSONPath equality alone also matches nested arrays.
	var path strings.Builder
	path.WriteString("$")
	for _, part := range parts[1:] {
		key, _ := json.Marshal(part)
		path.WriteByte('.')
		path.Write(key)
	}
	leafPath := path.String()
	predicates := make([]string, 0, len(values))
	for _, value := range values {
		encoded, _ := json.Marshal(value)
		predicates = append(predicates, "@ == "+string(encoded))
	}
	path.WriteString(" ? (")
	path.WriteString(strings.Join(predicates, " || "))
	path.WriteByte(')')
	valuesJSON, _ := json.Marshal(values)
	return query,
		[]interface{}{path.String(), leafPath, string(valuesJSON)}, nil
}
