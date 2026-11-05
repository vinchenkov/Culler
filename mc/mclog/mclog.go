package mclog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const logSuffix = ".ndjson"

type Level uint8

const (
	LevelError Level = iota
	LevelWarn
	LevelInfo
	LevelDebug
)

func ParseLevel(raw string, fallback Level) Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "error":
		return LevelError
	case "warn":
		return LevelWarn
	case "info":
		return LevelInfo
	case "debug":
		return LevelDebug
	}
	return fallback
}

func (l Level) String() string {
	switch l {
	case LevelError:
		return "error"
	case LevelWarn:
		return "warn"
	case LevelInfo:
		return "info"
	case LevelDebug:
		return "debug"
	}
	return "info"
}

type Ctx map[string]any

type logFile struct {
	mu     sync.Mutex
	f      *os.File
	path   string
	closed bool

	maxBytes int64
}

// Logger appends records for one component. A nil *Logger is a no-op.
type Logger struct {
	shared *logFile
	comp   string
	level  Level
	base   Ctx
}

// OpenComponent never creates an absent dir and returns (nil, nil) for it.
func OpenComponent(dir, comp string, level Level) (*Logger, error) {
	if err := validComponent(comp); err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("mclog: log root %s is not a directory", dir)
	}
	path := filepath.Join(dir, comp+logSuffix)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	shared := &logFile{f: f, path: path, maxBytes: ROTATE_MAX_BYTES}
	return &Logger{shared: shared, comp: comp, level: level}, nil
}

func validComponent(comp string) error {
	if comp == "" {
		return errors.New("mclog: empty component name")
	}
	if strings.HasPrefix(comp, ".") {
		return fmt.Errorf("mclog: component %q may not begin with a dot", comp)
	}
	for _, r := range comp {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return fmt.Errorf("mclog: component %q contains %q; want [A-Za-z0-9._-]", comp, r)
		}
	}
	return nil
}

func (l *Logger) Error(event, msg string, ctx Ctx) { l.At(LevelError, event, msg, ctx) }
func (l *Logger) Warn(event, msg string, ctx Ctx)  { l.At(LevelWarn, event, msg, ctx) }
func (l *Logger) Info(event, msg string, ctx Ctx)  { l.At(LevelInfo, event, msg, ctx) }
func (l *Logger) Debug(event, msg string, ctx Ctx) { l.At(LevelDebug, event, msg, ctx) }

func (l *Logger) At(level Level, event, msg string, ctx Ctx) {
	if l == nil || level > l.level {
		return
	}
	line := encodeRecord(time.Now().UTC(), level, l.comp, event, msg, l.base, ctx)

	l.shared.mu.Lock()
	defer l.shared.mu.Unlock()
	if l.shared.closed {
		return
	}
	if _, err := l.shared.f.Write(line); err != nil {
		return
	}
	l.shared.maybeRotate()
}

func (l *Logger) With(ctx Ctx) *Logger {
	if l == nil {
		return nil
	}
	merged := make(Ctx, len(l.base)+len(ctx))
	for k, v := range l.base {
		merged[k] = v
	}
	for k, v := range ctx {
		merged[k] = v
	}
	clone := *l
	clone.base = merged
	return &clone
}

func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.shared.mu.Lock()
	defer l.shared.mu.Unlock()
	if l.shared.closed {
		return nil
	}
	l.shared.closed = true
	return l.shared.f.Close()
}
