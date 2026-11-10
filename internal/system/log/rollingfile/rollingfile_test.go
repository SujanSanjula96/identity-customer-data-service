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

package rollingfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func backups(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "cds-*.log"))
	require.NoError(t, err)
	return matches
}

func TestRotatesAtSizeLimit(t *testing.T) {
	dir := t.TempDir()
	writer, err := New(Config{Path: filepath.Join(dir, "cds.log"), MaxSizeMB: 1})
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	record := []byte(strings.Repeat("x", 600*1024) + "\n")
	for i := 0; i < 3; i++ {
		_, err = writer.Write(record)
		require.NoError(t, err)
	}

	assert.Len(t, backups(t, dir), 2, "each record that does not fit starts a new file")
	active, err := os.Stat(filepath.Join(dir, "cds.log"))
	require.NoError(t, err)
	assert.Equal(t, int64(len(record)), active.Size(), "a record is never split between two files")
}

func TestRotatesWhenTheDayChanges(t *testing.T) {
	dir := t.TempDir()
	writer, err := New(Config{Path: filepath.Join(dir, "cds.log")})
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	day := time.Date(2026, 11, 11, 23, 59, 0, 0, time.UTC)
	writer.now = func() time.Time { return day }
	writer.day = day.Format(dateLayout)
	_, err = writer.Write([]byte("first day\n"))
	require.NoError(t, err)

	writer.now = func() time.Time { return day.Add(2 * time.Minute) }
	_, err = writer.Write([]byte("second day\n"))
	require.NoError(t, err)

	rotated := backups(t, dir)
	require.Len(t, rotated, 1)
	assert.Equal(t, "cds-2026-11-11.1.log", filepath.Base(rotated[0]), "the name has the date of the content")
}

func TestKeepsOnlyMaxBackups(t *testing.T) {
	dir := t.TempDir()
	writer, err := New(Config{Path: filepath.Join(dir, "cds.log"), MaxSizeMB: 1, MaxBackups: 2})
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()

	record := []byte(strings.Repeat("x", 600*1024) + "\n")
	for i := 0; i < 6; i++ {
		_, err = writer.Write(record)
		require.NoError(t, err)
	}

	assert.Len(t, backups(t, dir), 2)
}

func TestInvalidConfigIsRefused(t *testing.T) {
	_, err := New(Config{})
	assert.Error(t, err)

	_, err = New(Config{Path: filepath.Join(t.TempDir(), "cds.log"), MaxSizeMB: -1})
	assert.Error(t, err)
}
