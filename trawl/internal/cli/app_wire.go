package cli

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"google.golang.org/protobuf/proto"
)

const (
	appWireCommand = "__app"
	appSearchLimit = 20
	appFrameLimit  = 16 << 20
)

func isAppWireCommand(args []string) bool {
	return len(args) > 0 && args[0] == appWireCommand
}

func executeAppWire(args []string, stdout, stderr io.Writer, timeout time.Duration) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: trawl %s status|sync|search|open", appWireCommand)
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
	case "open":
		return runtime.runAppOpen(args[2:])
	default:
		return fmt.Errorf("usage: trawl %s status|sync|search|open", appWireCommand)
	}
}

func (r *Runtime) runAppStatus() error {
	results := r.collectAppStatus(discoverCrawlers(r.ctx))
	return writeAppResponse(r.stdout, appStatusResponse(results, r.now()))
}

type appStatusResult struct {
	Source Source
	Status StatusEnvelope
	Err    error
}

func (r *Runtime) collectAppStatus(sources []Source) []appStatusResult {
	results := make([]appStatusResult, 0, len(sources))
	for _, source := range sources {
		if source.MetadataErr != nil {
			results = append(results, appStatusResult{
				Source: source,
				Status: errorStatus(source, "the crawler did not identify itself"),
				Err:    source.MetadataErr,
			})
			continue
		}
		status, err := r.statusSource(source)
		if err != nil {
			results = append(results, appStatusResult{
				Source: source,
				Status: errorStatus(source, "the crawler did not report its status"),
				Err:    err,
			})
			continue
		}
		results = append(results, appStatusResult{Source: source, Status: normalizeStatus(source, status)})
	}
	return results
}

func (r *Runtime) runAppSync() error {
	sources := discoverCrawlers(r.ctx)
	results := make([]SyncResult, 0, len(sources))
	for _, source := range sources {
		results = append(results, r.appSyncSource(source))
	}
	return writeAppResponse(r.stdout, appSyncResponse(sources, results))
}

func (r *Runtime) appSyncSource(source Source) SyncResult {
	if source.MetadataErr != nil {
		return appSyncFailureResult(source, "metadata failed", source.MetadataErr)
	}
	syncer, ok := source.Crawler.(trawlkit.Syncer)
	if !ok {
		return appSyncFailureResult(source, "sync is unavailable", fmt.Errorf("source does not support sync"))
	}
	var report *trawlkit.SyncReport
	err := r.withSourceRequest(source, "sync", sourceStoreWrite, outputFormat(true), io.Discard, func(ctx context.Context, req *trawlkit.Request) error {
		var syncErr error
		report, syncErr = syncer.Sync(ctx, req)
		return syncErr
	})
	if err != nil {
		return appSyncFailureResult(source, "sync failed", err)
	}
	result := SyncResult{
		Event:         "sync",
		Source:        source.ID,
		State:         "ok",
		displaySource: sourceHumanName(source),
		commandToken:  sourceCommandToken(source),
	}
	if report != nil && len(report.Warnings) > 0 {
		result.State = "partial"
		result.Message = report.Warnings[0]
		result.Error = &ErrorBody{Code: "internal", Message: report.Warnings[0], Remedy: fmt.Sprintf("run trawl doctor %s", sourceCommandToken(source))}
	}
	return result
}

func appSyncFailureResult(source Source, message string, err error) SyncResult {
	body := sourceErrorBody(err)
	if isTimeoutError(err) {
		body.Code = "deadline_exceeded"
	}
	if body.Message == "" {
		body.Message = message
	}
	if body.Remedy == "" {
		body.Remedy = fmt.Sprintf("run trawl doctor %s", sourceCommandToken(source))
	}
	return SyncResult{
		Event:         "sync",
		Source:        source.ID,
		State:         "error",
		Message:       message,
		Error:         &ErrorBody{Code: body.Code, Message: body.Message, Remedy: body.Remedy},
		displaySource: sourceHumanName(source),
		commandToken:  sourceCommandToken(source),
	}
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
		selected, ok := findSource(sources, id)
		if !ok {
			return writeAppResponse(r.stdout, appInvalidSourceSearchResponse(id))
		}
		sources = []Source{selected}
	}
	results := collectSearch(r, sources, query, searchOptions{limit: appSearchLimit})
	merged := mergedSearchRows(results, appSearchLimit, searchQueryDefaultSort)
	return writeAppResponse(r.stdout, appSearchResponse(results, merged))
}

func (r *Runtime) runAppOpen(args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("usage: trawl %s open REF", appWireCommand)
	}
	ref := strings.TrimSpace(args[0])
	if sourceID, _, ok := splitOpenRef(ref); ok {
		source, found := findSource(discoverCrawlers(r.ctx), sourceID)
		if !found {
			return writeAppResponse(r.stdout, appUnknownOpenResponse(sourceID, ref))
		}
		output, err := r.appOpen(source, ref)
		return writeAppResponse(r.stdout, appOpenResponse(source, ref, output.Bytes(), err))
	}
	if strings.Contains(ref, ":") {
		return writeAppResponse(r.stdout, appInvalidOpenResponse(ref))
	}
	return writeAppResponse(r.stdout, appInvalidOpenResponse(ref))
}

func (r *Runtime) appOpen(source Source, ref string) (*bytes.Buffer, error) {
	var output bytes.Buffer
	if source.MetadataErr != nil {
		return &output, source.MetadataErr
	}
	opener, ok := source.Crawler.(trawlkit.Opener)
	if !ok {
		return &output, fmt.Errorf("source does not support open")
	}
	err := r.withSourceRequest(source, "open", sourceStoreRead, outputFormat(false), &output, func(ctx context.Context, req *trawlkit.Request) error {
		return opener.Open(ctx, req, ref)
	})
	return &output, err
}

func writeAppResponse(w io.Writer, message proto.Message) error {
	size := proto.Size(message)
	if size == 0 || size > appFrameLimit {
		return fmt.Errorf("app protobuf frame is %d bytes; maximum is %d", size, appFrameLimit)
	}
	payload, err := proto.Marshal(message)
	if err != nil {
		return err
	}
	if len(payload) != size {
		return fmt.Errorf("app protobuf frame size changed from %d to %d bytes", size, len(payload))
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}
