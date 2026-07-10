package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/prototransport"
)

const (
	appWireCommand = "__app"
	appSearchLimit = 20
)

func isAppWireCommand(args []string) bool {
	return len(args) > 0 && args[0] == appWireCommand
}

func executeAppWire(args []string, stdout, stderr io.Writer, timeout time.Duration) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: trawl %s status|sync|search", appWireCommand)
	}
	runtime := &Runtime{
		ctx: context.Background(), stdout: stdout, stderr: stderr,
		root: &CLI{}, now: time.Now, timeout: timeout,
	}
	switch args[1] {
	case "status":
		return runtime.runAppStatus()
	case "sync":
		return runtime.runAppSync()
	case "search":
		return runtime.runAppSearch(args[2:])
	default:
		return fmt.Errorf("usage: trawl %s status|sync|search", appWireCommand)
	}
}

func (r *Runtime) runAppStatus() error {
	results := collectStatus(r, discoverCrawlers(r.ctx))
	for _, result := range results {
		if err := prototransport.WriteDelimited(r.stdout, appStatusMessage(result.Source, result.Status, r.now())); err != nil {
			return err
		}
	}
	return statusExit(results)
}

func (r *Runtime) runAppSync() error {
	sources := discoverCrawlers(r.ctx)
	results := make([]SyncResult, 0, len(sources))
	for _, source := range sources {
		results = append(results, syncSource(r, source))
	}
	return syncExit(results)
}

func (r *Runtime) runAppSearch(args []string) error {
	flags := flag.NewFlagSet(appWireCommand+" search", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sourceID := flags.String("source", "", "source id")
	if err := flags.Parse(args); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if query == "" {
		return fmt.Errorf("usage: trawl %s search [--source ID] QUERY", appWireCommand)
	}
	sources := searchable(discoverCrawlers(r.ctx))
	if id := strings.TrimSpace(*sourceID); id != "" {
		selected, err := r.selectSources(sources, []string{id})
		if err != nil {
			return err
		}
		sources = selected
	}
	results := collectSearch(r, sources, query, searchOptions{limit: appSearchLimit})
	merged := mergedSearchRows(results, appSearchLimit, searchQueryDefaultSort)
	for _, row := range merged.Rows {
		if err := prototransport.WriteDelimited(r.stdout, appSearchMessage(row)); err != nil {
			return err
		}
	}
	return searchExit(results)
}
