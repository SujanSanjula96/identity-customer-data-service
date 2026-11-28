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
	closed     bool
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

	if w.closed {
		return 0, errors.New("log file is closed")
	}
	if w.file == nil {
		// An earlier rotation could not open the active file. Try again, so that file logging
		// recovers when the cause is gone.
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	today := w.now().Format(dateLayout)
	if today != w.day || (w.size > 0 && w.size+int64(len(p)) > w.maxBytes) {
		if err := w.rotate(); err != nil {
			// Keep the records: continue in the current file when rotation fails.
			fmt.Fprintf(os.Stderr, "log file rotation failed for %s: %v\n", w.path, err)
		}
	}
	if w.file == nil {
		return 0, errors.New("log file is not open")
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// Close closes the active file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true
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
	// #nosec G304 -- the path is from the deployment configuration.
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
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
// removes the rotated files that exceed the limit. If it cannot open the new active file, w.file
// is nil, and the next Write tries to open it again.
func (w *Writer) rotate() error {
	if w.size == 0 {
		w.day = w.now().Format(dateLayout)
		return nil
	}
	backupName, err := w.nextBackupName()
	if err != nil {
		// Keep the current file open, so that the records continue in it.
		return err
	}
	closeErr := w.file.Close()
	w.file = nil
	renameErr := os.Rename(w.path, backupName)
	if err := w.open(); err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if renameErr != nil {
		return renameErr
	}
	w.prune()
	return nil
}

// nextBackupName returns the first rotated name of the content day that is not in use. It
// returns an error when it cannot check a name, for example when it has no permission.
func (w *Writer) nextBackupName() (string, error) {
	base, ext := w.split()
	for seq := 1; ; seq++ {
		name := fmt.Sprintf("%s-%s.%d%s", base, w.day, seq, ext)
		_, err := os.Stat(name)
		if os.IsNotExist(err) {
			return name, nil
		}
		if err != nil {
			return "", fmt.Errorf("failed to check the rotated file name %s: %w", name, err)
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
