package logger

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

type Level string

const (
	LevelInfo    Level = "INFO"
	LevelWarn    Level = "WARNING"
	LevelError   Level = "ERROR"
	LevelSuccess Level = "SUCCESS"
)

type LogEntry struct {
	Level     Level     `json:"level"`
	Source    string    `json:"source"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type LogStore interface {
	InsertLog(level, source, message string) error
}

type SSEBroadcaster interface {
	BroadcastLog(entry LogEntry)
}

type Logger struct {
	slog    *slog.Logger
	store   LogStore
	sse     SSEBroadcaster
	fileLog *FileRotatingWriter
	mu      sync.RWMutex
}

func New() *Logger {
	return &Logger{
		slog: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func (l *Logger) SetStore(store LogStore)   { l.mu.Lock(); l.store = store; l.mu.Unlock() }
func (l *Logger) SetSSE(sse SSEBroadcaster) { l.mu.Lock(); l.sse = sse; l.mu.Unlock() }

// SetFileLog enables daily-rotated file logging under logDir (e.g. /data/logs/agent.log).
func (l *Logger) SetFileLog(logDir string, retentionDays int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fileLog = NewFileRotatingWriter(logDir, retentionDays)
}

// CloseFileLog closes the rotating file handle (e.g. on shutdown).
func (l *Logger) CloseFileLog() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fileLog == nil {
		return nil
	}
	err := l.fileLog.Close()
	l.fileLog = nil
	return err
}

func (l *Logger) log(level Level, source, message string) {
	entry := LogEntry{
		Level:     level,
		Source:    source,
		Message:   message,
		CreatedAt: time.Now(),
	}

	switch level {
	case LevelError:
		l.slog.Error(message, "source", source)
	case LevelWarn:
		l.slog.Warn(message, "source", source)
	default:
		l.slog.Info(message, "source", source)
	}

	l.mu.RLock()
	fileLog := l.fileLog
	store := l.store
	sse := l.sse
	l.mu.RUnlock()

	if fileLog != nil {
		line := fmt.Sprintf("%s %-7s [%s] %s\n", entry.CreatedAt.Format("2006-01-02 15:04:05"), level, source, message)
		_ = fileLog.WriteLine(line)
	}

	if store != nil {
		_ = store.InsertLog(string(level), source, message)
	}
	if sse != nil {
		sse.BroadcastLog(entry)
	}
}

func (l *Logger) Info(source, msg string, args ...any) {
	l.log(LevelInfo, source, fmt.Sprintf(msg, args...))
}

func (l *Logger) Warn(source, msg string, args ...any) {
	l.log(LevelWarn, source, fmt.Sprintf(msg, args...))
}

func (l *Logger) Error(source, msg string, args ...any) {
	l.log(LevelError, source, fmt.Sprintf(msg, args...))
}

func (l *Logger) Success(source, msg string, args ...any) {
	l.log(LevelSuccess, source, fmt.Sprintf(msg, args...))
}
