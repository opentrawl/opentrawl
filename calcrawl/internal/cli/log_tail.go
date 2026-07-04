package cli

import (
	"strings"

	crawlog "github.com/openclaw/crawlkit/log"
	ckrender "github.com/openclaw/crawlkit/render"
)

type logTailOutput = ckrender.LogTail

const logTailLimit = 1000

func (r *runtime) logTail() logTailOutput {
	out := logTailOutput{}
	stateRoot, crawlerID := logPathParts(defaultLogDir())
	reader, err := crawlog.NewReaderWithFileName(stateRoot, crawlerID, calcrawlLogFileName)
	if err != nil {
		out.Errors = []string{err.Error()}
		return out
	}
	lines, err := reader.RecentLines("", logTailLimit)
	if err != nil {
		out.Errors = []string{err.Error()}
		return out
	}
	currentRunID := r.log.RunID()
	if runID := previousRunID(lines, currentRunID); runID != "" {
		out.LastRun = summarizeLogRun(runID, filterLogRun(lines, runID))
	}
	if line, ok := mostRecentLogError(lines, currentRunID); ok {
		copied := line
		out.MostRecentError = &copied
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

func summarizeLogRun(runID string, lines []crawlog.Line) *crawlog.RunSummary {
	if len(lines) == 0 {
		return nil
	}
	out := &crawlog.RunSummary{RunID: runID, Outcome: "running"}
	for _, line := range lines {
		if line.Event == "grammar" {
			continue
		}
		if out.Command == "" {
			out.Command = line.Command
		}
		out.LastEvent = line.Event
		out.LineCount++
		if out.StartedAt.IsZero() || line.Event == "start" {
			out.StartedAt = line.Timestamp
		}
		if line.Level == crawlog.LevelError {
			out.Outcome = "error"
			copied := line
			out.Error = &copied
		}
		if line.Event == "finish" {
			out.FinishedAt = line.Timestamp
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
