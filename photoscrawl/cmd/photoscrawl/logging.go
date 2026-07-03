package main

import (
	"context"
	"errors"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	cklog "github.com/openclaw/crawlkit/log"
	ckrender "github.com/openclaw/crawlkit/render"
	"github.com/openclaw/photoscrawl/internal/archive"
)

type logTailOutput struct {
	Path            string          `json:"path,omitempty"`
	LastRun         *logRunOutput   `json:"last_run,omitempty"`
	MostRecentError *logErrorOutput `json:"most_recent_error,omitempty"`
}

type logRunOutput struct {
	RunID      string          `json:"run_id"`
	Command    string          `json:"command"`
	StartedAt  string          `json:"started_at,omitempty"`
	FinishedAt string          `json:"finished_at,omitempty"`
	Outcome    string          `json:"outcome"`
	LastEvent  string          `json:"last_event,omitempty"`
	Error      *logErrorOutput `json:"error,omitempty"`
}

type logErrorOutput struct {
	RunID     string `json:"run_id"`
	Command   string `json:"command"`
	Event     string `json:"event"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp,omitempty"`
}

func startLogRun(paths archive.Paths, command string, jsonProgress bool) (*cklog.Run, error) {
	return cklog.NewRun(cklog.Options{
		StateRoot:    logStateRoot(paths),
		CrawlerID:    "photoscrawl",
		Command:      logCommandName(command),
		Version:      archive.Version,
		Platform:     goruntime.GOOS + "/" + goruntime.GOARCH,
		JSONProgress: jsonProgress,
	})
}

func finishLogRun(run *cklog.Run, command string, err error) error {
	if run == nil {
		return err
	}
	if err != nil {
		_ = run.Error(errorEvent(command, err), loggableError(err))
	}
	if logErr := run.Finish(err); err == nil && logErr != nil {
		return logErr
	}
	return err
}

func logInfo(run *cklog.Run, event, message string) {
	if run == nil {
		return
	}
	_ = run.Info(event, message)
}

func readLogTail(paths archive.Paths, currentRun *cklog.Run) *logTailOutput {
	reader, err := cklog.NewReader(logStateRoot(paths), "photoscrawl")
	if err != nil {
		return nil
	}
	lines, err := reader.RecentLines("", 200)
	if err != nil {
		return &logTailOutput{
			Path: filepath.Join(paths.LogDir, "current.log"),
			MostRecentError: &logErrorOutput{
				Command: "log",
				Event:   "read_failed",
				Message: err.Error(),
			},
		}
	}
	currentRunID := ""
	if currentRun != nil {
		currentRunID = currentRun.RunID()
	}
	out := &logTailOutput{Path: filepath.Join(paths.LogDir, "current.log")}
	if runID := previousRunID(lines, currentRunID); runID != "" {
		if summary, ok, err := reader.LastRun(runID); err == nil && ok {
			out.LastRun = newLogRunOutput(summary)
		}
	}
	if line, ok := previousErrorLine(lines, currentRunID); ok {
		out.MostRecentError = newLogErrorOutput(line)
	}
	if out.LastRun == nil && out.MostRecentError == nil {
		return nil
	}
	return out
}

func renderLogTail(value *logTailOutput) ckrender.LogTail {
	if value == nil {
		return ckrender.LogTail{}
	}
	var out ckrender.LogTail
	if value.LastRun != nil {
		out.LastRun = &cklog.RunSummary{
			RunID:      value.LastRun.RunID,
			Command:    value.LastRun.Command,
			StartedAt:  parseLogTime(value.LastRun.StartedAt),
			FinishedAt: parseLogTime(value.LastRun.FinishedAt),
			Outcome:    value.LastRun.Outcome,
			LastEvent:  value.LastRun.LastEvent,
		}
	}
	if value.MostRecentError != nil {
		out.MostRecentError = &cklog.Line{
			RunID:     value.MostRecentError.RunID,
			Command:   value.MostRecentError.Command,
			Event:     value.MostRecentError.Event,
			Message:   value.MostRecentError.Message,
			Timestamp: parseLogTime(value.MostRecentError.Timestamp),
			Level:     cklog.LevelError,
		}
	}
	return out
}

func previousRunID(lines []cklog.Line, currentRunID string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line.Event == "grammar" || line.RunID == "-" || line.RunID == currentRunID {
			continue
		}
		return line.RunID
	}
	return ""
}

func previousErrorLine(lines []cklog.Line, currentRunID string) (cklog.Line, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line.Level == cklog.LevelError && line.RunID != currentRunID {
			return line, true
		}
	}
	return cklog.Line{}, false
}

func newLogRunOutput(summary cklog.RunSummary) *logRunOutput {
	out := &logRunOutput{
		RunID:     summary.RunID,
		Command:   summary.Command,
		Outcome:   summary.Outcome,
		LastEvent: summary.LastEvent,
	}
	if !summary.StartedAt.IsZero() {
		out.StartedAt = summary.StartedAt.Format(time.RFC3339)
	}
	if !summary.FinishedAt.IsZero() {
		out.FinishedAt = summary.FinishedAt.Format(time.RFC3339)
	}
	if summary.Error != nil {
		out.Error = newLogErrorOutput(*summary.Error)
	}
	return out
}

func newLogErrorOutput(line cklog.Line) *logErrorOutput {
	return &logErrorOutput{
		RunID:     line.RunID,
		Command:   line.Command,
		Event:     line.Event,
		Message:   line.Message,
		Timestamp: line.Timestamp.Format(time.RFC3339),
	}
}

func parseLogTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func logStateRoot(paths archive.Paths) string {
	logDir := strings.TrimSpace(paths.LogDir)
	if logDir != "" {
		return filepath.Dir(filepath.Dir(logDir))
	}
	dataDir := strings.TrimSpace(paths.DataDir)
	if dataDir == "" {
		return "."
	}
	if filepath.Base(dataDir) == "photoscrawl" {
		return filepath.Dir(dataDir)
	}
	return dataDir
}

func logCommandName(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return "command"
	}
	var b strings.Builder
	for i, char := range command {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9' && i > 0:
			b.WriteRune(char)
		case char >= 'A' && char <= 'Z':
			b.WriteRune(char + ('a' - 'A'))
		case char == '_' || char == '-' || char == '.':
			b.WriteRune(char)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_-.")
	if out == "" {
		return "command"
	}
	return out
}

func errorEvent(command string, err error) string {
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if outputIsUsage(err) {
		return "usage_error"
	}
	switch logCommandName(command) {
	case "metadata":
		return "metadata_failed"
	case "status":
		return "status_failed"
	case "doctor":
		return "doctor_failed"
	case "sync":
		return "sync_failed"
	case "classify":
		return "classify_failed"
	case "search":
		return "search_failed"
	case "open":
		return "open_failed"
	case "neighbors":
		return "neighbors_failed"
	default:
		return "command_failed"
	}
}

func outputIsUsage(err error) bool {
	return strings.TrimSpace(normaliseError(err).Code) == "usage"
}

func loggableError(err error) error {
	contract := normaliseError(err)
	return cklog.WorldMustChange{Err: err, Message: contract.Message, Remedy: contract.Remedy}
}
