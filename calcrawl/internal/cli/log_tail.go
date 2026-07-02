package cli

import (
	"path/filepath"
	"strings"
	"time"

	crawlog "github.com/openclaw/crawlkit/log"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
)

type logTailOutput struct {
	Path            string         `json:"path,omitempty"`
	LastRun         *logRunOutput  `json:"last_run,omitempty"`
	MostRecentError *logLineOutput `json:"most_recent_error,omitempty"`
	Errors          []string       `json:"errors,omitempty"`
}

type logRunOutput struct {
	RunID      string         `json:"run_id"`
	Command    string         `json:"command"`
	Outcome    string         `json:"outcome"`
	StartedAt  string         `json:"started_at,omitempty"`
	FinishedAt string         `json:"finished_at,omitempty"`
	LastEvent  string         `json:"last_event,omitempty"`
	LineCount  int            `json:"line_count"`
	Error      *logLineOutput `json:"error,omitempty"`
}

type logLineOutput struct {
	Time    string `json:"time"`
	RunID   string `json:"run_id"`
	Command string `json:"command"`
	Event   string `json:"event"`
	Message string `json:"message"`
}

func (r *runtime) logTail() logTailOutput {
	out := logTailOutput{Path: filepath.Join(defaultBaseDir(), archive.AppID, "logs", "current.log")}
	reader, err := crawlog.NewReader(defaultBaseDir(), archive.AppID)
	if err != nil {
		out.Errors = []string{err.Error()}
		return out
	}
	lines, err := reader.RecentLines("", 1000)
	if err != nil {
		out.Errors = []string{err.Error()}
		return out
	}
	currentRunID := r.log.RunID()
	if runID := previousRunID(lines, currentRunID); runID != "" {
		out.LastRun = summarizeLogRun(runID, filterLogRun(lines, runID))
	}
	if line, ok := mostRecentLogError(lines, currentRunID); ok {
		out.MostRecentError = newLogLineOutput(line)
	}
	return out
}

func previousRunID(lines []crawlog.Line, currentRunID string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line.RunID == "" || line.RunID == "-" || line.RunID == currentRunID {
			continue
		}
		return line.RunID
	}
	return ""
}

func filterLogRun(lines []crawlog.Line, runID string) []crawlog.Line {
	out := []crawlog.Line{}
	for _, line := range lines {
		if line.RunID == runID {
			out = append(out, line)
		}
	}
	return out
}

func summarizeLogRun(runID string, lines []crawlog.Line) *logRunOutput {
	if len(lines) == 0 {
		return nil
	}
	out := &logRunOutput{RunID: runID, Outcome: "running"}
	for _, line := range lines {
		if line.Event == "grammar" {
			continue
		}
		if out.Command == "" {
			out.Command = line.Command
		}
		out.LastEvent = line.Event
		out.LineCount++
		if out.StartedAt == "" || line.Event == "start" {
			out.StartedAt = line.Timestamp.Format(time.RFC3339)
		}
		if line.Level == crawlog.LevelError {
			out.Outcome = "error"
			out.Error = newLogLineOutput(line)
		}
		if line.Event == "finish" {
			out.FinishedAt = line.Timestamp.Format(time.RFC3339)
			if strings.Contains(line.Message, "outcome=success") {
				out.Outcome = "success"
			} else if strings.Contains(line.Message, "outcome=error") {
				out.Outcome = "error"
			}
		}
	}
	if out.LineCount == 0 {
		out.Outcome = ""
	}
	return out
}

func mostRecentLogError(lines []crawlog.Line, currentRunID string) (crawlog.Line, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line.RunID == currentRunID {
			continue
		}
		if line.Level == crawlog.LevelError {
			return line, true
		}
	}
	return crawlog.Line{}, false
}

func newLogLineOutput(line crawlog.Line) *logLineOutput {
	return &logLineOutput{
		Time:    line.Timestamp.Format(time.RFC3339),
		RunID:   line.RunID,
		Command: line.Command,
		Event:   line.Event,
		Message: line.Message,
	}
}
