package cli

import (
	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/crawlkit/render"
)

func (r *runtime) logTail() render.LogTail {
	reader, err := cklog.NewReader(r.logStateRoot, "birdcrawl")
	if err != nil {
		return render.LogTail{}
	}
	lines, err := reader.RecentLines("", 1000)
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
