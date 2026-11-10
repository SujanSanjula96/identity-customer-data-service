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

// Package rollingfile provides a log file writer that rotates the file each day and when the file
// reaches a size limit. It keeps a limited number of rotated files.
//
// The active file keeps its configured name, for example cds.log. A rotated file gets the date of
// its content and a sequence number, for example cds-2026-11-11.1.log.
package rollingfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultMaxSizeMB is the size limit of the active file when none is configured.
	DefaultMaxSizeMB = 10
	// DefaultMaxBackups is the number of rotated files to keep when none is configured.
	DefaultMaxBackups = 20

	dateLayout = "2006-01-02"
	dirMode    = 0o750
	fileMode   = 0o640
)

// Config configures a Writer.
type Config struct {
	// Path is the absolute path of the active file.
	Path string
	// MaxSizeMB is the size at which the active file rotates. Zero uses DefaultMaxSizeMB.
	MaxSizeMB int
	// MaxBackups is the number of rotated files to keep. Zero uses DefaultMaxBackups.
	MaxBackups int
}

// Writer is an io.WriteCloser that writes to a rotating file. It is safe for concurrent use.
type Writer struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	maxBackups int
	file       *os.File
	size       int64
	day        string
	now        func() time.Time
}

// New opens the active file for append and creates its directory when necessary.
func New(cfg Config) (*Writer, error) {
	if cfg.Path == "" {
		return nil, errors.New("log file path is empty")
	}
	if cfg.MaxSizeMB < 0 || cfg.MaxBackups < 0 {
		return nil, errors.New("log file limits must not be negative")
	}
	if cfg.MaxSizeMB == 0 {
		cfg.MaxSizeMB = DefaultMaxSizeMB
	}
	if cfg.MaxBackups == 0 {
		cfg.MaxBackups = DefaultMaxBackups
	}
	w := &Writer{
		path:       cfg.Path,
		maxBytes:   int64(cfg.MaxSizeMB) * 1024 * 1024,
		maxBackups: cfg.MaxBackups,
		now:        time.Now,
	}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

// Write appends p to the active file. It rotates the file first when the day changed or when p
// would take the file over the size limit. One call writes one record, so a record is never split
// between two files.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return 0, errors.New("log file is closed")
	}
	today := w.now().Format(dateLayout)
	if today != w.day || (w.size > 0 && w.size+int64(len(p)) > w.maxBytes) {
		if err := w.rotate(); err != nil {
			// Keep the records: continue in the current file when rotation fails.
			fmt.Fprintf(os.Stderr, "log file rotation failed for %s: %v\n", w.path, err)
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// Close closes the active file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// open opens the active file and reads its size and the day of its content.
func (w *Writer) open() error {
	if err := os.MkdirAll(filepath.Dir(w.path), dirMode); err != nil {
		return fmt.Errorf("failed to create the log directory: %w", err)
	}
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode) // #nosec G304 -- path is from the deployment configuration.
	if err != nil {
		return fmt.Errorf("failed to open the log file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("failed to read the log file: %w", err)
	}
	w.file = file
	w.size = info.Size()
	w.day = w.now().Format(dateLayout)
	if info.Size() > 0 {
		// A file from an earlier run keeps the day of its last record, so it rotates on the first write.
		w.day = info.ModTime().Format(dateLayout)
	}
	return nil
}

// rotate renames the active file to the next free rotated name, opens a new active file, and
// removes the rotated files that exceed the limit.
func (w *Writer) rotate() error {
	if w.size == 0 {
		w.day = w.now().Format(dateLayout)
		return nil
	}
	if err := w.file.Close(); err != nil {
		return err
	}
	renameErr := os.Rename(w.path, w.nextBackupName())
	if err := w.open(); err != nil {
		return err
	}
	if renameErr != nil {
		return renameErr
	}
	w.prune()
	return nil
}

// nextBackupName returns the first rotated name of the content day that is not in use.
func (w *Writer) nextBackupName() string {
	base, ext := w.split()
	for seq := 1; ; seq++ {
		name := fmt.Sprintf("%s-%s.%d%s", base, w.day, seq, ext)
		if _, err := os.Stat(name); os.IsNotExist(err) {
			return name
		}
	}
}

// prune removes the oldest rotated files until maxBackups remain.
func (w *Writer) prune() {
	base, ext := w.split()
	matches, err := filepath.Glob(base + "-*" + ext)
	if err != nil || len(matches) <= w.maxBackups {
		return
	}
	type backup struct {
		name    string
		modTime time.Time
	}
	backups := make([]backup, 0, len(matches))
	for _, name := range matches {
		if info, err := os.Stat(name); err == nil && !info.IsDir() {
			backups = append(backups, backup{name: name, modTime: info.ModTime()})
		}
	}
	sort.Slice(backups, func(a, b int) bool { return backups[a].modTime.Before(backups[b].modTime) })
	for len(backups) > w.maxBackups {
		_ = os.Remove(backups[0].name)
		backups = backups[1:]
	}
}

// split returns the path without its extension, and the extension.
func (w *Writer) split() (base, ext string) {
	ext = filepath.Ext(w.path)
	return strings.TrimSuffix(w.path, ext), ext
}
