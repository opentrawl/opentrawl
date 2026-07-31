package cli

import (
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	app "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/app"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	"google.golang.org/protobuf/proto"
)

const (
	appWireCommand                    = "__app"
	defaultMaximumAppSearchMatchCount = 20
	appFrameLimit                     = 16 << 20
)

func isAppWireCommand(args []string) bool {
	return len(args) > 0 && args[0] == appWireCommand
}

func executeAppWire(
	args []string,
	stdout, stderr io.Writer,
	timeout time.Duration,
	stateRoot string,
) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: trawl %s status|update|search|open", appWireCommand)
	}
	runtime := &Runtime{
		ctx: context.Background(), stdout: stdout, stderr: stderr,
		root: &CLI{}, now: time.Now, timeout: timeout, stateRoot: stateRoot,
	}
	switch args[1] {
	case "status":
		return runtime.runAppStatus()
	case "update":
		return runtime.runAppUpdate(args[2:])
	case "search":
		return runtime.runAppSearch(args[2:])
	case "open":
		return runtime.runAppOpen(args[2:])
	default:
		return fmt.Errorf("usage: trawl %s status|update|search|open", appWireCommand)
	}
}

func (r *Runtime) runAppStatus() error {
	registeredTrawlerManifestSnapshot := buildRegisteredTrawlerManifestSnapshot(true)
	return writeAppResponse(r.stdout, r.appStatusResponse(r.ctx, registeredTrawlerManifestSnapshot))
}

func (r *Runtime) runAppUpdate(args []string) error {
	flags := flag.NewFlagSet(appWireCommand+" update", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var trawlerIdentities repeatedStringFlag
	flags.Var(&trawlerIdentities, "trawler", "trawler manifest identity")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return fmt.Errorf("usage: trawl %s update [--trawler ID]", appWireCommand)
	}
	trawlers := discoverInstalledTrawlers(r.ctx)
	if len(trawlerIdentities) > 0 {
		selectedTrawlers := make([]InstalledTrawler, 0, len(trawlerIdentities))
		seen := make(map[string]struct{}, len(trawlerIdentities))
		for _, requested := range trawlerIdentities {
			id := strings.TrimSpace(requested)
			if id == "" {
				return fmt.Errorf("trawler manifest identity is required")
			}
			if _, exists := seen[id]; exists {
				continue
			}
			selected, ok := findInstalledTrawler(trawlers, id)
			if !ok {
				return fmt.Errorf("trawler %q was not found", id)
			}
			seen[id] = struct{}{}
			selectedTrawlers = append(selectedTrawlers, selected)
		}
		trawlers = selectedTrawlers
	}
	trawlers = canonicalUpdateTrawlers(trawlers)
	allInstalledTrawlers := discoverInstalledTrawlers(r.ctx)
	events := appUpdateEventWriter{writer: r.stdout}
	operation, err := r.runUpdateBatch(
		trawlers,
		nil,
		allInstalledTrawlers,
		nil,
		func(trawler InstalledTrawler, phase updatePhase) {
			events.progress(trawler.RegisteredTrawlerManifest.GetRegisteredTrawler(), appArchiveBuildPhase(phase))
		},
	)
	if err != nil {
		var already updateAlreadyRunningError
		if errors.As(err, &already) {
			return events.result(appUpdateAlreadyRunningResponse())
		}
		return err
	}
	return events.result(operation)
}

type appUpdateEventWriter struct {
	mu     sync.Mutex
	writer io.Writer
	err    error
}

func (w *appUpdateEventWriter) progress(
	updatingTrawler *trawlkit.RegisteredTrawlerIdentity,
	phase app.ArchiveBuildPhase,
) {
	w.write(&app.TrawlerArchiveUpdateEvent{
		Kind: &app.TrawlerArchiveUpdateEvent_Progress{
			Progress: &app.TrawlerArchiveUpdateProgress{
				UpdatingTrawler: updatingTrawler,
				Phase:           phase,
			},
		},
	})
}

func (w *appUpdateEventWriter) result(response *federation.FederatedTrawlerArchiveUpdateOperation) error {
	w.write(&app.TrawlerArchiveUpdateEvent{
		Kind: &app.TrawlerArchiveUpdateEvent_Result{Result: response},
	})
	return w.err
}

func (w *appUpdateEventWriter) write(message proto.Message) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err == nil {
		w.err = writeAppResponse(w.writer, message)
	}
}

func appArchiveBuildPhase(phase updatePhase) app.ArchiveBuildPhase {
	switch phase {
	case updatePhaseBuilding:
		return app.ArchiveBuildPhase_ARCHIVE_BUILD_PHASE_BUILDING
	case updatePhaseFinalising:
		return app.ArchiveBuildPhase_ARCHIVE_BUILD_PHASE_FINALISING
	default:
		return app.ArchiveBuildPhase_ARCHIVE_BUILD_PHASE_UNSPECIFIED
	}
}

type repeatedStringFlag []string

func (values *repeatedStringFlag) String() string { return strings.Join(*values, ",") }

func (values *repeatedStringFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func (r *Runtime) runAppSearch(args []string) error {
	flags := flag.NewFlagSet(appWireCommand+" search", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	trawlerIdentity := flags.String("trawler", "", "trawler manifest identity")
	limit := flags.Int("limit", defaultMaximumAppSearchMatchCount, "maximum number of results")
	after := flags.String("after", "", "results on or after this date")
	before := flags.String("before", "", "results on or before this date or time")
	if err := flags.Parse(args); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(flags.Args(), " "))
	normalizedLimit, err := normalizeSearchLimit(*limit)
	if err != nil {
		return err
	}
	canonicalSearchQuery, err := trawlkitSearchQuery(query, searchOptions{
		limit:  normalizedLimit,
		after:  *after,
		before: *before,
	}, "")
	if err != nil {
		return err
	}
	if canonicalSearchQuery.Text == "" && canonicalSearchQuery.After.IsZero() && canonicalSearchQuery.Before.IsZero() {
		return fmt.Errorf("usage: trawl %s search [--trawler ID] [--after TIME] [--before TIME] [--limit COUNT] [QUERY]", appWireCommand)
	}
	trawlers := discoverInstalledTrawlers(r.ctx)
	if id := strings.TrimSpace(*trawlerIdentity); id != "" {
		selected, ok := findInstalledTrawler(trawlers, id)
		if !ok {
			return fmt.Errorf("trawler %q was not found", id)
		}
		trawlers = []InstalledTrawler{selected}
	}
	canonicalSearchQuery.SearchTotalIsLowerBoundWhenResultLimitIsReached = true
	return writeAppResponse(
		r.stdout,
		r.appSearchResponse(r.ctx, trawlers, canonicalSearchQuery, normalizedLimit),
	)
}

func (r *Runtime) runAppOpen(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: trawl %s open LINK ANCHOR_ID", appWireCommand)
	}
	requestedTrawlLink := trawlkit.NewGloballyRoutableTrawlLink(args[0])
	route, err := trawlkit.ParseGloballyRoutableTrawlLink(requestedTrawlLink)
	if err != nil {
		return fmt.Errorf("open link is not valid")
	}
	response := r.appOpenResponse(
		r.ctx,
		route.RegisteredTrawler,
		route.LocalShortReference,
		trawlkit.NewRecordAnchorIdentifier(args[1]),
	)
	response.RequestedTrawlLink = requestedTrawlLink
	return writeAppResponse(r.stdout, response)
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
