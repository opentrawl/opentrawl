package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

const syncStateWidth = 5

type SyncCmd struct {
	Args []string `arg:"" optional:"" passthrough:"partial" name:"source-or-flag" help:"Source ids, followed by source-specific flags when syncing one source"`
}

type SyncResult struct {
	Event   string     `json:"event"`
	Source  string     `json:"source"`
	State   string     `json:"state"`
	Message string     `json:"message,omitempty"`
	Error   *ErrorBody `json:"error,omitempty"`

	displaySource string
	commandToken  string
}

func (c *SyncCmd) Run(r *Runtime) error {
	sourceIDs, sourceArgs, err := splitSyncArgs(c.Args)
	if err != nil {
		return err
	}
	sources, err := r.selectedSourceArgs(sourceIDs)
	if err != nil {
		return err
	}
	sources = canonicalSyncSources(sources)
	if len(sourceArgs) > 0 && len(sources) != 1 {
		return usageErr{fmt.Errorf("source-specific sync flags require exactly one source")}
	}
	if len(sources) == 0 {
		return nil
	}
	if syncHelpRequested(sourceArgs) {
		return r.writeSourceSyncHelp(sources[0], sourceArgs)
	}

	allSources := discoverCrawlers(r.ctx)
	sources, results, err := r.runSyncBatch(sources, sourceArgs, allSources, func(canonical []Source) {
		for _, source := range canonical {
			_, _ = fmt.Fprintf(r.stderr, "%s syncing…\n", sourceHumanName(source))
		}
	})
	if err != nil {
		return err
	}
	sourceWidth := syncSourceWidth(sources)
	for _, result := range results {
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

func syncSource(r *Runtime, ctx context.Context, source Source, sourceArgs []string) SyncResult {
	started := r.logSourceStart(source, "sync")
	if source.MetadataErr != nil {
		r.logSourceDone(source, "sync", started, source.MetadataErr)
		return syncFailureResult(source, "metadata failed")
	}
	report, err := r.runSourceSyncContext(ctx, source, sourceArgs)
	if err != nil {
		r.logSourceDone(source, "sync", started, err)
		body := ckoutput.ErrorBodyFor(err)
		if structuredSyncError(body.Code) {
			return syncErrorResult(source, body)
		}
		return syncFailureResult(source, "sync failed")
	}
	r.logSourceDone(source, "sync", started, nil)
	return normalizeSyncReport(source, report)
}

func structuredSyncError(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "", "internal", "command_failed", "sync_failed":
		return false
	default:
		return true
	}
}

func normalizeSyncReport(source Source, report *trawlkit.SyncReport) SyncResult {
	if report == nil {
		report = &trawlkit.SyncReport{}
	}
	state := "ok"
	var errorBody *ErrorBody
	if len(report.Warnings) > 0 {
		state = "partial"
		errorBody = &ErrorBody{
			Code:    "internal",
			Message: report.Warnings[0],
			Remedy:  "Review OpenTrawl's logs for this source, then sync again.",
		}
	}
	message := firstNonEmpty(firstWarning(report.Warnings), syncReportText(report))
	return SyncResult{
		Event:         "sync",
		Source:        source.ID,
		State:         state,
		Message:       message,
		Error:         errorBody,
		displaySource: sourceHumanName(source),
		commandToken:  sourceCommandToken(source),
	}
}

func syncErrorResult(source Source, body ckoutput.ErrorBody) SyncResult {
	return SyncResult{
		Event: "sync", Source: source.ID, State: "error", Message: body.Message,
		Error:         &ErrorBody{Code: body.Code, Message: body.Message, Remedy: body.Remedy},
		displaySource: sourceHumanName(source), commandToken: sourceCommandToken(source),
	}
}

func firstWarning(warnings []string) string {
	if len(warnings) == 0 {
		return ""
	}
	return strings.TrimSpace(warnings[0])
}

func (r *Runtime) runSourceSync(source Source, sourceArgs []string) (*trawlkit.SyncReport, error) {
	return r.runSourceSyncContext(r.ctx, source, sourceArgs)
}

func (r *Runtime) runSourceSyncContext(ctx context.Context, source Source, sourceArgs []string) (*trawlkit.SyncReport, error) {
	return r.sourceExecutor().Sync(ctx, source.Crawler, sourceArgs)
}

func syncHelpRequested(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func (r *Runtime) writeSourceSyncHelp(source Source, sourceArgs []string) error {
	args := append([]string{source.ID, "sync"}, sourceArgs...)
	out, err := runTrawlkitCaptured(r.ctx, args, []trawlkit.Crawler{source.Crawler})
	if err != nil {
		return err
	}
	help := canonicalSourceSyncHelp(string(out.Stdout), source.ID)
	if _, err := io.WriteString(r.stdout, help); err != nil {
		return err
	}
	if len(out.Stderr) > 0 {
		if _, err := r.lockedStderr().Write(out.Stderr); err != nil {
			return err
		}
	}
	if out.Code != 0 {
		return exitErr{code: out.Code}
	}
	return nil
}

func canonicalSourceSyncHelp(help, sourceID string) string {
	lines := strings.SplitN(help, "\n", 2)
	if len(lines) == 0 {
		return help
	}
	if strings.HasPrefix(lines[0], "Usage: ") {
		if index := strings.Index(lines[0], " sync"); index >= 0 {
			lines[0] = "Usage: trawl sync " + sourceID + lines[0][index+len(" sync"):]
		}
	} else if index := strings.Index(lines[0], " sync:"); index >= 0 {
		lines[0] = "trawl sync " + sourceID + lines[0][index+len(" sync"):]
	}
	return strings.Join(lines, "\n")
}

func splitSyncArgs(args []string) ([]string, []string, error) {
	firstFlag := len(args)
	for index, arg := range args {
		if arg == "--" || strings.HasPrefix(arg, "-") {
			firstFlag = index
			break
		}
	}
	sources := append([]string(nil), args[:firstFlag]...)
	sourceArgs := append([]string(nil), args[firstFlag:]...)
	if len(sourceArgs) > 0 && sourceArgs[0] == "--" {
		sourceArgs = sourceArgs[1:]
	}
	return sources, sourceArgs, nil
}

func syncFailureResult(source Source, message string) SyncResult {
	return SyncResult{
		Event:         "sync",
		Source:        source.ID,
		State:         "error",
		Message:       message,
		displaySource: sourceHumanName(source),
		commandToken:  sourceCommandToken(source),
		Error: &ErrorBody{
			Code:    "sync_failed",
			Message: message,
			Remedy:  "Review OpenTrawl's logs for this source, then sync again.",
		},
	}
}

func syncReportText(report *trawlkit.SyncReport) string {
	parts := make([]string, 0, 3)
	for _, item := range []struct {
		value int64
		label string
	}{
		{report.Added, "added"},
		{report.Updated, "updated"},
		{report.Removed, "removed"},
	} {
		if item.value == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s", render.FormatInteger(item.value), item.label))
	}
	return strings.Join(parts, " · ")
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
	partials := 0
	for _, result := range results {
		if syncResultFailed(result) {
			failures++
			continue
		}
		if strings.EqualFold(result.State, "partial") {
			partials++
		}
		successes++
	}
	if failures == 0 && partials == 0 {
		return nil
	}
	if successes > 0 {
		return exitErr{code: 3}
	}
	return exitErr{code: 1}
}

func (r *Runtime) reportSyncFailure(result SyncResult) {
	remedy := "Review OpenTrawl's logs for this source, then sync again."
	if result.Error != nil && result.Error.Remedy != "" {
		remedy = result.Error.Remedy
	}
	failure := "sync failed"
	if result.Error != nil && result.Error.Code == "snapshot_incomplete" {
		failure = fmt.Sprintf("sync failed (%s)", result.Error.Code)
	}
	_, _ = fmt.Fprintf(r.stderr, "%s %s.\n", firstNonEmpty(result.displaySource, result.Source), failure)
	_, _ = fmt.Fprintf(r.stderr, "  Remedy: %s\n", remedy)
}
