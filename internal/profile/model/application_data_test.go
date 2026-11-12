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
package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplicationDataMarshalJSONFlattensAppData(t *testing.T) {
	data := ApplicationData{
		AppId:           "app-1",
		AppSpecificData: map[string]interface{}{"theme": "dark", "visits": 3},
	}

	out, err := json.Marshal(data)

	require.NoError(t, err)
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Equal(t, map[string]interface{}{"application_id": "app-1", "theme": "dark", "visits": float64(3)}, got)
}

func TestApplicationDataMarshalJSONWithoutAppData(t *testing.T) {
	out, err := json.Marshal(ApplicationData{AppId: "app-1"})

	require.NoError(t, err)
	assert.JSONEq(t, `{"application_id":"app-1"}`, string(out))
}
