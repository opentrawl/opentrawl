package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestFederationAdaptersPreserveOrderAndCopyStoredManifest(t *testing.T) {
	manifest := control.NewManifest("notes", "Notes", "notescrawl")
	manifest.Aliases = []string{"journal"}
	manifest.Headlines = []string{"notes"}
	manifest.Commands["status"] = control.Command{Argv: []string{"notescrawl", "status"}}
	manifest.Commands["search"] = control.Command{Argv: []string{"notescrawl", "search"}}
	manifest.Commands["open"] = control.Command{Argv: []string{"notescrawl", "open"}}
	sources := []Source{
		{Manifest: manifest, ID: "notes", Surface: "Notes"},
		{Manifest: control.NewManifest("photos", "Photos", "photoscrawl"), ID: "photos", Surface: "Photos", MetadataErr: errors.New("metadata is malformed")},
	}
	runtime := &Runtime{}
	status := runtime.federationStatusSources(sources)
	search := runtime.federationSearchSources(sources)
	open := runtime.federationOpenSources(sources)
	if status[0].Manifest.ID != "notes" || status[1].Manifest.ID != "photos" || search[0].Manifest.Binary.Name != "notescrawl" || open[0].Manifest.Headlines[0] != "notes" {
		t.Fatalf("adapters lost manifest facts: %#v %#v %#v", status, search, open)
	}
	sources[0].Manifest.Headlines[0] = "changed"
	sources[0].Manifest.Commands["open"] = control.Command{}
	if open[0].Manifest.Headlines[0] != "notes" || len(open[0].Manifest.Commands["open"].Argv) == 0 {
		t.Fatalf("adapter aliases source manifest: %#v", open[0].Manifest)
	}
	if _, failure := status[1].Run(context.Background()); failure == nil {
		t.Fatal("metadata error callback returned no failure")
	}
	if _, failure := search[0].Run(context.Background(), trawlkit.Query{}); failure == nil || failure.GetCode() != federationv1.FailureCode_FAILURE_CODE_INTERNAL {
		t.Fatalf("declared search without interface = %#v", failure)
	}
	if _, failure := open[0].Run(context.Background(), "n:7", ""); failure == nil || failure.GetCode() != federationv1.FailureCode_FAILURE_CODE_INTERNAL {
		t.Fatalf("declared open without interface = %#v", failure)
	}
	if status[0].SkipReason != "" || search[1].SkipReason != "" || open[1].SkipReason != "" {
		t.Fatalf("metadata error must win over command declaration")
	}
}

func TestFederationAdaptersProjectMixedResponses(t *testing.T) {
	ensureSyntheticHome(t)
	archive := filepath.Join(t.TempDir(), "notes.db")
	store, err := ckstore.Open(context.Background(), ckstore.Options{Path: archive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	manifest := control.NewManifest("notes", "Notes", "notescrawl")
	manifest.Branding.SymbolName = "note.text"
	manifest.Headlines = []string{"Search notes"}
	manifest.Commands["status"] = control.Command{}
	manifest.Commands["search"] = control.Command{}
	manifest.Commands["open"] = control.Command{}
	crawler := &adapterCrawler{id: "notes", archive: archive}
	sources := []Source{
		{Manifest: manifest, ID: "notes", Surface: "Notes", Crawler: crawler},
		{Manifest: control.NewManifest("calendar", "Calendar", "calcrawl"), ID: "calendar", Surface: "Calendar"},
		{Manifest: control.NewManifest("photos", "Photos", "photoscrawl"), ID: "photos", Surface: "Photos", MetadataErr: errors.New("synthetic metadata error")},
	}
	runtime := &Runtime{timeout: crawlerCommandTimeout, stderr: io.Discard}

	status := federation.Status(context.Background(), runtime.federationStatusSources(sources))
	if status.GetOutcome() != federationv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(status.GetSources()) != 1 || len(status.GetFailures()) != 1 || len(status.GetSkippedSources()) != 1 {
		t.Fatalf("status = %#v", status)
	}
	query := trawlkit.Query{Text: "synthetic", Limit: 2}
	search := federation.Search(context.Background(), runtime.federationSearchSources(sources), query, federationv1.SearchOrder_SEARCH_ORDER_RECENCY, 2)
	if search.GetOutcome() != federationv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(search.GetHits()) != 1 || len(search.GetFailures()) != 1 || len(search.GetSkippedSources()) != 1 {
		t.Fatalf("search = %#v", search)
	}
	open := federation.Open(context.Background(), runtime.federationOpenSources(sources), "notes", " short-7 ", "")
	if open.GetOutcome() != federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE || open.GetRequestedRef() != " short-7 " || crawler.statusCalls != 1 || crawler.searchCalls != 1 || crawler.openCalls != 1 {
		t.Fatalf("open = %#v, calls = %#v", open, crawler)
	}
	unsupported := federation.Open(context.Background(), runtime.federationOpenSources(sources), "calendar", "short-7", "")
	if unsupported.GetFailure().GetMessage() != "Open is not supported." {
		t.Fatalf("unsupported = %#v", unsupported)
	}

	input, err := json.MarshalIndent([]any{sources[0].Manifest, sources[1].Manifest, sources[2].Manifest}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeRuntimeEvidence(t, "status-adapter-input.json", append(input, '\n'))
	writeRuntimeEvidence(t, "status-response.pbtxt", []byte(prototext.Format(status)))
	writeRuntimeEvidence(t, "search-adapter-input.json", append(input, '\n'))
	writeRuntimeEvidence(t, "search-response.pbtxt", []byte(prototext.Format(search)))
	openInput, err := json.MarshalIndent(map[string]any{
		"manifest":      sources[0].Manifest,
		"requested_ref": " short-7 ",
		"callback_ref":  "short-7",
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeRuntimeEvidence(t, "open-adapter-input.json", append(openInput, '\n'))
	writeRuntimeEvidence(t, "open-response.pbtxt", []byte(prototext.Format(open)))
}

func TestAppSearchUsesBoundedTotals(t *testing.T) {
	ensureSyntheticHome(t)
	archive := filepath.Join(t.TempDir(), "notes.db")
	store, err := ckstore.Open(context.Background(), ckstore.Options{Path: archive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	manifest := control.NewManifest("notes", "Notes", "notescrawl")
	manifest.Commands["search"] = control.Command{}
	crawler := &adapterCrawler{id: "notes", archive: archive, searchLimit: appSearchLimit, boundedTotals: true}
	response := (&Runtime{timeout: crawlerCommandTimeout, stderr: io.Discard}).appSearchResponse(context.Background(), []Source{{
		Manifest: manifest,
		ID:       "notes",
		Surface:  "Notes",
		Crawler:  crawler,
	}}, "synthetic")
	if len(response.GetFailures()) != 0 || len(response.GetSources()) != 1 || !response.GetSources()[0].GetTotalIsExact() {
		t.Fatalf("app search response = %#v", response)
	}
}

func TestFederationAndAppSearchCompleteStoredShortRefs(t *testing.T) {
	ensureSyntheticHome(t)
	ctx := context.Background()
	archive := filepath.Join(t.TempDir(), "notes.db")
	store, err := ckstore.Open(ctx, ckstore.Options{Path: archive})
	if err != nil {
		t.Fatal(err)
	}
	const ref = "notes:note/1"
	req := &trawlkit.Request{Store: store}
	if _, err := req.AssignShortRefs(ctx, []trawlkit.ShortRefRecord{{Ref: ref}}); err != nil {
		t.Fatal(err)
	}
	aliases, err := req.ShortRefAliases(ctx, []string{ref})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	manifest := control.NewManifest("notes", "Notes", "notescrawl")
	manifest.Commands["search"] = control.Command{}
	response := federation.Search(ctx, (&Runtime{timeout: crawlerCommandTimeout, stderr: io.Discard}).federationSearchSources([]Source{{
		Manifest: manifest,
		ID:       "notes",
		Surface:  "Notes",
		Crawler:  &adapterCrawler{id: "notes", archive: archive, omitSearchShortRef: true},
	}}), trawlkit.Query{Text: "synthetic", Limit: 2}, federationv1.SearchOrder_SEARCH_ORDER_RECENCY, 2)
	if len(response.GetFailures()) != 0 || len(response.GetHits()) != 1 {
		t.Fatalf("response = %#v", response)
	}
	if got, want := response.GetHits()[0].GetShortRef(), aliases[ref]; got != want {
		t.Fatalf("short ref = %q, want stored alias %q", got, want)
	}

	appResponse := (&Runtime{timeout: crawlerCommandTimeout, stderr: io.Discard}).appSearchResponse(ctx, []Source{{
		Manifest: manifest,
		ID:       "notes",
		Surface:  "Notes",
		Crawler: &adapterCrawler{
			id:                 "notes",
			archive:            archive,
			searchLimit:        appSearchLimit,
			boundedTotals:      true,
			omitSearchShortRef: true,
		},
	}}, "synthetic")
	if len(appResponse.GetFailures()) != 0 || len(appResponse.GetHits()) != 1 {
		t.Fatalf("app response = %#v", appResponse)
	}
	if got, want := appResponse.GetHits()[0].GetShortRef(), aliases[ref]; got != want {
		t.Fatalf("app short ref = %q, want stored alias %q", got, want)
	}
}

func TestFederationMissingArchiveStampsSafeSourceFailure(t *testing.T) {
	ensureSyntheticHome(t)
	archive := filepath.Join(t.TempDir(), "synthetic-missing.db")
	manifest := control.NewManifest("notes", "Notes", "notescrawl")
	manifest.Commands["search"] = control.Command{}
	runtime := &Runtime{timeout: crawlerCommandTimeout, stderr: io.Discard}
	response := federation.Search(context.Background(), runtime.federationSearchSources([]Source{{
		Manifest: manifest,
		ID:       "notes",
		Surface:  "Notes",
		Crawler:  &adapterCrawler{id: "notes", archive: archive},
	}}), trawlkit.Query{Text: "synthetic"}, federationv1.SearchOrder_SEARCH_ORDER_RECENCY, 1)
	if len(response.GetFailures()) != 1 {
		t.Fatalf("response = %#v", response)
	}
	failure := response.GetFailures()[0]
	if failure.GetSourceId() != "notes" || failure.GetSurface() != "Notes" || failure.GetCode() != federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE || failure.GetMessage() != "This source is not ready yet." || failure.GetRemedy() != "Retry with -v to see the log location." || strings.Contains(failure.GetMessage(), archive) || strings.Contains(failure.GetRemedy(), archive) {
		t.Fatalf("failure = %#v", failure)
	}
}

func TestAppStatusResponseAppliesShortBudgetAndKeepsCompletedSource(t *testing.T) {
	ensureSyntheticHome(t)
	readyManifest := control.NewManifest("ready", "Ready", "readycrawl")
	readyManifest.Commands["status"] = control.Command{}
	timedManifest := control.NewManifest("timed", "Timed", "timedcrawl")
	timedManifest.Commands["status"] = control.Command{}
	deadline := make(chan time.Time, 1)
	runtime := &Runtime{timeout: crawlerCommandTimeout, stderr: io.Discard}
	started := time.Now()
	response := runtime.appStatusResponse(context.Background(), []Source{
		{Manifest: readyManifest, ID: "ready", Surface: "Ready", Crawler: appStatusCrawler{
			id: "ready",
			status: func(context.Context) (*control.Status, error) {
				return &control.Status{AppID: "ready", State: "ok", Summary: "The synthetic archive is ready."}, nil
			},
		}},
		{Manifest: timedManifest, ID: "timed", Surface: "Timed", Crawler: appStatusCrawler{
			id: "timed",
			status: func(ctx context.Context) (*control.Status, error) {
				value, ok := ctx.Deadline()
				if !ok {
					deadline <- time.Time{}
					return nil, errors.New("app status context has no deadline")
				}
				deadline <- value
				return nil, context.DeadlineExceeded
			},
		}},
	})
	finished := time.Now()

	if appStatusTimeout != 2*time.Second {
		t.Fatalf("app status timeout = %s, want 2s", appStatusTimeout)
	}
	observed := <-deadline
	if observed.Before(started.Add(appStatusTimeout)) || observed.After(finished.Add(appStatusTimeout)) {
		t.Fatalf("status deadline = %s, want %s after the response call began", observed, appStatusTimeout)
	}
	if len(response.GetSources()) != 1 || response.GetSources()[0].GetAppId() != "ready" {
		t.Fatalf("completed sources = %#v", response.GetSources())
	}
	if len(response.GetFailures()) != 1 || response.GetFailures()[0].GetSourceId() != "timed" || response.GetFailures()[0].GetCode() != federationv1.FailureCode_FAILURE_CODE_TIMEOUT {
		t.Fatalf("timeout failures = %#v", response.GetFailures())
	}
}

type appStatusCrawler struct {
	id     string
	status func(context.Context) (*control.Status, error)
}

func (c appStatusCrawler) Info() trawlkit.Info {
	return trawlkit.Info{ID: c.id, Surface: c.id, DisplayName: c.id}
}

func (appStatusCrawler) Verbs() []trawlkit.Verb { return nil }

func (c appStatusCrawler) Status(ctx context.Context, _ *trawlkit.Request) (*control.Status, error) {
	return c.status(ctx)
}

type adapterCrawler struct {
	id                 string
	archive            string
	searchLimit        int
	boundedTotals      bool
	statusCalls        int
	searchCalls        int
	openCalls          int
	omitSearchShortRef bool
}

func (c *adapterCrawler) Info() trawlkit.Info {
	return trawlkit.Info{ID: c.id, Surface: "Notes", DisplayName: "Notes", DefaultPaths: trawlkit.Paths{Archive: c.archive}}
}

func (c *adapterCrawler) Verbs() []trawlkit.Verb { return nil }

func (c *adapterCrawler) Status(context.Context, *trawlkit.Request) (*control.Status, error) {
	c.statusCalls++
	return &control.Status{AppID: c.id, State: "ok", Summary: "Synthetic archive is ready."}, nil
}

func (c *adapterCrawler) Search(_ context.Context, _ *trawlkit.Request, query trawlkit.Query) (trawlkit.SearchResult, error) {
	c.searchCalls++
	limit := c.searchLimit
	if limit == 0 {
		limit = 2
	}
	if query.Text != "synthetic" || query.Limit != limit || query.BoundedTotals != c.boundedTotals {
		return trawlkit.SearchResult{}, errors.New("query did not preserve its total policy, text and limit")
	}
	shortRef := "short-7"
	if c.omitSearchShortRef {
		shortRef = ""
	}
	return trawlkit.SearchResult{Results: []trawlkit.Hit{{
		Source:   c.id,
		Ref:      "notes:note/1",
		ShortRef: shortRef,
		AnchorID: trawlkit.MatchAnchorID,
		Summary:  trawlkit.ResultSummary{Title: "Synthetic note", Subtitle: "Notes"},
		Archive:  []trawlkit.ArchiveContext{{Kind: "notes", Label: "In Notes"}},
		Evidence: []trawlkit.EvidenceFragment{trawlkit.TextMatch("Note passage", "Synthetic note")},
	}}, TotalMatches: 1}, nil
}

func (c *adapterCrawler) OpenRecord(_ context.Context, _ *trawlkit.Request, ref string) (*openv1.OpenRecord, error) {
	c.openCalls++
	if ref != "short-7" {
		return nil, errors.New("open ref was not trimmed")
	}
	return &openv1.OpenRecord{SourceId: c.id, OpenRef: "notes:note/1", Data: &anypb.Any{TypeUrl: "type.example/Open"}, Presentation: &presentationv1.PresentationDocument{
		Title: "Synthetic note", PrimaryAnchorId: trawlkit.MatchAnchorID,
		Blocks: []*presentationv1.Block{{AnchorId: trawlkit.MatchAnchorID, Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: "Synthetic note"}}}},
	}}, nil
}

func writeRuntimeEvidence(t *testing.T, name string, content []byte) {
	t.Helper()
	directory := os.Getenv("OPENTRAWL_EVIDENCE_DIR")
	if directory == "" {
		return
	}
	if len(content) == 0 {
		t.Fatalf("evidence %s is empty", name)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(content) {
		t.Fatalf("evidence %s changed on write", name)
	}
}
