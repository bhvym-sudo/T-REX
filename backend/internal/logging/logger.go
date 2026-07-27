package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Logger struct {
	mu      sync.RWMutex
	entries []string
	dir     string
	file    *os.File
	sink    func(level, message string)
	closed  bool
}

func New(dir string) (*Logger, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("trex_log_%s.log", time.Now().Format("20060102_150405"))
	file, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	logger := &Logger{dir: dir, file: file}
	logger.Info("Application session started")
	return logger, nil
}

func (l *Logger) SetSink(sink func(level, message string)) {
	l.mu.Lock()
	l.sink = sink
	l.mu.Unlock()
}

func (l *Logger) Info(message string) {
	l.write("INFO", message)
}

func (l *Logger) Error(message string) {
	l.write("ERROR", message)
}

func (l *Logger) write(level, message string) {
	line := fmt.Sprintf("[%s] [%s] %s", time.Now().Format("2006-01-02 15:04:05"), level, message)
	l.mu.Lock()
	l.entries = append(l.entries, line)
	if l.file != nil {
		_, _ = fmt.Fprintln(l.file, line)
		_ = l.file.Sync()
	}
	sink := l.sink
	l.mu.Unlock()
	if sink != nil {
		sink(level, message)
	}
}

func (l *Logger) Entries() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]string(nil), l.entries...)
}

func (l *Logger) Close() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	l.mu.Unlock()
	l.Info("Application session closing")
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}
