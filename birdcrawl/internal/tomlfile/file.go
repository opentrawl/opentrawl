package tomlfile

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type File struct {
	lines []line
}

type line struct {
	raw   string
	key   string
	value string
}

func Read(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data), nil
}

func Empty() *File {
	return &File{}
}

func Parse(data []byte) *File {
	var out File
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		raw := scanner.Text()
		key, value, ok := parseLine(raw)
		if !ok {
			out.lines = append(out.lines, line{raw: raw})
			continue
		}
		out.lines = append(out.lines, line{raw: raw, key: key, value: value})
	}
	return &out
}

func (f *File) Get(key string) string {
	key = strings.TrimSpace(key)
	for _, line := range f.lines {
		if line.key == key {
			return line.value
		}
	}
	return ""
}

func (f *File) Set(key, value string) {
	key = strings.TrimSpace(key)
	for i := range f.lines {
		if f.lines[i].key == key {
			f.lines[i].value = value
			f.lines[i].raw = formatLine(key, value)
			return
		}
	}
	f.lines = append(f.lines, line{key: key, value: value, raw: formatLine(key, value)})
}

func (f *File) Bytes() []byte {
	var out strings.Builder
	for _, line := range f.lines {
		out.WriteString(line.raw)
		out.WriteByte('\n')
	}
	return []byte(out.String())
}

func (f *File) WriteAtomic(path string, perm os.FileMode) error {
	if perm == 0 {
		perm = 0o600
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(f.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func parseLine(raw string) (string, string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
		return "", "", false
	}
	key, rest, ok := strings.Cut(trimmed, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}
	value := stripComment(strings.TrimSpace(rest))
	if unquoted, err := strconv.Unquote(value); err == nil {
		value = unquoted
	}
	return key, value, true
}

func stripComment(value string) string {
	inQuote := false
	escaped := false
	for i, r := range value {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuote = !inQuote
		case r == '#' && !inQuote:
			return strings.TrimSpace(value[:i])
		}
	}
	return strings.TrimSpace(value)
}

func formatLine(key, value string) string {
	return key + " = " + strconv.Quote(value)
}
