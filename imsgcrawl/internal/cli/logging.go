package cli

import (
	"context"
	"errors"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	cklog "github.com/openclaw/crawlkit/log"
)

type logRun = cklog.Run

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

func (r *runtime) startLogRun() error {
	run, err := cklog.NewRun(cklog.Options{
		StateRoot:    r.logStateRoot(),
		CrawlerID:    "imsgcrawl",
		Command:      r.command,
		Version:      version,
		Platform:     goruntime.GOOS + "/" + goruntime.GOARCH,
		JSONProgress: r.json,
		Stderr:       r.stderr,
	})
	if err != nil {
		return err
	}
	r.runLog = run
	return nil
}

func (r *runtime) finishLogRun(err error) error {
	if err != nil {
		_ = r.logError(errorEvent(r.command, err), err)
	}
	if logErr := r.runLog.Finish(err); err == nil && logErr != nil {
		return logErr
	}
	return err
}

func (r *runtime) logInfo(event, message string) error {
	if r == nil || r.runLog == nil {
		return nil
	}
	return r.runLog.Info(event, message)
}

func (r *runtime) logError(event string, err error) error {
	if r == nil || r.runLog == nil {
		return nil
	}
	return r.runLog.Error(event, err)
}

func (r *runtime) progress(event, unit string, total int64) *cklog.Progress {
	if r == nil || r.runLog == nil {
		return nil
	}
	return r.runLog.Progress(cklog.ProgressOptions{Event: event, Unit: unit, Total: total})
}

func (r *runtime) startHeartbeat(progress *cklog.Progress, message string) func() {
	if progress == nil {
		return func() {}
	}
	done := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = progress.Report(0, message)
			case <-done:
				return
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
		})
	}
}

func (r *runtime) readLogTail() *logTailOutput {
	reader, err := cklog.NewReader(r.logStateRoot(), "imsgcrawl")
	if err != nil {
		return nil
	}
	lines, err := reader.RecentLines("", 200)
	if err != nil {
		return nil
	}
	currentRunID := ""
	if r.runLog != nil {
		currentRunID = r.runLog.RunID()
	}
	out := &logTailOutput{Path: filepath.Join(r.logStateRoot(), "imsgcrawl", "logs", "current.log")}
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

func (r *runtime) logStateRoot() string {
	path := strings.TrimSpace(r.archivePath)
	if path == "" {
		return defaultBaseDir()
	}
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return defaultBaseDir()
	}
	return dir
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
	var codeErr *cliError
	if errors.As(err, &codeErr) && codeErr.code == 2 {
		return "usage_error"
	}
	switch command {
	case "sync":
		return "sync_failed"
	case "status":
		return "status_failed"
	case "doctor":
		return "doctor_failed"
	case "chats":
		return "chats_failed"
	case "messages":
		return "messages_failed"
	case "who":
		return "who_failed"
	case "search":
		return "search_failed"
	case "open":
		return "open_failed"
	case "contacts":
		return "contacts_failed"
	case "metadata":
		return "metadata_failed"
	default:
		return "command_failed"
	}
}

func worldMustChange(err error, message, remedy string) error {
	return cklog.WorldMustChange{Err: err, Message: message, Remedy: remedy}
}
