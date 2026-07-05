package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type requestLogger struct {
	stderr    io.Writer
	verbosity int
	file      *os.File
	path      string
}

type apiLogEntry struct {
	Method       string
	Path         string
	Status       int
	Duration     time.Duration
	Summary      string
	RequestBody  []byte
	ResponseBody []byte
}

func newRequestLogger(stderr io.Writer, verbosity int) (*requestLogger, error) {
	path, err := linearLogPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create Linear log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Linear log: %w", err)
	}
	return &requestLogger{
		stderr:    stderr,
		verbosity: verbosity,
		file:      file,
		path:      path,
	}, nil
}

func (l *requestLogger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

func (l *requestLogger) LogAPICall(entry apiLogEntry) {
	if l == nil {
		return
	}
	fields := map[string]any{
		"time":        time.Now().Format(time.RFC3339),
		"method":      entry.Method,
		"path":        entry.Path,
		"status":      entry.Status,
		"duration_ms": entry.Duration.Milliseconds(),
	}
	if entry.Summary != "" {
		fields["summary"] = entry.Summary
	}
	if l.verbosity >= 2 {
		if len(entry.RequestBody) > 0 {
			fields["request_body"] = bodyForLog(entry.RequestBody)
		}
		if len(entry.ResponseBody) > 0 {
			fields["response_body"] = bodyForLog(entry.ResponseBody)
		}
	}
	line, err := json.Marshal(fields)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(l.file, string(line))
	if l.verbosity > 0 && l.stderr != nil {
		_, _ = fmt.Fprintln(l.stderr, string(line))
	}
}

func (l *requestLogger) Warn(message string) {
	message = strings.TrimSpace(message)
	if l == nil || message == "" {
		return
	}
	if l.stderr != nil {
		_, _ = fmt.Fprintf(l.stderr, "linear: %s\n", message)
	}
	l.logDiagnostic("warn", message)
}

func (l *requestLogger) LogDiagnostic(level, message string) {
	message = strings.TrimSpace(message)
	if l == nil || message == "" {
		return
	}
	l.logDiagnostic(level, message)
}

func (l *requestLogger) logDiagnostic(level, message string) {
	fields := map[string]any{
		"time":    time.Now().Format(time.RFC3339),
		"level":   strings.TrimSpace(level),
		"message": message,
	}
	line, err := json.Marshal(fields)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(l.file, string(line))
}

func bodyForLog(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return ""
	}
	var compact bytes.Buffer
	if json.Compact(&compact, body) == nil {
		return compact.String()
	}
	return string(body)
}
