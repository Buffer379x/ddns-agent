package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileRotatingWriter appends to agent.log under logDir. When the local calendar day
// changes, the current file is renamed to agent-YYYY-MM-DD.log and a new agent.log is started.
// Archived agent-*.log files older than retentionDays are removed.
type FileRotatingWriter struct {
	dir         string
	activePath  string
	retention   int
	mu          sync.Mutex
	f           *os.File
	currentDay  string
	initialized bool
}

// NewFileRotatingWriter creates a writer; the first WriteLine creates/opens files.
func NewFileRotatingWriter(logDir string, retentionDays int) *FileRotatingWriter {
	if retentionDays < 1 {
		retentionDays = 7
	}
	return &FileRotatingWriter{
		dir:       logDir,
		activePath: filepath.Join(logDir, "agent.log"),
		retention: retentionDays,
	}
}

// WriteLine appends one line (should end with \n) after applying daily rotation.
func (w *FileRotatingWriter) WriteLine(line string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	today := time.Now().Local().Format("2006-01-02")

	if !w.initialized {
		if err := w.bootstrap(today); err != nil {
			return err
		}
		w.initialized = true
	} else if w.currentDay != today {
		if err := w.rotateForNewDay(today); err != nil {
			return err
		}
	}

	if w.f == nil {
		if err := w.openAppend(); err != nil {
			return err
		}
	}
	_, err := w.f.WriteString(line)
	return err
}

func (w *FileRotatingWriter) bootstrap(today string) error {
	if err := os.MkdirAll(w.dir, 0755); err != nil {
		return err
	}

	st, err := os.Stat(w.activePath)
	if err == nil {
		fileDay := st.ModTime().Local().Format("2006-01-02")
		if fileDay != today {
			archive := filepath.Join(w.dir, fmt.Sprintf("agent-%s.log", fileDay))
			if err := os.Rename(w.activePath, archive); err != nil {
				return err
			}
		}
	}

	w.currentDay = today
	w.pruneArchives()
	return w.openAppend()
}

func (w *FileRotatingWriter) rotateForNewDay(today string) error {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	archive := filepath.Join(w.dir, fmt.Sprintf("agent-%s.log", w.currentDay))
	if err := os.Rename(w.activePath, archive); err != nil && !os.IsNotExist(err) {
		return err
	}
	w.currentDay = today
	w.pruneArchives()
	return w.openAppend()
}

func (w *FileRotatingWriter) openAppend() error {
	f, err := os.OpenFile(w.activePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	w.f = f
	return nil
}

func (w *FileRotatingWriter) pruneArchives() {
	cutoff := time.Now().Local().Truncate(24 * time.Hour).AddDate(0, 0, -w.retention)
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if name == "agent.log" {
			continue
		}
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		mid := strings.TrimPrefix(name, "agent-")
		mid = strings.TrimSuffix(mid, ".log")
		t, err := time.ParseInLocation("2006-01-02", mid, time.Local)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			_ = os.Remove(filepath.Join(w.dir, name))
		}
	}
}

// Close releases the open log file.
func (w *FileRotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
