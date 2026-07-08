package log

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var linePattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) +(INFO|WARN|ERROR|DEBUG) +([^ ]+) +([^ ]+) +([a-z][a-z0-9_]*): (.*)$`)

type Line struct {
	Raw        string
	Timestamp  time.Time
	Level      Level
	RunID      string
	Command    string
	Event      string
	Message    string
	Visibility Visibility
}

type RunSummary struct {
	RunID      string
	Command    string
	StartedAt  time.Time
	FinishedAt time.Time
	Outcome    string
	LastEvent  string
	LineCount  int
	Error      *Line
	Version    string
	Commit     string
	Platform   string
}

type Reader struct {
	stateRoot string
	crawlerID string
	logPath   string
}

func NewReader(stateRoot, crawlerID string) (*Reader, error) {
	return NewReaderWithFileName(stateRoot, crawlerID, currentLogName)
}

func NewReaderWithFileName(stateRoot, crawlerID, fileName string) (*Reader, error) {
	stateRoot = strings.TrimSpace(stateRoot)
	if stateRoot == "" {
		return nil, errors.New("state root is required")
	}
	crawlerID = strings.TrimSpace(crawlerID)
	if !validPathSegment(crawlerID) {
		return nil, fmt.Errorf("invalid crawler id %q", crawlerID)
	}
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		fileName = currentLogName
	}
	if !validLogFileName(fileName) {
		return nil, fmt.Errorf("invalid log file name %q", fileName)
	}
	return &Reader{
		stateRoot: stateRoot,
		crawlerID: crawlerID,
		logPath:   filepath.Join(stateRoot, crawlerID, "logs", fileName),
	}, nil
}

func ParseLine(raw string) (Line, bool) {
	match := linePattern.FindStringSubmatch(raw)
	if match == nil {
		return Line{}, false
	}
	ts, err := time.ParseInLocation(logTimeLayout, match[1], time.Local)
	if err != nil {
		return Line{}, false
	}
	level, ok := parseLevel(match[2])
	if !ok {
		return Line{}, false
	}
	message := match[6]
	return Line{
		Raw:        raw,
		Timestamp:  ts,
		Level:      level,
		RunID:      match[3],
		Command:    match[4],
		Event:      match[5],
		Message:    message,
		Visibility: normalizeVisibility(Visibility(parseFields(message)["visibility"])),
	}, true
}

func (r *Reader) RecentLines(runID string, limit int) ([]Line, error) {
	if r == nil || limit <= 0 {
		return nil, nil
	}
	lines, err := r.lines(runID)
	if err != nil {
		return nil, err
	}
	if len(lines) <= limit {
		return lines, nil
	}
	return append([]Line(nil), lines[len(lines)-limit:]...), nil
}

func (r *Reader) LastRun(runID string) (RunSummary, bool, error) {
	if r == nil {
		return RunSummary{}, false, nil
	}
	lines, err := r.lines("")
	if err != nil {
		return RunSummary{}, false, err
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		for i := len(lines) - 1; i >= 0; i-- {
			if lines[i].RunID != "-" && lines[i].Event != "grammar" {
				runID = lines[i].RunID
				break
			}
		}
	}
	if runID == "" {
		return RunSummary{}, false, nil
	}
	var filtered []Line
	for _, line := range lines {
		if line.RunID == runID {
			filtered = append(filtered, line)
		}
	}
	if len(filtered) == 0 {
		return RunSummary{}, false, nil
	}
	return summarizeRun(runID, filtered), true, nil
}

func (r *Reader) MostRecentError(runID string) (Line, bool, error) {
	if r == nil {
		return Line{}, false, nil
	}
	lines, err := r.lines(runID)
	if err != nil {
		return Line{}, false, err
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].Level == LevelError {
			return lines[i], true, nil
		}
	}
	return Line{}, false, nil
}

func IsUserFacingError(line Line) bool {
	if line.Level != "" && line.Level != LevelError {
		return false
	}
	return lineVisibility(line) == VisibilityUserFacing
}

// IsWorldStateError is kept for callers that used the old name. It now reads
// the explicit log visibility field only.
func IsWorldStateError(line Line) bool {
	return IsUserFacingError(line)
}

func lineVisibility(line Line) Visibility {
	if value := strings.TrimSpace(parseFields(line.Message)["visibility"]); value != "" {
		return normalizeVisibility(Visibility(value))
	}
	return normalizeVisibility(line.Visibility)
}

func (r *Reader) lines(runID string) ([]Line, error) {
	file, err := os.Open(r.logPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open log reader: %w", err)
	}
	defer func() { _ = file.Close() }()

	var lines []Line
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line, ok := ParseLine(scanner.Text())
		if !ok {
			continue
		}
		if runID != "" && line.RunID != runID {
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan log: %w", err)
	}
	return lines, nil
}

func summarizeRun(runID string, lines []Line) RunSummary {
	summary := RunSummary{
		RunID:   runID,
		Outcome: "running",
	}
	for _, line := range lines {
		if line.Event == "grammar" {
			continue
		}
		if summary.Command == "" {
			summary.Command = line.Command
		}
		summary.LastEvent = line.Event
		summary.LineCount++
		if summary.StartedAt.IsZero() || line.Event == "start" {
			summary.StartedAt = line.Timestamp
		}
		if line.Event == "start" {
			fields := parseFields(line.Message)
			summary.Version = fields["version"]
			summary.Commit = fields["commit"]
			summary.Platform = fields["platform"]
		}
		if line.Level == LevelError {
			copied := line
			summary.Error = &copied
			if summary.Outcome == "running" {
				summary.Outcome = "error"
			}
		}
		if line.Event == "finish" {
			summary.FinishedAt = line.Timestamp
			summary.Outcome = finishOutcome(line)
		}
	}
	if summary.LineCount == 0 {
		summary.Outcome = ""
	}
	return summary
}

func finishOutcome(line Line) string {
	outcome := strings.ToLower(strings.TrimSpace(parseFields(line.Message)["outcome"]))
	switch outcome {
	case "success", "succeeded":
		return "success"
	case "error", "failed", "failure":
		return "error"
	case "rejected":
		return "rejected"
	}
	if line.Level == LevelError {
		return "error"
	}
	return "success"
}

func parseFields(message string) map[string]string {
	fields := make(map[string]string)
	for _, match := range valuePattern.FindAllStringSubmatch(message, -1) {
		if len(match) < 3 {
			continue
		}
		value := match[2]
		if strings.HasPrefix(value, `"`) {
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			}
		}
		fields[match[1]] = value
	}
	return fields
}

func parseLevel(value string) (Level, bool) {
	switch value {
	case "INFO":
		return LevelInfo, true
	case "WARN":
		return LevelWarn, true
	case "ERROR":
		return LevelError, true
	case "DEBUG":
		return LevelDebug, true
	default:
		return "", false
	}
}
