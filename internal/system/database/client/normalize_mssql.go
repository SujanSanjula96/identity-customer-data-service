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

func normalizeMSSQLValue(value interface{}, declaredType, column string) interface{} {
	if value == nil {
		return nil
	}

	if jsonColumns[strings.ToLower(column)] {
		if text, ok := value.(string); ok {
			return []byte(text)
		}
		return value
	}

	switch strings.ToUpper(strings.TrimSpace(declaredType)) {
	case "BIT":
		if boolean, ok := boolFromValue(value); ok {
			return boolean
		}
	case "CHAR", "VARCHAR", "NCHAR", "NVARCHAR", "TEXT", "NTEXT":
		return textFromValue(value)
	case "TINYINT", "SMALLINT", "INT", "BIGINT":
		return integerFromValue(value)
	case columnTypeDate, columnTypeDatetime, "DATETIME2", "SMALLDATETIME", "DATETIMEOFFSET":
		return timeFromValue(value)
	}

	return value
}
