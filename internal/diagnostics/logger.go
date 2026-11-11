package diagnostics

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"
)

const (
	DefaultMaxBytes = 1 << 20
	DefaultBackups  = 3
)

type Level string

const (
	LevelInfo    Level = "info"
	LevelWarning Level = "warning"
	LevelError   Level = "error"
)

type Event struct {
	Level     Level
	Operation string
	Code      string
}

type record struct {
	Time      time.Time `json:"time"`
	Level     Level     `json:"level"`
	Operation string    `json:"operation"`
	Code      string    `json:"code"`
}

type Logger struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	backups  int
	file     *os.File
	size     int64
	now      func() time.Time
}

var metadataPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

func Open(path string) (*Logger, error) {
	return OpenWithLimits(path, DefaultMaxBytes, DefaultBackups)
}

func OpenWithLimits(path string, maxBytes int64, backups int) (*Logger, error) {
	if path == "" {
		return nil, errors.New("log path is unavailable")
	}
	if maxBytes <= 0 {
		return nil, errors.New("log size limit must be positive")
	}
	if backups < 0 {
		return nil, errors.New("log backup count cannot be negative")
	}
	logger := &Logger{path: path, maxBytes: maxBytes, backups: backups, now: time.Now}
	if err := logger.open(); err != nil {
		return nil, err
	}
	return logger, nil
}

func (l *Logger) Record(event Event) error {
	if event.Level != LevelInfo && event.Level != LevelWarning && event.Level != LevelError {
		return errors.New("invalid log level")
	}
	if !metadataPattern.MatchString(event.Operation) || !metadataPattern.MatchString(event.Code) {
		return errors.New("log operation and code must be fixed lowercase metadata")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return errors.New("logger is closed")
	}
	encoded, err := json.Marshal(record{
		Time:      l.now().UTC(),
		Level:     event.Level,
		Operation: event.Operation,
		Code:      event.Code,
	})
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if l.size > 0 && l.size+int64(len(encoded)) > l.maxBytes {
		if err := l.rotate(); err != nil {
			return fmt.Errorf("rotate log: %w", err)
		}
	}
	if _, err := l.file.Write(encoded); err != nil {
		return fmt.Errorf("write log: %w", err)
	}
	l.size += int64(len(encoded))
	return nil
}

func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *Logger) open() error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	file, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("inspect log: %w", err)
	}
	l.file = file
	l.size = info.Size()
	return nil
}

func (l *Logger) rotate() error {
	if err := l.file.Close(); err != nil {
		return err
	}
	l.file = nil
	if l.backups == 0 {
		if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			l.open()
			return err
		}
	} else {
		oldest := l.path + "." + strconv.Itoa(l.backups)
		if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
			l.open()
			return err
		}
		for index := l.backups - 1; index >= 1; index-- {
			source := l.path + "." + strconv.Itoa(index)
			destination := l.path + "." + strconv.Itoa(index+1)
			if err := os.Rename(source, destination); err != nil && !errors.Is(err, os.ErrNotExist) {
				l.open()
				return err
			}
		}
		if err := os.Rename(l.path, l.path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
			l.open()
			return err
		}
	}
	l.size = 0
	return l.open()
}
