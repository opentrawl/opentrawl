package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	clawdex "github.com/opentrawl/opentrawl/trawlers/contacts"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
)

const fakeCrawlersEnv = "TRAWL_TEST_FAKE_CRAWLERS"

// shortLocalTestTime renders a contract timestamp exactly the way the
// human tables do, so expectations hold in any timezone.
func shortLocalTestTime(t *testing.T, value string) string {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return render.ShortLocalTime(parsed)
}

type fakeCrawler struct {
	name                  string
	metadata              string
	metadataExit          int
	status                string
	statusExit            int
	statusCalls           *int
	search                string
	searchExit            int
	searchCalls           *int
	searchSleep           string
	searchStderr          string
	searchLimit           string
	searchQuery           string
	searchNoQuery         bool
	searchWho             string
	who                   string
	whoExit               int
	whoQuery              string
	shortRefAlias         string
	open                  string
	openExit              int
	openCalls             *int
	openRef               string
	openRecord            *openv1.OpenRecord
	openHuman             string
	openHumanExit         int
	openStderr            string
	openUnknownShortRef   bool
	openAmbiguousShortRef bool
	evidence              *fakeCrawlerEvidence
	sync                  string
	syncExit              int
	syncSleep             string
	prepareMarker         string
	peopleSnapshot        *control.PeopleSnapshot
	peopleMarker          string
	chats                 []trawlkit.Chat
	chatsError            string
}

// fakeCrawlerEvidence is deliberately test-only. It records the typed
// boundary values that the CLI adapters receive and return for
// TestCanonicalConsumerBoundaries.
type fakeCrawlerEvidence struct {
	mu              sync.Mutex
	Inputs          []string
	StatusResponses []*federationv1.SourceStatus
	SearchResponses []*federationv1.SearchSourceResult
	OpenRecords     []*openv1.OpenRecord
	StatusCalls     int
	SearchCalls     int
	OpenCalls       int
}

func (e *fakeCrawlerEvidence) input(value string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Inputs = append(e.Inputs, value)
}

func (e *fakeCrawlerEvidence) status(value *federationv1.SourceStatus) {
	if e == nil || value == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.StatusResponses = append(e.StatusResponses, value)
}

func (e *fakeCrawlerEvidence) statusCall() {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.StatusCalls++
}

func (e *fakeCrawlerEvidence) search(value *federationv1.SearchSourceResult) {
	if e == nil || value == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.SearchResponses = append(e.SearchResponses, value)
}

func (e *fakeCrawlerEvidence) searchCall() {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.SearchCalls++
}

func (e *fakeCrawlerEvidence) open(value *openv1.OpenRecord) {
	if e == nil || value == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.OpenRecords = append(e.OpenRecords, value)
}

func (e *fakeCrawlerEvidence) openCall() {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.OpenCalls++
}

type fakeCrawlerWire struct {
	Name                  string                  `json:"name"`
	Metadata              string                  `json:"metadata"`
	MetadataExit          int                     `json:"metadata_exit"`
	Status                string                  `json:"status"`
	StatusExit            int                     `json:"status_exit"`
	Search                string                  `json:"search"`
	SearchExit            int                     `json:"search_exit"`
	SearchSleep           string                  `json:"search_sleep"`
	SearchStderr          string                  `json:"search_stderr"`
	SearchLimit           string                  `json:"search_limit"`
	SearchQuery           string                  `json:"search_query"`
	SearchNoQuery         bool                    `json:"search_no_query"`
	SearchWho             string                  `json:"search_who"`
	Who                   string                  `json:"who"`
	WhoExit               int                     `json:"who_exit"`
	WhoQuery              string                  `json:"who_query"`
	ShortRefAlias         string                  `json:"short_ref_alias"`
	Open                  string                  `json:"open"`
	OpenExit              int                     `json:"open_exit"`
	OpenRef               string                  `json:"open_ref"`
	OpenHuman             string                  `json:"open_human"`
	OpenHumanExit         int                     `json:"open_human_exit"`
	OpenStderr            string                  `json:"open_stderr"`
	OpenUnknownShortRef   bool                    `json:"open_unknown_short_ref"`
	OpenAmbiguousShortRef bool                    `json:"open_ambiguous_short_ref"`
	Sync                  string                  `json:"sync"`
	SyncExit              int                     `json:"sync_exit"`
	SyncSleep             string                  `json:"sync_sleep"`
	PrepareMarker         string                  `json:"prepare_marker"`
	PeopleSnapshot        *control.PeopleSnapshot `json:"people_snapshot"`
	PeopleMarker          string                  `json:"people_marker"`
	Chats                 []trawlkit.Chat         `json:"chats"`
	ChatsError            string                  `json:"chats_error"`
}

func (f fakeCrawler) MarshalJSON() ([]byte, error) {
	return json.Marshal(fakeCrawlerWire{
		Name:                  f.name,
		Metadata:              f.metadata,
		MetadataExit:          f.metadataExit,
		Status:                f.status,
		StatusExit:            f.statusExit,
		Search:                f.search,
		SearchExit:            f.searchExit,
		SearchSleep:           f.searchSleep,
		SearchStderr:          f.searchStderr,
		SearchLimit:           f.searchLimit,
		SearchQuery:           f.searchQuery,
		SearchNoQuery:         f.searchNoQuery,
		SearchWho:             f.searchWho,
		Who:                   f.who,
		WhoExit:               f.whoExit,
		WhoQuery:              f.whoQuery,
		ShortRefAlias:         f.shortRefAlias,
		Open:                  f.open,
		OpenExit:              f.openExit,
		OpenRef:               f.openRef,
		OpenHuman:             f.openHuman,
		OpenHumanExit:         f.openHumanExit,
		OpenStderr:            f.openStderr,
		OpenUnknownShortRef:   f.openUnknownShortRef,
		OpenAmbiguousShortRef: f.openAmbiguousShortRef,
		Sync:                  f.sync,
		SyncExit:              f.syncExit,
		SyncSleep:             f.syncSleep,
		PrepareMarker:         f.prepareMarker,
		PeopleSnapshot:        f.peopleSnapshot,
		PeopleMarker:          f.peopleMarker,
		Chats:                 f.chats,
		ChatsError:            f.chatsError,
	})
}

func (f *fakeCrawler) UnmarshalJSON(data []byte) error {
	var wire fakeCrawlerWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*f = fakeCrawler{
		name:                  wire.Name,
		metadata:              wire.Metadata,
		metadataExit:          wire.MetadataExit,
		status:                wire.Status,
		statusExit:            wire.StatusExit,
		search:                wire.Search,
		searchExit:            wire.SearchExit,
		searchSleep:           wire.SearchSleep,
		searchStderr:          wire.SearchStderr,
		searchLimit:           wire.SearchLimit,
		searchQuery:           wire.SearchQuery,
		searchNoQuery:         wire.SearchNoQuery,
		searchWho:             wire.SearchWho,
		who:                   wire.Who,
		whoExit:               wire.WhoExit,
		whoQuery:              wire.WhoQuery,
		shortRefAlias:         wire.ShortRefAlias,
		open:                  wire.Open,
		openExit:              wire.OpenExit,
		openRef:               wire.OpenRef,
		openHuman:             wire.OpenHuman,
		openHumanExit:         wire.OpenHumanExit,
		openStderr:            wire.OpenStderr,
		openUnknownShortRef:   wire.OpenUnknownShortRef,
		openAmbiguousShortRef: wire.OpenAmbiguousShortRef,
		sync:                  wire.Sync,
		syncExit:              wire.SyncExit,
		syncSleep:             wire.SyncSleep,
		prepareMarker:         wire.PrepareMarker,
		peopleSnapshot:        wire.PeopleSnapshot,
		peopleMarker:          wire.PeopleMarker,
		chats:                 wire.Chats,
		chatsError:            wire.ChatsError,
	}
	return nil
}

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == trawlkit.HiddenWireSubcommand {
		if path := os.Getenv(fakeCrawlersEnv); path != "" {
			crawlers, err := loadFakeCrawlers(path)
			if err != nil {
				_, _ = fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			crawlerFactories = fakeCrawlerFactories(crawlers)
			os.Exit(ExecuteCrawlerWire(os.Args[1:]))
		}
	}
	os.Exit(m.Run())
}

func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	ensureSyntheticHome(t)
	ensureFakeArchives(t)
	var stdout, stderr bytes.Buffer
	err := Execute(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), ExitCode(err)
}

// runCLITimeout drives the real per-source read deadline so the timeout
// path can be exercised against a slow fake crawler in
// milliseconds instead of the production 30s.
func runCLITimeout(t *testing.T, timeout time.Duration, args ...string) (string, string, int) {
	t.Helper()
	ensureSyntheticHome(t)
	ensureFakeArchives(t)
	var stdout, stderr bytes.Buffer
	err := execute(args, &stdout, &stderr, timeout)
	return stdout.String(), stderr.String(), ExitCode(err)
}

func syntheticHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/private/tmp", "trawl-cli-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	return home
}

func ensureSyntheticHome(t *testing.T) string {
	t.Helper()
	home := os.Getenv("HOME")
	if strings.HasPrefix(home, "/private/tmp/") {
		return home
	}
	home = syntheticHome(t)
	t.Setenv("HOME", home)
	return home
}

type fakeCrawlerMarker interface {
	fakeCrawler()
}

func ensureFakeArchives(t *testing.T) {
	t.Helper()
	home := ensureSyntheticHome(t)
	for _, crawler := range registeredCrawlers() {
		if _, ok := crawler.(fakeCrawlerMarker); !ok {
			continue
		}
		id := strings.TrimSpace(crawler.Info().ID)
		if id == "" {
			continue
		}
		path := filepath.Join(home, ".opentrawl", id, id+".db")
		st, err := ckstore.Open(context.Background(), ckstore.Options{Path: path})
		if err != nil {
			t.Fatalf("create fake archive %s: %v", path, err)
		}
		if err := st.Close(); err != nil {
			t.Fatalf("close fake archive %s: %v", path, err)
		}
	}
}

func writeFakeCrawlers(t *testing.T, crawlers ...fakeCrawler) string {
	t.Helper()
	dir := t.TempDir()
	oldFactories := crawlerFactories
	normalized := make([]fakeCrawler, 0, len(crawlers))
	for _, crawler := range crawlers {
		normaliseFakeCrawler(&crawler)
		normalized = append(normalized, crawler)
	}
	factories := make([]func() trawlkit.Crawler, 0, len(normalized))
	for _, crawler := range normalized {
		crawler := crawler
		factories = append(factories, func() trawlkit.Crawler { return newFakeSource(t, crawler) })
	}
	crawlerFactories = factories
	fakeCrawlerPath := filepath.Join(dir, "fake-crawlers.json")
	data, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fakeCrawlerPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeCrawlersEnv, fakeCrawlerPath)
	t.Cleanup(func() {
		crawlerFactories = oldFactories
	})
	return dir
}

func loadFakeCrawlers(path string) ([]fakeCrawler, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var crawlers []fakeCrawler
	if err := json.Unmarshal(data, &crawlers); err != nil {
		return nil, err
	}
	return crawlers, nil
}

func fakeCrawlerFactories(crawlers []fakeCrawler) []func() trawlkit.Crawler {
	factories := make([]func() trawlkit.Crawler, 0, len(crawlers))
	for _, crawler := range crawlers {
		crawler := crawler
		factories = append(factories, func() trawlkit.Crawler { return newFakeSource(nil, crawler) })
	}
	return factories
}

func normaliseFakeCrawler(crawler *fakeCrawler) {
	if crawler.metadata == "" && crawler.metadataExit == 0 {
		crawler.metadata = metadataJSON(crawler.name)
	}
	if crawler.status == "" && crawler.statusExit == 0 {
		crawler.status = statusJSON(crawler.name, "ok")
	}
	if crawler.search == "" && crawler.searchExit == 0 {
		crawler.search = searchJSON("query")
	}
	if crawler.open == "" && crawler.openExit == 0 {
		crawler.open = `{"body":"Example body","ref":"msg/1"}`
	}
	if crawler.openHuman == "" && crawler.openHumanExit == 0 {
		crawler.openHuman = "Example body"
	}
	if crawler.sync == "" && crawler.syncExit == 0 {
		crawler.sync = `{"state":"ok","message":"0 new items"}`
	}
}

func newFakeSource(t *testing.T, crawler fakeCrawler) trawlkit.Crawler {
	if t != nil {
		t.Helper()
	}
	base := &fakeSource{t: t, crawler: crawler, manifest: fakeManifest(crawler)}
	hasWho := fakeHasCapability(base.manifest, "who")
	hasSearch := fakeHasCapability(base.manifest, "search")
	hasOpen := fakeHasCapability(base.manifest, "open")
	hasSync := fakeHasCapability(base.manifest, "sync")
	hasPeopleSnapshot := crawler.peopleSnapshot != nil
	switch {
	case hasSync && hasPeopleSnapshot:
		return &fakeSyncPeopleSnapshot{base}
	case hasSearch && hasOpen && hasSync && hasWho:
		return &fakeSearchOpenSyncWho{&fakeSearchOpenSync{base}}
	case hasSearch && hasOpen && hasSync:
		return &fakeSearchOpenSync{base}
	case hasSearch && hasOpen:
		return &fakeSearchOpen{base}
	case hasSearch:
		return &fakeSearch{base}
	case hasOpen:
		return &fakeOpen{base}
	case base.manifest.ID == "contacts" && strings.TrimSpace(crawler.who) != "":
		return &fakeWho{base}
	default:
		return base
	}
}

func fakeManifest(crawler fakeCrawler) control.Manifest {
	if crawler.metadataExit != 0 {
		return control.Manifest{ID: crawler.name}
	}
	var manifest control.Manifest
	if err := decodeContractJSON([]byte(crawler.metadata), &manifest); err != nil {
		return control.Manifest{ID: crawler.name}
	}
	if manifest.ID == "" {
		manifest.ID = crawler.name
	}
	if manifest.DisplayName == "" {
		manifest.DisplayName = manifest.ID
	}
	if manifest.Binary.Name == "" {
		manifest.Binary.Name = crawler.name
	}
	return manifest
}

func fakeHasCapability(manifest control.Manifest, capability string) bool {
	for _, candidate := range manifest.Capabilities {
		if strings.EqualFold(candidate, capability) {
			return true
		}
	}
	return false
}

type fakeSource struct {
	t        *testing.T
	crawler  fakeCrawler
	manifest control.Manifest
}

func (f *fakeSource) Info() trawlkit.Info {
	return trawlkit.Info{
		ID:          f.manifest.ID,
		Surface:     sourceAlias(f.manifest.DisplayName),
		Aliases:     f.manifest.Aliases,
		DisplayName: f.manifest.DisplayName,
		Headlines:   f.manifest.Headlines,
	}
}

func (f *fakeSource) fakeCrawler() {}

func (f *fakeSource) Verbs() []trawlkit.Verb {
	if f.crawler.metadataExit != 0 || strings.TrimSpace(f.crawler.metadata) == "not-json" {
		return []trawlkit.Verb{{Name: "status", Help: "invalid metadata"}}
	}
	commands := orderedFakeCommands(f.manifest)
	verbs := make([]trawlkit.Verb, 0, len(commands))
	if f.crawler.peopleMarker != "" {
		var input, source string
		verbs = append(verbs, trawlkit.Verb{
			Name:     clawdex.InternalPeopleReconcileVerb,
			Internal: true,
			Mutates:  true,
			Store:    trawlkit.StoreRequired,
			Flags: func(fs *flag.FlagSet) {
				fs.StringVar(&input, "input", "", "People snapshot file")
				fs.StringVar(&source, "source", "", "source id")
			},
			Run: func(_ context.Context, req *trawlkit.Request) error {
				data, err := os.ReadFile(input)
				if err != nil {
					return err
				}
				var exported control.PeopleSnapshot
				if err := json.Unmarshal(data, &exported); err != nil {
					return err
				}
				marker, err := json.Marshal(struct {
					Source string                 `json:"source"`
					Export control.PeopleSnapshot `json:"export"`
					Store  bool                   `json:"store"`
				}{Source: source, Export: exported, Store: req.Store != nil})
				if err != nil {
					return err
				}
				return os.WriteFile(f.crawler.peopleMarker, marker, 0o600)
			},
		})
	}
	for _, command := range commands {
		tokens := fixedVerbTokens(command)
		if len(tokens) == 0 {
			continue
		}
		name := strings.Join(tokens, " ")
		if fakeSpineVerb(name) {
			continue
		}
		verbName := name
		limit := ""
		verbs = append(verbs, trawlkit.Verb{
			Name:  verbName,
			Help:  command.Title,
			Store: trawlkit.StoreNone,
			Flags: func(fs *flag.FlagSet) {
				fs.StringVar(&limit, "limit", "", "limit")
			},
			Run: func(ctx context.Context, req *trawlkit.Request) error {
				_ = ctx
				args := append([]string(nil), req.Args...)
				if args == nil {
					args = []string{}
				}
				payload := map[string]any{
					"verb": verbName,
					"args": args,
				}
				if limit != "" {
					payload["limit"] = limit
				}
				if req.Format != ckoutput.JSON {
					_, err := fmt.Fprintf(req.Out, "verb=%s args=%s limit=%s\n", verbName, strings.Join(args, ","), limit)
					return err
				}
				return ckoutput.Write(req.Out, req.Format, verbName, payload)
			},
		})
	}
	return verbs
}

func (f *fakeSource) PrepareArchive(_ context.Context, path string) error {
	if f.crawler.prepareMarker == "" {
		return nil
	}
	file, err := os.OpenFile(f.crawler.prepareMarker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(file, "%s\n", path); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func orderedFakeCommands(manifest control.Manifest) []control.Command {
	keys := make([]string, 0, len(manifest.Commands))
	for key := range manifest.Commands {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]control.Command, 0, len(keys))
	for _, key := range keys {
		out = append(out, manifest.Commands[key])
	}
	return out
}

func fakeSpineVerb(name string) bool {
	switch strings.Join(strings.Fields(name), " ") {
	case "metadata", "status", "sync", "search", "open", "who":
		return true
	default:
		return false
	}
}

func (f *fakeSource) Status(ctx context.Context, req *trawlkit.Request) (*control.Status, error) {
	_ = ctx
	if req != nil {
		f.crawler.evidence.input(fmt.Sprintf("status source=%s format=%s", f.manifest.ID, req.Format))
	}
	f.crawler.evidence.statusCall()
	if f.crawler.statusCalls != nil {
		*f.crawler.statusCalls++
	}
	if f.crawler.statusExit != 0 {
		return nil, errors.New("status failed")
	}
	var status control.Status
	if err := decodeContractJSON([]byte(f.crawler.status), &status); err != nil {
		return nil, err
	}
	if projected, err := federation.ProjectStatus(f.manifest, &status); err == nil {
		f.crawler.evidence.status(projected)
	}
	return &status, nil
}

func (f *fakeSource) Chats(ctx context.Context, req *trawlkit.Request, query trawlkit.ChatQuery) ([]trawlkit.Chat, error) {
	_ = req
	if f.crawler.chatsError != "" {
		if f.crawler.chatsError == trawlkit.ErrChatsNoReadState.Error() {
			return nil, trawlkit.ErrChatsNoReadState
		}
		return nil, errors.New(f.crawler.chatsError)
	}
	rows := make([]trawlkit.Chat, 0, len(f.crawler.chats))
	for _, chat := range f.crawler.chats {
		if query.Unread && (chat.Unread == nil || *chat.Unread == 0) {
			continue
		}
		rows = append(rows, chat)
	}
	if query.Limit > 0 && len(rows) > query.Limit {
		rows = rows[:query.Limit]
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return rows, nil
	}
}

func (f *fakeSource) search(ctx context.Context, req *trawlkit.Request, query trawlkit.Query) (trawlkit.SearchResult, error) {
	if req != nil {
		f.crawler.evidence.input(fmt.Sprintf("search source=%s format=%s text=%q who=%q limit=%d after=%q before=%q", f.manifest.ID, req.Format, query.Text, query.Who, query.Limit, query.After.Format(time.RFC3339), query.Before.Format(time.RFC3339)))
	}
	f.crawler.evidence.searchCall()
	if f.crawler.searchCalls != nil {
		*f.crawler.searchCalls++
	}
	_ = req
	if f.crawler.searchSleep != "" {
		select {
		case <-time.After(parseFakeSleep(f.crawler.searchSleep)):
		case <-ctx.Done():
			return trawlkit.SearchResult{}, ctx.Err()
		}
	}
	if f.crawler.searchStderr != "" && req.Log != nil {
		for _, line := range strings.Split(f.crawler.searchStderr, "\n") {
			if strings.TrimSpace(line) != "" {
				_ = req.Log.Info("fake_stderr", line)
			}
		}
	}
	if f.crawler.searchQuery != "" && query.Text != f.crawler.searchQuery {
		return trawlkit.SearchResult{}, fmt.Errorf("query = %q, want %q", query.Text, f.crawler.searchQuery)
	}
	if f.crawler.searchNoQuery && query.Text != "" {
		return trawlkit.SearchResult{}, fmt.Errorf("query = %q, want empty", query.Text)
	}
	if f.crawler.searchLimit != "" && fmt.Sprint(query.Limit) != f.crawler.searchLimit {
		return trawlkit.SearchResult{}, fmt.Errorf("limit = %d, want %s", query.Limit, f.crawler.searchLimit)
	}
	if f.crawler.searchWho != "" && query.Who != f.crawler.searchWho {
		return trawlkit.SearchResult{}, fmt.Errorf("who = %q, want %q", query.Who, f.crawler.searchWho)
	}
	if f.crawler.searchExit != 0 {
		if code := fakeSearchContractErrorCode([]byte(f.crawler.search)); code != "" {
			return trawlkit.SearchResult{}, fakeErrorBody(code)
		}
		return trawlkit.SearchResult{}, errors.New("search failed")
	}
	envelope, err := decodeFakeSearchEnvelope([]byte(f.crawler.search))
	if err != nil {
		return trawlkit.SearchResult{}, err
	}
	hits := make([]trawlkit.Hit, 0, len(envelope.Results))
	for _, row := range envelope.Results {
		parsed, _ := time.Parse(time.RFC3339, row.Time)
		hit := trawlkit.Hit{
			Ref:          row.Ref,
			ShortRef:     firstNonEmpty(row.ShortRef, row.Alias),
			Time:         parsed,
			AnchorID:     trawlkit.MatchAnchorID,
			AllDay:       row.AllDay,
			Availability: row.Availability,
		}
		switch f.manifest.ID {
		case "calendar":
			hit.Summary = trawlkit.ResultSummary{Title: firstNonEmpty(row.Snippet, "Calendar event"), Subtitle: row.Calendar}
			hit.Archive = []trawlkit.ArchiveContext{{Kind: "calendar", Label: "In " + firstNonEmpty(row.Calendar, "Calendar")}}
			hit.Evidence = []trawlkit.EvidenceFragment{trawlkit.FieldMatch("Event match", "event", row.Snippet)}
		case "contacts":
			hit.Summary = trawlkit.ResultSummary{Title: firstNonEmpty(row.Who, "Contact"), Subtitle: "Contact"}
			hit.Archive = []trawlkit.ArchiveContext{{Kind: "source", Label: "In Contacts"}}
			hit.Evidence = []trawlkit.EvidenceFragment{trawlkit.FieldMatch("Contact field", "contact", row.Snippet)}
		case "gmail":
			hit.Summary = trawlkit.ResultSummary{Title: firstNonEmpty(row.Where, "(no subject)"), Subtitle: row.Who}
			hit.Archive = []trawlkit.ArchiveContext{{Kind: "direction", Label: "Received"}}
			hit.Evidence = []trawlkit.EvidenceFragment{trawlkit.TextMatch("Message body", row.Snippet)}
		case "imessage", "telegram", "whatsapp":
			hit.Summary = trawlkit.ResultSummary{Title: firstNonEmpty(row.Where, "Conversation"), Subtitle: firstNonEmpty(row.Who, "Unknown sender")}
			hit.Archive = []trawlkit.ArchiveContext{{Kind: "direction", Label: "Received"}}
			hit.Evidence = []trawlkit.EvidenceFragment{trawlkit.TextMatch("Message from "+firstNonEmpty(row.Who, "Unknown sender"), row.Snippet)}
		case "notes":
			hit.Summary = trawlkit.ResultSummary{Title: firstNonEmpty(row.Where, "Note"), Subtitle: firstNonEmpty(row.Where, "Notes")}
			hit.Archive = []trawlkit.ArchiveContext{{Kind: "notes", Label: "In " + firstNonEmpty(row.Where, "Notes")}}
			hit.Evidence = []trawlkit.EvidenceFragment{trawlkit.TextMatch("Note passage", row.Snippet)}
		case "photos":
			hit.Summary = trawlkit.ResultSummary{Title: firstNonEmpty(row.Who, "Photo"), Subtitle: row.Where}
			hit.Archive = []trawlkit.ArchiveContext{{Kind: "source", Label: "Photos library"}}
			hit.Evidence = []trawlkit.EvidenceFragment{trawlkit.TextMatch("Photo match", row.Snippet)}
		case "twitter":
			hit.Summary = trawlkit.ResultSummary{Title: firstNonEmpty(row.Who, "Post"), Subtitle: "Twitter (X)"}
			hit.Archive = []trawlkit.ArchiveContext{{Kind: "role", Label: "Archived post"}}
			hit.Evidence = []trawlkit.EvidenceFragment{trawlkit.TextMatch("Post text", row.Snippet)}
		default:
			hit.Summary = trawlkit.ResultSummary{Title: firstNonEmpty(row.Where, row.Who, row.Snippet, "Search result")}
			hit.Archive = []trawlkit.ArchiveContext{{Kind: "source", Label: "In " + firstNonEmpty(f.manifest.DisplayName, f.manifest.ID)}}
			hit.Evidence = []trawlkit.EvidenceFragment{trawlkit.TextMatch("Search match", row.Snippet)}
		}
		hits = append(hits, hit)
	}
	result := trawlkit.SearchResult{Results: hits, TotalMatches: envelope.TotalMatches, Truncated: envelope.Truncated}
	if projected, err := federation.ProjectSearch(f.manifest, result); err == nil {
		f.crawler.evidence.search(projected)
	}
	return result, nil
}

type fakeSearchResult struct {
	Ref          string `json:"ref"`
	ShortRef     string `json:"short_ref,omitempty"`
	Alias        string `json:"alias,omitempty"`
	Time         string `json:"time"`
	AllDay       bool   `json:"all_day"`
	Who          string `json:"who"`
	Where        string `json:"where"`
	Calendar     string `json:"calendar,omitempty"`
	Snippet      string `json:"snippet"`
	Availability *int64 `json:"availability,omitempty"`
}

type fakeSearchEnvelope struct {
	Query        string             `json:"query"`
	Results      []fakeSearchResult `json:"results"`
	TotalMatches int                `json:"total_matches"`
	Truncated    bool               `json:"truncated"`
}

func decodeFakeSearchEnvelope(data []byte) (fakeSearchEnvelope, error) {
	var raw struct {
		Query        string          `json:"query"`
		Results      json.RawMessage `json:"results"`
		TotalMatches int             `json:"total_matches"`
		Truncated    bool            `json:"truncated"`
	}
	if err := decodeContractJSON(data, &raw); err != nil {
		return fakeSearchEnvelope{}, err
	}
	trimmed := bytes.TrimSpace(raw.Results)
	if len(trimmed) == 0 || !bytes.HasPrefix(trimmed, []byte("[")) {
		return fakeSearchEnvelope{}, errors.New("search results array is missing")
	}
	var results []fakeSearchResult
	if err := decodeContractJSON(trimmed, &results); err != nil {
		return fakeSearchEnvelope{}, err
	}
	return fakeSearchEnvelope{
		Query:        raw.Query,
		Results:      results,
		TotalMatches: raw.TotalMatches,
		Truncated:    raw.Truncated,
	}, nil
}

func fakeSearchContractErrorCode(data []byte) string {
	if len(bytes.TrimSpace(data)) == 0 {
		return ""
	}
	var envelope ErrorEnvelope
	if err := decodeContractJSON(data, &envelope); err != nil {
		return ""
	}
	return strings.TrimSpace(envelope.Error.Code)
}

func parseFakeSleep(value string) time.Duration {
	if duration, err := time.ParseDuration(value); err == nil {
		return duration
	}
	duration, err := time.ParseDuration(value + "s")
	if err != nil {
		return 0
	}
	return duration
}

func (f *fakeSource) open(ctx context.Context, req *trawlkit.Request, ref string) error {
	_ = ctx
	expected := f.crawler.openRef
	if f.crawler.shortRefAlias != "" && req.Format == ckoutput.JSON {
		expected = f.crawler.shortRefAlias
	}
	if expected != "" && ref != expected {
		return fmt.Errorf("open ref = %q, want %q", ref, expected)
	}
	if f.crawler.openStderr != "" && req.Log != nil {
		_ = req.Log.Info("fake_stderr", f.crawler.openStderr)
	}
	if req.Format == ckoutput.JSON {
		_, _ = fmt.Fprintln(req.Out, f.crawler.open)
		if f.crawler.openExit != 0 {
			if envelope, ok := fakeOpenErrorEnvelope([]byte(f.crawler.open)); ok {
				return fakeErrorBody(envelope.Error.Code)
			}
			return errors.New("open failed")
		}
		return nil
	}
	if f.crawler.openHuman != "" {
		_, _ = fmt.Fprintln(req.Out, f.crawler.openHuman)
	}
	if f.crawler.openHumanExit != 0 {
		return errors.New("open failed")
	}
	return nil
}

func fakeOpenErrorEnvelope(data []byte) (ErrorEnvelope, bool) {
	var envelope ErrorEnvelope
	if err := decodeContractJSON(data, &envelope); err != nil || strings.TrimSpace(envelope.Error.Code) == "" {
		return ErrorEnvelope{}, false
	}
	return envelope, true
}

func (f *fakeSource) OpenRecord(_ context.Context, req *trawlkit.Request, ref string) (*openv1.OpenRecord, error) {
	format := ckoutput.JSON
	if req != nil {
		format = req.Format
	}
	f.crawler.evidence.input(fmt.Sprintf("open source=%s format=%s ref=%q", f.manifest.ID, format, ref))
	f.crawler.evidence.openCall()
	if f.crawler.openCalls != nil {
		*f.crawler.openCalls++
	}
	expected := f.crawler.openRef
	if alias := f.crawler.shortRefAlias; alias != "" && ref != alias && ref != expected && !f.crawler.openUnknownShortRef && !f.crawler.openAmbiguousShortRef {
		return nil, fmt.Errorf("open ref = %q, want %q or %q", ref, alias, expected)
	}
	if f.crawler.shortRefAlias == "" && expected != "" && ref != expected {
		return nil, fmt.Errorf("open ref = %q, want %q", ref, expected)
	}
	if f.crawler.shortRefAlias != "" && ref != expected {
		if f.crawler.openAmbiguousShortRef {
			return nil, fakeErrorBody("ambiguous_short_ref")
		}
		if f.crawler.openUnknownShortRef || ref != f.crawler.shortRefAlias {
			return nil, fakeErrorBody("unknown_short_ref")
		}
	}
	if f.crawler.openExit != 0 {
		return nil, errors.New("open failed")
	}
	if envelope, ok := fakeOpenErrorEnvelope([]byte(f.crawler.open)); ok {
		return nil, fakeErrorBody(envelope.Error.Code)
	}
	if f.crawler.openHumanExit != 0 {
		return nil, errors.New("open failed")
	}
	if f.crawler.openRecord != nil {
		f.crawler.evidence.open(f.crawler.openRecord)
		return f.crawler.openRecord, nil
	}
	openRef := f.crawler.openRef
	if openRef == "" {
		openRef = f.manifest.ID + ":item/example-1"
	}
	record := &openv1.OpenRecord{
		SourceId:     f.manifest.ID,
		OpenRef:      openRef,
		Data:         fakeOpenData(),
		Presentation: fakeOpenPresentation(f.crawler.openHuman),
	}
	f.crawler.evidence.open(record)
	return record, nil
}

func fakeOpenPresentation(human string) *presentationv1.PresentationDocument {
	human = firstNonEmpty(human, "Synthetic record")
	title, body, found := strings.Cut(human, "\n")
	if !found || strings.TrimSpace(body) == "" {
		body = title
	}
	return &presentationv1.PresentationDocument{
		Title:           title,
		PrimaryAnchorId: trawlkit.MatchAnchorID,
		Blocks: []*presentationv1.Block{{
			AnchorId: trawlkit.MatchAnchorID,
			Content:  &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: body}},
		}},
	}
}

func (f *fakeSource) ResolveResource(_ context.Context, _ *trawlkit.Request, request *presentationv1.ResourceRequest) (*presentationv1.ResourceResponse, error) {
	data := []byte("synthetic resource bytes")
	if request.GetSourceId() != f.manifest.ID || uint32(len(data)) > request.GetMaxBytes() {
		return nil, errors.New("synthetic resource request is invalid")
	}
	return &presentationv1.ResourceResponse{ResourceRef: request.GetResourceRef(), ContentType: "image/jpeg", Data: data}, nil
}

func fakeOpenData() *anypb.Any {
	value, err := anypb.New(&emptypb.Empty{})
	if err != nil {
		panic(err)
	}
	return value
}

func (f *fakeSource) sync(ctx context.Context, req *trawlkit.Request) (*trawlkit.SyncReport, error) {
	_ = req
	if f.crawler.syncSleep != "" {
		select {
		case <-time.After(parseFakeSleep(f.crawler.syncSleep)):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.crawler.syncExit != 0 {
		var envelope ErrorEnvelope
		if err := decodeContractJSON([]byte(f.crawler.sync), &envelope); err == nil && strings.TrimSpace(envelope.Error.Code) != "" {
			return nil, fakeError{body: ckoutput.ErrorBody{
				Code:    envelope.Error.Code,
				Message: envelope.Error.Message,
				Remedy:  envelope.Error.Remedy,
			}}
		}
		return nil, errors.New("sync failed")
	}
	outcome, ok := lastSyncOutcome([]byte(f.crawler.sync))
	if !ok {
		return nil, errors.New("sync did not return a final JSON outcome")
	}
	var report trawlkit.SyncReport
	report.Added = jsonNumberInt64(outcome.Added)
	report.Updated = jsonNumberInt64(outcome.Updated)
	report.Removed = jsonNumberInt64(outcome.Removed)
	report.Warnings = append([]string(nil), outcome.Warnings...)
	return &report, nil
}

func jsonNumberInt64(value json.Number) int64 {
	if value.String() == "" {
		return 0
	}
	parsed, err := value.Int64()
	if err != nil {
		return 0
	}
	return parsed
}

func (f *fakeSource) who(ctx context.Context, req *trawlkit.Request, person string) ([]whomatch.Candidate, error) {
	_ = ctx
	format := ckoutput.JSON
	if req != nil {
		format = req.Format
	}
	f.crawler.evidence.input(fmt.Sprintf("who source=%s format=%s person=%q", f.manifest.ID, format, person))
	if f.crawler.whoQuery != "" && person != f.crawler.whoQuery {
		return nil, fmt.Errorf("who query = %q, want %q", person, f.crawler.whoQuery)
	}
	if f.crawler.whoExit != 0 {
		return nil, errors.New("who failed")
	}
	envelope, err := decodeWhoEnvelope([]byte(f.crawler.who), f.manifest.ID)
	if err != nil {
		return nil, err
	}
	out := make([]whomatch.Candidate, 0, len(envelope.Candidates))
	for _, candidate := range envelope.Candidates {
		parsed, _ := time.Parse(time.RFC3339, candidate.LastSeen)
		out = append(out, whomatch.Candidate{
			Who:         candidate.Who,
			Identifiers: append([]string(nil), candidate.Identifiers...),
			LastSeen:    parsed,
			Messages:    int64(candidate.Messages),
		})
	}
	return out, nil
}

type fakeError struct {
	body ckoutput.ErrorBody
}

func fakeErrorBody(code string) fakeError {
	return fakeError{body: ckoutput.ErrorBody{Code: code, Message: code}}
}

func (e fakeError) Error() string {
	return e.body.Message
}

func (e fakeError) ErrorBody() ckoutput.ErrorBody {
	return e.body
}

type fakeSearch struct{ *fakeSource }

func (f *fakeSearch) Search(ctx context.Context, req *trawlkit.Request, query trawlkit.Query) (trawlkit.SearchResult, error) {
	return f.search(ctx, req, query)
}

type fakeOpen struct{ *fakeSource }

func (f *fakeOpen) Open(ctx context.Context, req *trawlkit.Request, ref string) error {
	return f.open(ctx, req, ref)
}

type fakeWho struct{ *fakeSource }

func (f *fakeWho) Who(ctx context.Context, req *trawlkit.Request, person string) ([]whomatch.Candidate, error) {
	return f.who(ctx, req, person)
}

type fakeSearchOpen struct{ *fakeSource }

func (f *fakeSearchOpen) Search(ctx context.Context, req *trawlkit.Request, query trawlkit.Query) (trawlkit.SearchResult, error) {
	return f.search(ctx, req, query)
}

func (f *fakeSearchOpen) Open(ctx context.Context, req *trawlkit.Request, ref string) error {
	return f.open(ctx, req, ref)
}

type fakeSearchOpenSync struct{ *fakeSource }

func (f *fakeSearchOpenSync) Search(ctx context.Context, req *trawlkit.Request, query trawlkit.Query) (trawlkit.SearchResult, error) {
	return f.search(ctx, req, query)
}

func (f *fakeSearchOpenSync) Open(ctx context.Context, req *trawlkit.Request, ref string) error {
	return f.open(ctx, req, ref)
}

func (f *fakeSearchOpenSync) Sync(ctx context.Context, req *trawlkit.Request) (*trawlkit.SyncReport, error) {
	return f.sync(ctx, req)
}

type fakeSyncPeopleSnapshot struct{ *fakeSource }

func (f *fakeSyncPeopleSnapshot) Sync(ctx context.Context, req *trawlkit.Request) (*trawlkit.SyncReport, error) {
	return f.sync(ctx, req)
}

func (f *fakeSyncPeopleSnapshot) PeopleSnapshot(context.Context, *trawlkit.Request) (*control.PeopleSnapshot, error) {
	return f.crawler.peopleSnapshot, nil
}

type fakeSearchOpenSyncWho struct{ *fakeSearchOpenSync }

func (f *fakeSearchOpenSyncWho) Who(ctx context.Context, req *trawlkit.Request, person string) ([]whomatch.Candidate, error) {
	return f.who(ctx, req, person)
}

func metadataJSON(id string) string {
	return fmt.Sprintf(`{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":%q,"display_name":%q}`, id, id)
}

func statusJSON(id, state string) string {
	return fmt.Sprintf(`{"app_id":%q,"state":%q,"last_sync_at":"2026-07-02T14:03:00Z","freshness":{"last_sync":"2026-07-02T14:03:00Z"},"counts":[{"id":"messages","label":"messages","value":12345}]}`, id, state)
}

func searchJSON(query string) string {
	return fmt.Sprintf(`{"query":%q,"results":[],"total_matches":0,"truncated":false}`, query)
}
