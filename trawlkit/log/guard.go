package log

import (
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const (
	maxSafeLineLength     = 4096
	maxContentValueLength = 512
)

var (
	ErrUnsafeLogLine = errors.New("unsafe log line refused")

	secretPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(authorization|api[_-]?key|secret|token|password|passwd|private[_ -]?key)\s*[:=]\s*("[^"]+"|\S+)`),
		regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]{20,}`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{20,}\b`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`),
	}
	valuePattern = regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9_-]{0,32})=("[^"]*"|\S+)`)

	pathLocks sync.Map
)

func guardLine(line string) error {
	if len(line) > maxSafeLineLength {
		return ErrUnsafeLogLine
	}
	for _, pattern := range secretPatterns {
		if pattern.MatchString(line) {
			return ErrUnsafeLogLine
		}
	}
	for _, match := range valuePattern.FindAllStringSubmatch(line, -1) {
		if len(match) < 3 {
			continue
		}
		value := match[2]
		if strings.HasPrefix(value, `"`) {
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			}
		}
		if len(value) > maxContentValueLength {
			return ErrUnsafeLogLine
		}
	}
	return nil
}

func guardProgress(event progressEvent) error {
	if err := guardLine(event.Message); err != nil {
		return err
	}
	if err := guardLine(event.Unit); err != nil {
		return err
	}
	return nil
}

func validPathSegment(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	return validToken(value)
}

func validLogFileName(value string) bool {
	if !strings.HasSuffix(value, ".log") {
		return false
	}
	return validPathSegment(value)
}

func validField(value string) bool {
	return value != "" && validToken(value)
}

func validEvent(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' && i > 0 || r == '_' && i > 0 {
			continue
		}
		return false
	}
	return true
}

func validToken(value string) bool {
	for _, r := range value {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func lockForPath(path string) *sync.Mutex {
	lock, _ := pathLocks.LoadOrStore(path, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func trimLog(path string, limit int64) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if int64(len(data)) <= limit {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 0 {
		return nil
	}
	header := lines[0]
	used := len(header) + 1
	var tail []string
	for i := len(lines) - 1; i >= 1; i-- {
		lineSize := len(lines[i]) + 1
		if int64(used+lineSize) > limit {
			break
		}
		tail = append(tail, lines[i])
		used += lineSize
	}
	for left, right := 0, len(tail)-1; left < right; left, right = left+1, right-1 {
		tail[left], tail[right] = tail[right], tail[left]
	}
	out := append([]string{header}, tail...)
	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}
