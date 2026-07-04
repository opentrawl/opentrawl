package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	cklog "github.com/openclaw/crawlkit/log"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

const (
	gogcrawlLogFileName = "gogcrawl.log"
	logTailLimit        = 500
)

type logRunEnvelope struct {
	RunID      string `json:"run_id"`
	Command    string `json:"command"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	Outcome    string `json:"outcome"`
	LastEvent  string `json:"last_event,omitempty"`
}

type logErrorEnvelope struct {
	RunID   string `json:"run_id"`
	Command string `json:"command"`
	Event   string `json:"event"`
	Time    string `json:"time"`
	Message string `json:"message"`
}

func newCommandLog(command string, stderr interface {
	Write([]byte) (int, error)
}, jsonProgress bool, verbosity int) (*cklog.Run, error) {
	paths, err := archive.DefaultPaths()
	if err != nil {
		return nil, err
	}
	stateRoot, crawlerID := logPathParts(paths.LogDir)
	return cklog.NewRun(cklog.Options{
		StateRoot:    stateRoot,
		CrawlerID:    crawlerID,
		FileName:     gogcrawlLogFileName,
		Command:      command,
		Version:      version,
		JSONProgress: jsonProgress,
		Stderr:       stderr,
		Verbosity:    verbosity,
	})
}

func finishCommandLog(run *cklog.Run, err error) error {
	if run == nil {
		return err
	}
	if logErr := run.Finish(err); err == nil && logErr != nil {
		return logErr
	}
	return err
}

func commandName(args []string) string {
	if len(args) == 0 {
		return "help"
	}
	switch args[0] {
	case "metadata", "status", "sync", "search", "who", "open", "doctor", "contacts":
		return args[0]
	default:
		return "unknown"
	}
}

func errorEvent(err error) string {
	var codeErr *cliError
	if errors.As(err, &codeErr) && strings.TrimSpace(codeErr.name) != "" {
		return codeErr.name
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	return "command_failed"
}

func (r *runtime) logTail() (*logRunEnvelope, *logErrorEnvelope) {
	reader, err := newLogReader()
	if err != nil {
		return nil, nil
	}
	lines, err := reader.RecentLines("", logTailLimit)
	if err != nil {
		return nil, nil
	}
	currentRunID := ""
	if r != nil && r.log != nil {
		currentRunID = r.log.RunID()
	}
	return latestRun(lines, currentRunID), latestError(lines, currentRunID)
}

func newLogReader() (*cklog.Reader, error) {
	paths, err := archive.DefaultPaths()
	if err != nil {
		return nil, err
	}
	stateRoot, crawlerID := logPathParts(paths.LogDir)
	return cklog.NewReaderWithFileName(stateRoot, crawlerID, gogcrawlLogFileName)
}

func logPathParts(logDir string) (string, string) {
	baseDir := filepath.Dir(logDir)
	stateRoot := filepath.Dir(baseDir)
	crawlerID := filepath.Base(baseDir)
	if strings.TrimSpace(crawlerID) == "" || crawlerID == "." || crawlerID == string(filepath.Separator) {
		return baseDir, "gogcrawl"
	}
	return stateRoot, crawlerID
}

func latestRun(lines []cklog.Line, excludeRunID string) *logRunEnvelope {
	runID := ""
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line.RunID == "-" || line.RunID == excludeRunID || line.Event == "grammar" {
			continue
		}
		runID = line.RunID
		break
	}
	if runID == "" {
		return nil
	}
	out := logRunEnvelope{RunID: runID, Outcome: "running"}
	for _, line := range lines {
		if line.RunID != runID || line.Event == "grammar" {
			continue
		}
		if out.Command == "" {
			out.Command = line.Command
		}
		out.LastEvent = line.Event
		if out.StartedAt == "" || line.Event == "start" {
			out.StartedAt = formatLogTime(line.Timestamp)
		}
		if line.Event == "finish" {
			out.FinishedAt = formatLogTime(line.Timestamp)
			if strings.Contains(line.Message, "outcome=success") {
				out.Outcome = "success"
			} else if strings.Contains(line.Message, "outcome=error") {
				out.Outcome = "error"
			}
		}
		if line.Level == cklog.LevelError && out.Outcome == "running" {
			out.Outcome = "error"
		}
	}
	return &out
}

func latestError(lines []cklog.Line, excludeRunID string) *logErrorEnvelope {
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line.Level != cklog.LevelError || line.RunID == excludeRunID {
			continue
		}
		return &logErrorEnvelope{
			RunID:   line.RunID,
			Command: line.Command,
			Event:   line.Event,
			Time:    formatLogTime(line.Timestamp),
			Message: line.Message,
		}
	}
	return nil
}

func formatLogTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format(time.RFC3339)
}

func (r *runtime) logInfo(event, message string) {
	if r == nil || r.log == nil {
		return
	}
	_ = r.log.Info(event, message)
}

func (r *runtime) logDebug(event, message string) {
	if r == nil || r.log == nil {
		return
	}
	_ = r.log.Debug(event, message)
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
