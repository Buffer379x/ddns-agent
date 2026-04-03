package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// renameLog moves src to dst; if rename fails (e.g. dst exists on some FS), remove dst and retry once.
func renameLog(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) {
		return err
	}
	_ = os.Remove(dst)
	return os.Rename(src, dst)
}

// FileRotatingWriter appends to agent.log under logDir. When the calendar day in loc
// changes, the current file is renamed to agent-YYYY-MM-DD.log and a new agent.log is started.
// Archived agent-*.log files older than retentionDays are removed.
type FileRotatingWriter struct {
	dir         string
	activePath  string
	retention   int
	loc         *time.Location
	mu          sync.Mutex
	f           *os.File
	currentDay  string
	initialized bool
}

// NewFileRotatingWriter creates a writer; the first WriteLine creates/opens files.
// tz is used for “today” and archive filenames; nil uses time.Local.
func NewFileRotatingWriter(logDir string, retentionDays int, tz *time.Location) *FileRotatingWriter {
	if retentionDays < 1 {
		retentionDays = 7
	}
	if tz == nil {
		tz = time.Local
	}
	return &FileRotatingWriter{
		dir:        logDir,
		activePath: filepath.Join(logDir, "agent.log"),
		retention:  retentionDays,
		loc:        tz,
	}
}

// WriteLine appends one line (should end with \n) after applying daily rotation.
func (w *FileRotatingWriter) WriteLine(line string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	today := time.Now().In(w.loc).Format("2006-01-02")

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
		fileDay := st.ModTime().In(w.loc).Format("2006-01-02")
		if fileDay != today {
			archive := filepath.Join(w.dir, fmt.Sprintf("agent-%s.log", fileDay))
			if err := renameLog(w.activePath, archive); err != nil {
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
	if _, err := os.Stat(w.activePath); os.IsNotExist(err) {
		w.currentDay = today
		w.pruneArchives()
		return w.openAppend()
	}
	archive := filepath.Join(w.dir, fmt.Sprintf("agent-%s.log", w.currentDay))
	if err := renameLog(w.activePath, archive); err != nil {
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
	now := time.Now().In(w.loc)
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, w.loc).AddDate(0, 0, -w.retention)
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
		t, err := time.ParseInLocation("2006-01-02", mid, w.loc)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			_ = os.Remove(filepath.Join(w.dir, name))
		}
	}
}

// SetLocation updates the timezone used for day boundaries and archive names.
func (w *FileRotatingWriter) SetLocation(loc *time.Location) {
	if loc == nil {
		loc = time.Local
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loc = loc
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
