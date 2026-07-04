package cli

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/crawlkit/render"
)

const (
	telecrawlLogFileName = "telecrawl.log"
	logTailLimit         = 500
)

func (r *runtime) startLogRun(command string) error {
	run, err := r.newLogRun(command)
	if err != nil {
		return err
	}
	r.log = run
	return nil
}

func (r *runtime) newLogRun(command string) (*cklog.Run, error) {
	stateRoot, crawlerID := logPathParts(defaultLogDir())
	return cklog.NewRun(cklog.Options{
		StateRoot:    stateRoot,
		CrawlerID:    crawlerID,
		FileName:     telecrawlLogFileName,
		Command:      command,
		Version:      version,
		Stderr:       r.stderr,
		Verbosity:    r.verbosity,
		JSONProgress: r.json,
	})
}

func (r *runtime) finishLogRun(err error) error {
	if r == nil || r.log == nil {
		return err
	}
	if err != nil {
		_ = r.log.Error(errorEventCode(err), err)
	}
	if finishErr := r.log.Finish(err); err == nil {
		return finishErr
	}
	return err
}

func (r *runtime) logInfo(event, message string) error {
	if r == nil || r.log == nil {
		return nil
	}
	return r.log.Info(event, message)
}

func (r *runtime) logDebug(event, message string) error {
	if r == nil || r.log == nil {
		return nil
	}
	return r.log.Debug(event, message)
}

func (r *runtime) logTail() render.LogTail {
	reader, err := newLogReader()
	if err != nil {
		return render.LogTail{}
	}
	lines, err := reader.RecentLines("", logTailLimit)
	if err != nil {
		return render.LogTail{}
	}
	currentRunID := ""
	if r.log != nil {
		currentRunID = r.log.RunID()
	}
	var tail render.LogTail
	if lastRunID := lastLoggedRunID(lines, currentRunID); lastRunID != "" {
		if summary, ok, err := reader.LastRun(lastRunID); err == nil && ok {
			tail.LastRun = &summary
		}
	}
	if line, ok := mostRecentLoggedError(lines, currentRunID); ok {
		tail.MostRecentError = &line
	}
	return tail
}

func newLogReader() (*cklog.Reader, error) {
	stateRoot, crawlerID := logPathParts(defaultLogDir())
	return cklog.NewReaderWithFileName(stateRoot, crawlerID, telecrawlLogFileName)
}

func logPathParts(logDir string) (string, string) {
	baseDir := filepath.Dir(logDir)
	stateRoot := filepath.Dir(baseDir)
	crawlerID := filepath.Base(baseDir)
	if strings.TrimSpace(crawlerID) == "" || crawlerID == "." || crawlerID == string(filepath.Separator) {
		return baseDir, "telecrawl"
	}
	return stateRoot, crawlerID
}

func lastLoggedRunID(lines []cklog.Line, skipRunID string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line.RunID == "" || line.RunID == "-" || line.RunID == skipRunID || line.Event == "grammar" {
			continue
		}
		return line.RunID
	}
	return ""
}

func mostRecentLoggedError(lines []cklog.Line, skipRunID string) (cklog.Line, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line.RunID == skipRunID || line.RunID == "-" {
			continue
		}
		if line.Level == cklog.LevelError {
			return line, true
		}
	}
	return cklog.Line{}, false
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
