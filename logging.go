package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// rotatingLogWriter keeps file logging bounded. It opens files only while a
// log record is written, so an idle daemon holds no log file descriptor.
type rotatingLogWriter struct {
	mu            sync.Mutex
	directory     string
	pattern       string
	format        string
	rotate        bool
	maxBytes      int64
	maxFiles      int
	retentionDays int
}

func configureLogging(cfg loggingConfig, home string) (*rotatingLogWriter, error) {
	if cfg.Directory == "" {
		return nil, nil
	}
	if cfg.FilePattern == "" {
		cfg.FilePattern = "jvm-oom-guardian-20060102.log"
	}
	if cfg.Format == "" {
		cfg.Format = "text"
	}
	if cfg.Format != "text" && cfg.Format != "json" {
		return nil, fmt.Errorf("logging.format must be text or json")
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 10 * 1024 * 1024
	}
	if cfg.MaxFiles == 0 {
		cfg.MaxFiles = 7
	}
	if cfg.RetentionDays == 0 {
		cfg.RetentionDays = 30
	}
	if cfg.MaxBytes < 0 || cfg.MaxFiles < 1 || cfg.RetentionDays < 0 {
		return nil, fmt.Errorf("logging retention settings must be non-negative")
	}
	directory := expandHome(cfg.Directory, home)
	rotate := true
	if cfg.Rotate != nil {
		rotate = *cfg.Rotate
	}
	writer, err := newRollingLogWriter(directory, cfg.FilePattern, cfg.Format, rotate, cfg.MaxBytes, cfg.MaxFiles, cfg.RetentionDays)
	if err != nil {
		return nil, err
	}
	log.SetOutput(io.MultiWriter(os.Stderr, writer))
	_ = writer.cleanup(time.Now())
	return writer, nil
}

func newRollingLogWriter(directory, pattern, format string, rotate bool, maxBytes int64, maxFiles, retentionDays int) (*rotatingLogWriter, error) {
	if filepath.Base(pattern) != pattern {
		return nil, fmt.Errorf("logging.file_pattern must be a filename, not a path")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	return &rotatingLogWriter{
		directory:     directory,
		pattern:       pattern,
		format:        format,
		rotate:        rotate,
		maxBytes:      maxBytes,
		maxFiles:      maxFiles,
		retentionDays: retentionDays,
	}, nil
}

func (w *rotatingLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	originalLen := len(p)
	now := time.Now()
	path := filepath.Join(w.directory, now.Format(w.pattern))
	if w.format == "json" {
		payload, err := json.Marshal(map[string]string{
			"timestamp": now.Format(time.RFC3339Nano),
			"message":   strings.TrimSuffix(string(p), "\n"),
		})
		if err != nil {
			return 0, err
		}
		p = append(payload, '\n')
	}
	if w.rotate && w.maxBytes > 0 {
		if info, err := os.Stat(path); err == nil && info.Size()+int64(len(p)) > w.maxBytes {
			if err := w.rotateFile(path); err != nil {
				return 0, err
			}
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}
	n, writeErr := file.Write(p)
	closeErr := file.Close()
	if writeErr != nil {
		return n, writeErr
	}
	if closeErr != nil {
		return n, closeErr
	}
	_ = w.cleanup(now)
	if w.format == "json" {
		return originalLen, nil
	}
	return n, nil
}

// OpenForCommand rotates/cleans the common log and returns a real file. Using
// *os.File here is important: os/exec does not create an internal pipe and
// copy goroutine for child processes that daemonize and inherit stdout/stderr.
func (w *rotatingLogWriter) OpenForCommand() (*os.File, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	path := filepath.Join(w.directory, now.Format(w.pattern))
	if w.rotate && w.maxBytes > 0 {
		if info, err := os.Stat(path); err == nil && info.Size() >= w.maxBytes {
			if err := w.rotateFile(path); err != nil {
				return nil, err
			}
		}
	}
	if err := w.cleanup(now); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
}

func (w *rotatingLogWriter) runCleanup(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			w.mu.Lock()
			_ = w.cleanup(now)
			w.mu.Unlock()
		}
	}
}

func (w *rotatingLogWriter) rotateFile(path string) error {
	for i := w.maxFiles - 1; i >= 1; i-- {
		oldPath := fmt.Sprintf("%s.%d", path, i)
		newPath := fmt.Sprintf("%s.%d", path, i+1)
		if _, err := os.Stat(oldPath); err == nil {
			if err := os.Rename(oldPath, newPath); err != nil {
				return err
			}
		}
	}
	if w.maxFiles > 0 {
		if err := os.Rename(path, path+".1"); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (w *rotatingLogWriter) cleanup(now time.Time) error {
	entries, err := os.ReadDir(w.directory)
	if err != nil {
		return err
	}
	prefix := strings.Split(w.pattern, "2006")[0]
	var candidates []os.DirEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		candidates = append(candidates, entry)
	}
	for _, entry := range candidates {
		info, infoErr := entry.Info()
		if infoErr == nil && w.retentionDays > 0 && now.Sub(info.ModTime()) > time.Duration(w.retentionDays)*24*time.Hour {
			_ = os.Remove(filepath.Join(w.directory, entry.Name()))
		}
	}
	if w.maxFiles > 0 && len(candidates) > w.maxFiles {
		sort.Slice(candidates, func(i, j int) bool {
			iInfo, iErr := candidates[i].Info()
			jInfo, jErr := candidates[j].Info()
			if iErr != nil || jErr != nil {
				return iErr == nil
			}
			return iInfo.ModTime().Before(jInfo.ModTime())
		})
		for _, entry := range candidates[:len(candidates)-w.maxFiles] {
			_ = os.Remove(filepath.Join(w.directory, entry.Name()))
		}
	}
	return nil
}
