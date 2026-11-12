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
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func encodeRaw(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func TestProfileCursorRoundTrip(t *testing.T) {
	created := time.Date(2026, 3, 4, 5, 6, 7, 123456789, time.UTC)
	for _, direction := range []string{"next", "prev"} {
		encoded := EncodeProfileCursor(ProfileCursor{CreatedAt: created, ProfileId: "p-1", Direction: direction})

		decoded, err := DecodeProfileCursor(encoded)

		require.NoError(t, err)
		assert.True(t, created.Equal(decoded.CreatedAt))
		assert.Equal(t, "p-1", decoded.ProfileId)
		assert.Equal(t, direction, decoded.Direction)
	}
}

func TestEncodeProfileCursorDefaultsDirectionToNext(t *testing.T) {
	encoded := EncodeProfileCursor(ProfileCursor{CreatedAt: time.Unix(0, 0), ProfileId: "p-1", Direction: "  "})

	decoded, err := DecodeProfileCursor(encoded)

	require.NoError(t, err)
	assert.Equal(t, "next", decoded.Direction)
}

func TestEncodeProfileCursorConvertsToUTC(t *testing.T) {
	zone := time.FixedZone("plus5", 5*60*60)
	created := time.Date(2026, 1, 1, 10, 0, 0, 0, zone)

	decoded, err := DecodeProfileCursor(EncodeProfileCursor(ProfileCursor{CreatedAt: created, ProfileId: "p"}))

	require.NoError(t, err)
	assert.Equal(t, time.UTC, decoded.CreatedAt.Location())
	assert.True(t, created.Equal(decoded.CreatedAt))
}

func TestDecodeProfileCursorEmptyIsNoCursor(t *testing.T) {
	for _, in := range []string{"", "   "} {
		decoded, err := DecodeProfileCursor(in)

		assert.NoError(t, err)
		assert.Nil(t, decoded)
	}
}

func TestDecodeProfileCursorAcceptsTwoParts(t *testing.T) {
	decoded, err := DecodeProfileCursor(encodeRaw("2026-01-01T00:00:00Z|p-9"))

	require.NoError(t, err)
	assert.Equal(t, "p-9", decoded.ProfileId)
	assert.Equal(t, "next", decoded.Direction)
}

func TestDecodeProfileCursorEmptyDirectionDefaultsToNext(t *testing.T) {
	decoded, err := DecodeProfileCursor(encodeRaw("2026-01-01T00:00:00Z|p-9| "))

	require.NoError(t, err)
	assert.Equal(t, "next", decoded.Direction)
}

func TestDecodeProfileCursorRejectsBadInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"not base64", "!!!not-base64!!!", "invalid cursor encoding"},
		{"one part", encodeRaw("only-one"), "invalid cursor format"},
		{"four parts", encodeRaw("a|b|c|d"), "invalid cursor format"},
		{"bad timestamp", encodeRaw("yesterday|p-1|next"), "invalid cursor timestamp"},
		{"empty profile id", encodeRaw("2026-01-01T00:00:00Z| |next"), "invalid cursor profile_id"},
		{"unknown direction", encodeRaw("2026-01-01T00:00:00Z|p-1|sideways"), "invalid cursor direction"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decoded, err := DecodeProfileCursor(tc.input)

			assert.Nil(t, decoded)
			assert.EqualError(t, err, tc.want)
		})
	}
}
