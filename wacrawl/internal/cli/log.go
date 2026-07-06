package cli

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/crawlkit/render"
)

const (
	wacrawlLogFileName = "wacrawl.log"
	logTailLimit       = 500
)

func (a *app) newLogRun(command string) (*cklog.Run, error) {
	stateRoot, crawlerID := logPathParts(defaultLogDir())
	return cklog.NewRun(cklog.Options{
		StateRoot:    stateRoot,
		CrawlerID:    crawlerID,
		FileName:     wacrawlLogFileName,
		Command:      command,
		Version:      version,
		Stderr:       a.stderr,
		Verbosity:    a.verbosity,
		JSONProgress: a.json,
	})
}

func defaultLogDir() string {
	return wacrawlPaths().LogDir
}

func logPathParts(logDir string) (string, string) {
	baseDir := filepath.Dir(logDir)
	stateRoot := filepath.Dir(baseDir)
	crawlerID := filepath.Base(baseDir)
	if strings.TrimSpace(crawlerID) == "" || crawlerID == "." || crawlerID == string(filepath.Separator) {
		return baseDir, "wacrawl"
	}
	return stateRoot, crawlerID
}

func logCommandName(rest []string) string {
	if len(rest) == 0 {
		return "command"
	}
	name := strings.ToLower(strings.TrimSpace(rest[0]))
	var out strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == '_' || r == '-' || r == '.':
			out.WriteRune(r)
		default:
			out.WriteRune('_')
		}
	}
	if out.Len() == 0 {
		return "command"
	}
	return out.String()
}

func errorEvent(rest []string, err error) string {
	var ce *cliError
	if errors.As(err, &ce) {
		if ce.code == 2 {
			return "usage_error"
		}
		if ce.name != "" {
			return logEventName(ce.name)
		}
	}
	if errors.Is(err, errNoArchive) {
		return "archive_missing"
	}
	return logEventName(logCommandName(rest) + "_failed")
}

func logEventName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	for i, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			out.WriteRune(r)
		case (r >= '0' && r <= '9') || r == '_':
			if i > 0 {
				out.WriteRune(r)
			}
		default:
			if i > 0 {
				out.WriteRune('_')
			}
		}
	}
	name := strings.Trim(out.String(), "_")
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return "run_failed"
	}
	return name
}

func worldMustChange(err error, remedy string) error {
	return cklog.WorldMustChange{Err: err, Message: err.Error(), Remedy: remedy}
}

func (a *app) logTail() logTailEnvelope {
	reader, err := newLogReader()
	if err != nil {
		return logTailEnvelope{}
	}
	lines, err := reader.RecentLines("", logTailLimit)
	if err != nil {
		return logTailEnvelope{}
	}
	currentRunID := ""
	if a.runLog != nil {
		currentRunID = a.runLog.RunID()
	}
	lastRunID := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if lineBelongsToTail(lines[i], currentRunID) {
			lastRunID = lines[i].RunID
			break
		}
	}
	var tail logTailEnvelope
	if lastRunID != "" {
		tail.LastRun = summarizeLogRun(lastRunID, lines)
	}
	var genericError *logErrorEnvelope
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if !lineBelongsToTail(line, currentRunID) || line.Level != cklog.LevelError {
			continue
		}
		if genericError != nil && line.RunID != genericError.RunID {
			break
		}
		envelope := newLogErrorEnvelope(line)
		if line.Event == "run_failed" {
			genericError = envelope
			continue
		}
		tail.Error = envelope
		break
	}
	if tail.Error == nil {
		tail.Error = genericError
	}
	return tail
}

func newLogReader() (*cklog.Reader, error) {
	stateRoot, crawlerID := logPathParts(defaultLogDir())
	return cklog.NewReaderWithFileName(stateRoot, crawlerID, wacrawlLogFileName)
}

func lineBelongsToTail(line cklog.Line, currentRunID string) bool {
	return line.RunID != "" && line.RunID != "-" && line.RunID != currentRunID && line.Event != "grammar"
}

func summarizeLogRun(runID string, lines []cklog.Line) *logRunEnvelope {
	out := &logRunEnvelope{RunID: runID, Outcome: "running"}
	for _, line := range lines {
		if line.RunID != runID || line.Event == "grammar" {
			continue
		}
		if out.Command == "" {
			out.Command = line.Command
		}
		out.LastEvent = line.Event
		if out.StartedAt == "" || line.Event == "start" {
			out.StartedAt = formatTime(line.Timestamp)
		}
		fields := logMessageFields(line.Message)
		if line.Event == "start" {
			out.Version = fields["version"]
			out.Commit = fields["commit"]
			out.Platform = fields["platform"]
		}
		if line.Level == cklog.LevelError && out.Outcome == "running" {
			out.Outcome = "error"
		}
		if line.Event == "finish" {
			out.FinishedAt = formatTime(line.Timestamp)
			if fields["outcome"] != "" {
				out.Outcome = fields["outcome"]
			} else if line.Level == cklog.LevelError {
				out.Outcome = "error"
			} else {
				out.Outcome = "success"
			}
		}
	}
	return out
}

func newLogErrorEnvelope(line cklog.Line) *logErrorEnvelope {
	fields := logMessageFields(line.Message)
	message := line.Message
	if fields["error"] != "" {
		message = fields["error"]
	}
	return &logErrorEnvelope{
		RunID:      line.RunID,
		Command:    line.Command,
		Event:      line.Event,
		Time:       formatTime(line.Timestamp),
		Message:    message,
		Remedy:     fields["remedy"],
		visibility: line.Visibility,
	}
}

func logMessageFields(message string) map[string]string {
	fields := map[string]string{}
	for i := 0; i < len(message); {
		for i < len(message) && unicode.IsSpace(rune(message[i])) {
			i++
		}
		keyStart := i
		for i < len(message) {
			r := rune(message[i])
			if r == '=' || unicode.IsSpace(r) {
				break
			}
			i++
		}
		if keyStart == i || i >= len(message) || message[i] != '=' {
			for i < len(message) && !unicode.IsSpace(rune(message[i])) {
				i++
			}
			continue
		}
		key := message[keyStart:i]
		i++
		value := ""
		if i < len(message) && message[i] == '"' {
			valueStart := i
			i++
			escaped := false
			closed := false
			for i < len(message) {
				switch {
				case escaped:
					escaped = false
				case message[i] == '\\':
					escaped = true
				case message[i] == '"':
					i++
					if unquoted, err := strconv.Unquote(message[valueStart:i]); err == nil {
						value = unquoted
					} else {
						value = message[valueStart:i]
					}
					closed = true
				}
				if closed {
					break
				}
				i++
			}
			if value == "" && valueStart < len(message) {
				value = strings.Trim(message[valueStart:i], `"`)
			}
		} else {
			valueStart := i
			for i < len(message) && !unicode.IsSpace(rune(message[i])) {
				i++
			}
			value = message[valueStart:i]
		}
		if key != "" {
			fields[key] = value
		}
	}
	return fields
}

type logTailEnvelope struct {
	LastRun *logRunEnvelope
	Error   *logErrorEnvelope
}

type logRunEnvelope struct {
	RunID      string `json:"run_id"`
	Command    string `json:"command"`
	Outcome    string `json:"outcome"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	LastEvent  string `json:"last_event,omitempty"`
	Version    string `json:"version,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Platform   string `json:"platform,omitempty"`
}

type logErrorEnvelope struct {
	RunID      string `json:"run_id"`
	Command    string `json:"command"`
	Event      string `json:"event"`
	Time       string `json:"time,omitempty"`
	Message    string `json:"message"`
	Remedy     string `json:"remedy,omitempty"`
	visibility cklog.Visibility
}

func renderLogTail(tail logTailEnvelope) render.LogTail {
	return render.LogTail{
		LastRun:         renderLogRun(tail.LastRun),
		MostRecentError: renderLogError(tail.Error),
	}
}

func renderLogRun(run *logRunEnvelope) *cklog.RunSummary {
	if run == nil {
		return nil
	}
	return &cklog.RunSummary{
		Command:    run.Command,
		StartedAt:  parseFormattedTime(run.StartedAt),
		FinishedAt: parseFormattedTime(run.FinishedAt),
		Outcome:    run.Outcome,
		LastEvent:  run.LastEvent,
		Version:    run.Version,
		Commit:     run.Commit,
		Platform:   run.Platform,
	}
}

func renderLogError(logError *logErrorEnvelope) *cklog.Line {
	if logError == nil {
		return nil
	}
	message := ""
	if strings.TrimSpace(logError.Message) != "" {
		message = "error=" + strconv.Quote(logError.Message)
	}
	if strings.TrimSpace(logError.Remedy) != "" {
		if message != "" {
			message += " "
		}
		message += "remedy=" + strconv.Quote(logError.Remedy)
	}
	return &cklog.Line{
		Timestamp: parseFormattedTime(logError.Time),
		Command:   logError.Command,
		Event:     logError.Event,
		Message:   message,
	}
}

func parseFormattedTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" || value == "-" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func logQuote(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return strconv.Quote("")
	}
	if strings.ContainsAny(value, " \t\r\n\"") {
		return strconv.Quote(value)
	}
	return value
}

func elapsedMS(value time.Duration) string {
	return strconv.FormatInt(value.Milliseconds(), 10)
}
