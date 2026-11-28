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

package client

import "strings"

func normalizeMySQLValue(value interface{}, declaredType, column string) interface{} {
	if value == nil {
		return nil
	}

	typeName := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(declaredType)), "UNSIGNED ")
	switch typeName {
	case "CHAR", "VARCHAR", "TINYTEXT", "TEXT", "MEDIUMTEXT", "LONGTEXT":
		return textFromValue(value)
	case "JSON":
		if text, ok := value.(string); ok {
			return []byte(text)
		}
	case "TINYINT":
		if booleanColumns[strings.ToLower(column)] {
			if boolean, ok := boolFromValue(value); ok {
				return boolean
			}
			return value
		}
		return integerFromValue(value)
	case "SMALLINT", "MEDIUMINT", "INT", "BIGINT", "YEAR":
		return integerFromValue(value)
	case "FLOAT", "DOUBLE":
		return floatFromValue(value)
	case columnTypeDatetime, "TIMESTAMP", columnTypeDate:
		return timeFromValue(value)
	}

	return value
}
