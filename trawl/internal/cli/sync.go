package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openclaw/crawlkit"
)

const syncStateWidth = 5

type SyncCmd struct {
	Sources []string `arg:"" optional:"" name:"source" help:"Source ids"`
}

type SyncResult struct {
	Event      string     `json:"event"`
	Source     string     `json:"source"`
	State      string     `json:"state"`
	Message    string     `json:"message,omitempty"`
	Counts     []Count    `json:"counts,omitempty"`
	FinishedAt string     `json:"finished_at,omitempty"`
	Error      *ErrorBody `json:"error,omitempty"`
}

type syncCrawlerOutcome struct {
	Event      string      `json:"event,omitempty"`
	State      string      `json:"state,omitempty"`
	Message    string      `json:"message,omitempty"`
	Summary    string      `json:"summary,omitempty"`
	Stage      string      `json:"stage,omitempty"`
	Done       json.Number `json:"done,omitempty"`
	Total      json.Number `json:"total,omitempty"`
	Added      json.Number `json:"added,omitempty"`
	Updated    json.Number `json:"updated,omitempty"`
	Removed    json.Number `json:"removed,omitempty"`
	Counts     []Count     `json:"counts,omitempty"`
	FinishedAt string      `json:"finished_at,omitempty"`
	Error      *ErrorBody  `json:"error,omitempty"`
}

func (c *SyncCmd) Run(r *Runtime) error {
	sources, err := r.selectedSourceArgs(c.Sources)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil
	}

	sourceWidth := syncSourceWidth(sources)
	results := make([]SyncResult, 0, len(sources))
	for _, source := range sources {
		_, _ = fmt.Fprintf(r.stderr, "%s syncing…\n", source.ID)
		result := syncSource(r, source)
		results = append(results, result)
		if r.root.JSON {
			if err := writeJSON(r.stdout, result); err != nil {
				return err
			}
		} else if err := renderSyncLine(r.stdout, result, sourceWidth, syncStateWidth); err != nil {
			return err
		}
		if syncResultFailed(result) {
			r.reportSyncFailure(result)
		}
	}
	return syncExit(results)
}

func syncSource(r *Runtime, source Source) SyncResult {
	started := r.logSourceStart(source, "sync")
	if source.MetadataErr != nil {
		r.logSourceDone(source, "sync", started, source.MetadataErr)
		return syncFailureResult(source, "metadata failed")
	}
	data, stderr, err := r.runSourceSync(source)
	if len(stderr) > 0 {
		_, _ = r.lockedStderr().Write(stderr)
	}
	if err != nil {
		r.logSourceDone(source, "sync", started, err)
		return syncFailureResult(source, "sync failed")
	}
	outcome, ok := lastSyncOutcome(data)
	if !ok {
		r.logSourceDone(source, "sync", started, fmt.Errorf("sync did not return a final JSON outcome"))
		return syncFailureResult(source, "sync did not return a final JSON outcome")
	}
	r.logSourceDone(source, "sync", started, nil)
	return normalizeSyncOutcome(source, outcome)
}

func lastSyncOutcome(data []byte) (syncCrawlerOutcome, bool) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed != "" && json.Valid([]byte(trimmed)) {
		var outcome syncCrawlerOutcome
		if err := decodeContractJSON([]byte(trimmed), &outcome); err == nil {
			return outcome, true
		}
	}
	lines := strings.Split(string(data), "\n")
	var last syncCrawlerOutcome
	found := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var outcome syncCrawlerOutcome
		if err := decodeContractJSON([]byte(line), &outcome); err != nil {
			continue
		}
		last = outcome
		found = true
	}
	return last, found
}

func normalizeSyncOutcome(source Source, outcome syncCrawlerOutcome) SyncResult {
	state := firstNonEmpty(outcome.State)
	if state == "" {
		state = "ok"
	}
	if outcome.Error != nil && state == "ok" {
		state = "error"
	}
	message := firstNonEmpty(
		outcome.Message,
		outcome.Summary,
		syncErrorMessage(outcome.Error),
		syncCountsText(outcome.Counts),
		syncReportText(outcome),
		syncProgressText(outcome),
	)
	return SyncResult{
		Event:      "sync",
		Source:     source.ID,
		State:      state,
		Message:    message,
		Counts:     outcome.Counts,
		FinishedAt: outcome.FinishedAt,
		Error:      outcome.Error,
	}
}

func (r *Runtime) runSourceSync(source Source) ([]byte, []byte, error) {
	args := []string{source.ID, "sync", "--json"}
	switch r.verbosity() {
	case 1:
		args = append([]string{"-v"}, args...)
	case 2:
		args = append([]string{"-vv"}, args...)
	}
	out, err := runCrawlkitCaptured(args, []crawlkit.Crawler{source.Crawler})
	if err != nil {
		return nil, nil, err
	}
	if out.Code != 0 {
		return out.Stdout, out.Stderr, crawlerCommandError{command: "sync", err: exitErr{code: out.Code}}
	}
	return out.Stdout, out.Stderr, nil
}

func syncFailureResult(source Source, message string) SyncResult {
	remedy := fmt.Sprintf("run: trawl doctor %s", source.ID)
	return SyncResult{
		Event:   "sync",
		Source:  source.ID,
		State:   "error",
		Message: message,
		Error: &ErrorBody{
			Code:    "sync_failed",
			Message: message,
			Remedy:  remedy,
		},
	}
}

func syncErrorMessage(body *ErrorBody) string {
	if body == nil {
		return ""
	}
	return body.Message
}

func syncCountsText(counts []Count) string {
	if len(counts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(counts))
	for _, count := range counts {
		parts = append(parts, formatCount(count))
	}
	return strings.Join(parts, " · ")
}

func syncReportText(outcome syncCrawlerOutcome) string {
	parts := make([]string, 0, 3)
	for _, item := range []struct {
		value json.Number
		label string
	}{
		{outcome.Added, "added"},
		{outcome.Updated, "updated"},
		{outcome.Removed, "removed"},
	} {
		if item.value.String() == "" {
			continue
		}
		value, err := item.value.Int64()
		if err != nil || value == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s", formatInteger(value, "", item.label), item.label))
	}
	return strings.Join(parts, " · ")
}

func syncProgressText(outcome syncCrawlerOutcome) string {
	stage := strings.TrimSpace(outcome.Stage)
	if stage == "" {
		return ""
	}
	done := outcome.Done.String()
	total := outcome.Total.String()
	if done != "" && total != "" {
		return fmt.Sprintf("%s %s/%s", stage, done, total)
	}
	return stage
}

func syncResultFailed(result SyncResult) bool {
	switch result.State {
	case "error", "fail", "missing":
		return true
	default:
		return false
	}
}

func syncExit(results []SyncResult) error {
	failures := 0
	successes := 0
	for _, result := range results {
		if syncResultFailed(result) {
			failures++
			continue
		}
		successes++
	}
	if failures == 0 {
		return nil
	}
	if successes > 0 {
		return exitErr{code: 3}
	}
	return exitErr{code: 1}
}

func (r *Runtime) reportSyncFailure(result SyncResult) {
	remedy := fmt.Sprintf("run: trawl doctor %s", result.Source)
	if result.Error != nil && result.Error.Remedy != "" {
		remedy = result.Error.Remedy
	}
	_, _ = fmt.Fprintf(r.stderr, "%s sync failed.\n", result.Source)
	_, _ = fmt.Fprintf(r.stderr, "  Remedy: %s\n", remedy)
}
