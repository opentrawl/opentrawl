package cli

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/clawdex/internal/repo"
	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/crawlkit/render"
)

const (
	clawdexLogFileName = "clawdex.log"
	logTailLimit       = 500
)

type logRun = cklog.Run

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

type indexLogWriter struct {
	r *Runtime
}

func (w indexLogWriter) Write(p []byte) (int, error) {
	message := strings.TrimSpace(string(p))
	if message == "" || w.r == nil {
		return len(p), nil
	}
	if count, ok := strings.CutPrefix(message, "index rebuilt: "); ok {
		count = strings.TrimSpace(strings.TrimSuffix(count, "people"))
		count = strings.TrimSpace(count)
		_ = w.r.logInfo("index_rebuilt", "people="+count)
		return len(p), nil
	}
	_ = w.r.logInfo("diagnostic", "message="+logQuote(message))
	return len(p), nil
}

func newCommandLog(command string, stderr io.Writer, jsonProgress bool, verbosity int) (*cklog.Run, error) {
	stateRoot, crawlerID := logPathParts(repo.DefaultLogDir())
	return cklog.NewRun(cklog.Options{
		StateRoot:    stateRoot,
		CrawlerID:    crawlerID,
		FileName:     clawdexLogFileName,
		Command:      logCommand(command),
		Version:      Version,
		Platform:     goruntime.GOOS + "/" + goruntime.GOARCH,
		Verbosity:    verbosity,
		JSONProgress: jsonProgress,
		Stderr:       stderr,
	})
}

func (r *Runtime) startLogRun(command string) error {
	run, err := newCommandLog(command, r.stderr, r.root.JSON, r.verbosity)
	if err != nil {
		return err
	}
	r.runLog = run
	return nil
}

func (r *Runtime) finishLogRun(err error) error {
	if r == nil || r.runLog == nil {
		return err
	}
	return finishRunLog(r.runLog, r.command, err)
}

func finishStandaloneLog(command string, stderr io.Writer, jsonProgress bool, verbosity int, err error) error {
	run, logErr := newCommandLog(command, stderr, jsonProgress, verbosity)
	if err == nil && logErr != nil {
		return logErr
	}
	if run == nil {
		return err
	}
	return finishRunLog(run, command, err)
}

// finishRunLog closes the run: usage errors are user feedback and finish as
// rejected (docs/rendering.md guard rails), never as a recorded crawler error.
func finishRunLog(run *cklog.Run, command string, err error) error {
	var usage usageErr
	if errors.As(err, &usage) {
		if logErr := run.FinishRejected(); err == nil && logErr != nil {
			return logErr
		}
		return err
	}
	if err != nil {
		_ = run.Error(errorEvent(command, err), err)
	}
	if logErr := run.Finish(err); err == nil && logErr != nil {
		return logErr
	}
	return err
}

func (r *Runtime) logInfo(event, message string) error {
	if r == nil || r.runLog == nil {
		return nil
	}
	return r.runLog.Info(event, message)
}

func (r *Runtime) logDebug(event, message string) error {
	if r == nil || r.runLog == nil {
		return nil
	}
	return r.runLog.Debug(event, message)
}

func (r *Runtime) logSyncTimings(source string, elapsed time.Duration) {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "unknown"
	}
	_ = r.logInfo("sync_done", strings.Join([]string{
		"source=" + logQuote(source),
		"dry_run=true",
		"elapsed_ms=" + elapsedMS(elapsed),
	}, " "))
	_ = r.logDebug("sync_phase", strings.Join([]string{
		"source=" + logQuote(source),
		"preview_ms=" + elapsedMS(elapsed),
	}, " "))
}

func (r *Runtime) logTail() (*cklog.RunSummary, *cklog.Line) {
	reader, err := newLogReader()
	if err != nil {
		return nil, nil
	}
	lines, err := reader.RecentLines("", logTailLimit)
	if err != nil {
		return nil, nil
	}
	currentRunID := ""
	if r != nil && r.runLog != nil {
		currentRunID = r.runLog.RunID()
	}
	runID := previousRunID(lines, currentRunID)
	var lastRun *cklog.RunSummary
	if runID != "" {
		if summary, ok, err := reader.LastRun(runID); err == nil && ok {
			lastRun = &summary
		}
	}
	var recentError *cklog.Line
	if line, ok := previousErrorLine(lines, currentRunID); ok {
		recentError = &line
	}
	return lastRun, recentError
}

func (r *Runtime) renderLogTail() render.LogTail {
	lastRun, recentError := r.logTail()
	return render.LogTail{LastRun: lastRun, MostRecentError: recentError}
}

func (r *Runtime) logTailEnvelope() (*logRunEnvelope, *logErrorEnvelope) {
	lastRun, recentError := r.logTail()
	return newLogRunEnvelope(lastRun), newLogErrorEnvelope(recentError)
}

func newLogReader() (*cklog.Reader, error) {
	stateRoot, crawlerID := logPathParts(repo.DefaultLogDir())
	return cklog.NewReaderWithFileName(stateRoot, crawlerID, clawdexLogFileName)
}

func logPathParts(logDir string) (string, string) {
	baseDir := filepath.Dir(logDir)
	stateRoot := filepath.Dir(baseDir)
	crawlerID := filepath.Base(baseDir)
	if strings.TrimSpace(crawlerID) == "" || crawlerID == "." || crawlerID == string(filepath.Separator) {
		return baseDir, "clawdex"
	}
	return stateRoot, crawlerID
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

func newLogRunEnvelope(summary *cklog.RunSummary) *logRunEnvelope {
	if summary == nil {
		return nil
	}
	out := &logRunEnvelope{
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
	return out
}

func newLogErrorEnvelope(line *cklog.Line) *logErrorEnvelope {
	if line == nil {
		return nil
	}
	return &logErrorEnvelope{
		RunID:   line.RunID,
		Command: line.Command,
		Event:   line.Event,
		Time:    line.Timestamp.Format(time.RFC3339),
		Message: line.Message,
	}
}

func pullVerbosity(args []string) (int, []string) {
	out := make([]string, 0, len(args))
	verbosity := 0
	for _, arg := range args {
		switch arg {
		case "-v", "--verbose":
			verbosity++
		case "-vv":
			verbosity += 2
		default:
			out = append(out, arg)
		}
	}
	return verbosity, out
}

func logCommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "help"
	}
	switch fields[0] {
	case "metadata", "init", "status", "config", "person", "contacts", "who", "note", "timeline", "search", "import", "sync", "export", "git", "doctor", "version", "help":
		return fields[0]
	default:
		return "unknown"
	}
}

func exitCommand(args []string) string {
	for _, arg := range args {
		if arg == "--version" {
			return "version"
		}
	}
	return "help"
}

func commandFromArgs(args []string) string {
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		switch arg {
		case "--config", "--repo":
			skipNext = true
			continue
		case "--version":
			return "version"
		case "-h", "--help", "-help":
			return "help"
		}
		if strings.HasPrefix(arg, "--config=") || strings.HasPrefix(arg, "--repo=") {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return logCommand(arg)
	}
	return "unknown"
}

func jsonFlagPresent(args []string) bool {
	for _, arg := range args {
		if arg == "--json" {
			return true
		}
	}
	return false
}

func errorEvent(command string, err error) string {
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	var usage usageErr
	if errors.As(err, &usage) {
		return "usage_error"
	}
	switch logCommand(command) {
	case "metadata":
		return "metadata_failed"
	case "init":
		return "init_failed"
	case "status":
		return "status_failed"
	case "config":
		return "config_failed"
	case "person":
		return "person_failed"
	case "contacts":
		return "contacts_failed"
	case "who":
		return "who_failed"
	case "note":
		return "note_failed"
	case "timeline":
		return "timeline_failed"
	case "search":
		return "search_failed"
	case "import":
		return "import_failed"
	case "sync":
		return "sync_failed"
	case "export":
		return "export_failed"
	case "git":
		return "git_failed"
	case "doctor":
		return "doctor_failed"
	default:
		return "command_failed"
	}
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

func diagnosticsLine() string {
	return "Diagnostics: run with -v, or read ~/.opentrawl/clawdex/logs/clawdex.log"
}
