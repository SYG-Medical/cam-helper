package logging

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"nystavision/internal/config"
)

const maxLogSizeBytes = 2 * 1024 * 1024

type Logger struct {
	*log.Logger
	file *os.File
	mu   sync.Mutex
}

func New() (*Logger, error) {
	logDir, err := config.LogsDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("create logs dir: %w", err)
	}

	current := filepath.Join(logDir, "agent.log")
	if err := rotateIfNeeded(current); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(current, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	return &Logger{Logger: log.New(f, "", log.LstdFlags|log.Lmicroseconds), file: f}, nil
}

func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	return l.file.Close()
}

func rotateIfNeeded(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat log: %w", err)
	}

	if info.Size() < maxLogSizeBytes {
		return nil
	}

	rotated := path + ".1"
	_ = os.Remove(rotated)
	if err := os.Rename(path, rotated); err != nil {
		return fmt.Errorf("rotate log: %w", err)
	}
	return nil
}
