package cli

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
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
		return fmt.Errorf("usage: trawl %s status|sync|search|open|request-photos", appWireCommand)
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
	case "request-photos":
		if len(args) != 2 {
			return fmt.Errorf("usage: trawl %s request-photos", appWireCommand)
		}
		return runtime.runAppRequestPhotos()
	default:
		return fmt.Errorf("usage: trawl %s status|sync|search|open|request-photos", appWireCommand)
	}
}

type photosAccessRequester interface {
	RequestPhotosAccess(context.Context) (control.SetupRequirement, error)
}

func (r *Runtime) runAppRequestPhotos() error {
	sources := discoverCrawlers(r.ctx)
	source, found := findSource(sources, "photos")
	if !found {
		return fmt.Errorf("Photos is not installed")
	}
	requester, ok := source.Crawler.(photosAccessRequester)
	if !ok {
		return fmt.Errorf("Photos does not support app permission requests")
	}
	if _, err := requester.RequestPhotosAccess(r.ctx); err == nil {
		return r.runAppStatus()
	} else {
		return writeAppResponse(r.stdout, appPhotosRequestFailure(r.appStatusResponse(r.ctx, sources), source))
	}
}

func appPhotosRequestFailure(response *federationv1.StatusResponse, source Source) *federationv1.StatusResponse {
	response.Failures = append(response.Failures, &federationv1.SourceFailure{
		SourceId: source.ID, Surface: sourceHumanName(source),
		Code:    federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE,
		Message: "Photos access could not be requested.",
		Remedy:  "Try again from OpenTrawl.",
	})
	if len(response.Sources) > 0 {
		response.Outcome = federationv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL
	} else {
		response.Outcome = federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED
	}
	return response
}

func (r *Runtime) runAppStatus() error {
	return writeAppResponse(r.stdout, r.appStatusResponse(r.ctx, discoverCrawlers(r.ctx)))
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
	sources := discoverCrawlers(r.ctx)
	if id := strings.TrimSpace(*sourceID); id != "" {
		selected, ok := findSource(sources, id)
		if !ok {
			return fmt.Errorf("source %q was not found", id)
		}
		sources = []Source{selected}
	}
	return writeAppResponse(r.stdout, r.appSearchResponse(r.ctx, sources, query))
}

func (r *Runtime) runAppOpen(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: trawl %s open SOURCE_ID REF", appWireCommand)
	}
	return writeAppResponse(r.stdout, r.appOpenResponse(r.ctx, args[0], args[1]))
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
