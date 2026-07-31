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
	appv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/app/v1"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
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
		return fmt.Errorf("usage: trawl %s status|sync|search|open", appWireCommand)
	}
	runtime := &Runtime{
		ctx: context.Background(), stdout: stdout, stderr: stderr,
		root: &CLI{}, now: time.Now, timeout: timeout, stateRoot: stateRoot,
	}
	switch args[1] {
	case "status":
		return runtime.runAppStatus()
	case "sync":
		return runtime.runAppSync(args[2:])
	case "search":
		return runtime.runAppSearch(args[2:])
	case "open":
		return runtime.runAppOpen(args[2:])
	default:
		return fmt.Errorf("usage: trawl %s status|sync|search|open", appWireCommand)
	}
}

func (r *Runtime) runAppStatus() error {
	registeredTrawlerManifestSnapshot := buildRegisteredTrawlerManifestSnapshot(true)
	return writeAppResponse(r.stdout, r.appStatusResponse(r.ctx, registeredTrawlerManifestSnapshot))
}

func (r *Runtime) runAppSync(args []string) error {
	flags := flag.NewFlagSet(appWireCommand+" sync", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var trawlerIdentities repeatedStringFlag
	flags.Var(&trawlerIdentities, "trawler", "trawler manifest identity")
	fullHistory := flags.Bool("full-history", false, "download older Telegram messages")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return fmt.Errorf("usage: trawl %s sync [--trawler ID] [--full-history]", appWireCommand)
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
	trawlers = canonicalSyncTrawlers(trawlers)
	if *fullHistory && (len(trawlers) != 1 || installedTrawlerIdentityText(trawlers[0]) != "telegram") {
		return fmt.Errorf("--full-history requires --trawler telegram")
	}
	allInstalledTrawlers := discoverInstalledTrawlers(r.ctx)
	var trawlerSpecificFlags []string
	if *fullHistory {
		trawlerSpecificFlags = []string{"--full-history"}
	}
	events := appSyncEventWriter{writer: r.stdout}
	operation, err := r.runSyncBatch(
		trawlers,
		trawlerSpecificFlags,
		allInstalledTrawlers,
		nil,
		func(trawler InstalledTrawler, phase syncPhase) {
			events.progress(trawler.RegisteredTrawlerManifest.GetRegisteredTrawler(), appArchiveBuildPhase(phase))
		},
	)
	if err != nil {
		var already syncAlreadyRunningError
		if errors.As(err, &already) {
			return events.result(appSyncAlreadyRunningResponse())
		}
		return err
	}
	return events.result(operation)
}

type appSyncEventWriter struct {
	mu     sync.Mutex
	writer io.Writer
	err    error
}

func (w *appSyncEventWriter) progress(
	syncingTrawler *trawlkit.RegisteredTrawlerIdentity,
	phase appv1.ArchiveBuildPhase,
) {
	w.write(&appv1.SyncEvent{Kind: &appv1.SyncEvent_Progress{Progress: &appv1.SyncProgress{
		SyncingTrawler: syncingTrawler,
		Phase:          phase,
	}}})
}

func (w *appSyncEventWriter) result(response *federationv1.FederatedTrawlerArchiveSyncOperation) error {
	w.write(&appv1.SyncEvent{Kind: &appv1.SyncEvent_Result{Result: response}})
	return w.err
}

func (w *appSyncEventWriter) write(message proto.Message) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err == nil {
		w.err = writeAppResponse(w.writer, message)
	}
}

func appArchiveBuildPhase(phase syncPhase) appv1.ArchiveBuildPhase {
	switch phase {
	case syncPhaseBuilding:
		return appv1.ArchiveBuildPhase_ARCHIVE_BUILD_PHASE_BUILDING
	case syncPhaseFinalising:
		return appv1.ArchiveBuildPhase_ARCHIVE_BUILD_PHASE_FINALISING
	default:
		return appv1.ArchiveBuildPhase_ARCHIVE_BUILD_PHASE_UNSPECIFIED
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
