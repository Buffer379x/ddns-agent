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
	slog   *slog.Logger
	store  LogStore
	sse    SSEBroadcaster
	mu     sync.RWMutex
}

func New() *Logger {
	return &Logger{
		slog: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func (l *Logger) SetStore(store LogStore)       { l.mu.Lock(); l.store = store; l.mu.Unlock() }
func (l *Logger) SetSSE(sse SSEBroadcaster)     { l.mu.Lock(); l.sse = sse; l.mu.Unlock() }

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
	store := l.store
	sse := l.sse
	l.mu.RUnlock()

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
