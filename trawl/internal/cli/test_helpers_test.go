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
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
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
	name          string
	metadata      string
	metadataExit  int
	status        string
	statusExit    int
	doctor        string
	doctorExit    int
	search        string
	searchExit    int
	searchSleep   string
	searchStderr  string
	searchLimit   string
	searchQuery   string
	searchNoQuery bool
	searchWho     string
	who           string
	whoExit       int
	whoQuery      string
	shortRefAlias string
	open          string
	openExit      int
	openRef       string
	openHuman     string
	openHumanExit int
	openStderr    string
	sync          string
	syncExit      int
	syncSleep     string
}

type fakeCrawlerWire struct {
	Name          string `json:"name"`
	Metadata      string `json:"metadata"`
	MetadataExit  int    `json:"metadata_exit"`
	Status        string `json:"status"`
	StatusExit    int    `json:"status_exit"`
	Doctor        string `json:"doctor"`
	DoctorExit    int    `json:"doctor_exit"`
	Search        string `json:"search"`
	SearchExit    int    `json:"search_exit"`
	SearchSleep   string `json:"search_sleep"`
	SearchStderr  string `json:"search_stderr"`
	SearchLimit   string `json:"search_limit"`
	SearchQuery   string `json:"search_query"`
	SearchNoQuery bool   `json:"search_no_query"`
	SearchWho     string `json:"search_who"`
	Who           string `json:"who"`
	WhoExit       int    `json:"who_exit"`
	WhoQuery      string `json:"who_query"`
	ShortRefAlias string `json:"short_ref_alias"`
	Open          string `json:"open"`
	OpenExit      int    `json:"open_exit"`
	OpenRef       string `json:"open_ref"`
	OpenHuman     string `json:"open_human"`
	OpenHumanExit int    `json:"open_human_exit"`
	OpenStderr    string `json:"open_stderr"`
	Sync          string `json:"sync"`
	SyncExit      int    `json:"sync_exit"`
	SyncSleep     string `json:"sync_sleep"`
}

func (f fakeCrawler) MarshalJSON() ([]byte, error) {
	return json.Marshal(fakeCrawlerWire{
		Name:          f.name,
		Metadata:      f.metadata,
		MetadataExit:  f.metadataExit,
		Status:        f.status,
		StatusExit:    f.statusExit,
		Doctor:        f.doctor,
		DoctorExit:    f.doctorExit,
		Search:        f.search,
		SearchExit:    f.searchExit,
		SearchSleep:   f.searchSleep,
		SearchStderr:  f.searchStderr,
		SearchLimit:   f.searchLimit,
		SearchQuery:   f.searchQuery,
		SearchNoQuery: f.searchNoQuery,
		SearchWho:     f.searchWho,
		Who:           f.who,
		WhoExit:       f.whoExit,
		WhoQuery:      f.whoQuery,
		ShortRefAlias: f.shortRefAlias,
		Open:          f.open,
		OpenExit:      f.openExit,
		OpenRef:       f.openRef,
		OpenHuman:     f.openHuman,
		OpenHumanExit: f.openHumanExit,
		OpenStderr:    f.openStderr,
		Sync:          f.sync,
		SyncExit:      f.syncExit,
		SyncSleep:     f.syncSleep,
	})
}

func (f *fakeCrawler) UnmarshalJSON(data []byte) error {
	var wire fakeCrawlerWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*f = fakeCrawler{
		name:          wire.Name,
		metadata:      wire.Metadata,
		metadataExit:  wire.MetadataExit,
		status:        wire.Status,
		statusExit:    wire.StatusExit,
		doctor:        wire.Doctor,
		doctorExit:    wire.DoctorExit,
		search:        wire.Search,
		searchExit:    wire.SearchExit,
		searchSleep:   wire.SearchSleep,
		searchStderr:  wire.SearchStderr,
		searchLimit:   wire.SearchLimit,
		searchQuery:   wire.SearchQuery,
		searchNoQuery: wire.SearchNoQuery,
		searchWho:     wire.SearchWho,
		who:           wire.Who,
		whoExit:       wire.WhoExit,
		whoQuery:      wire.WhoQuery,
		shortRefAlias: wire.ShortRefAlias,
		open:          wire.Open,
		openExit:      wire.OpenExit,
		openRef:       wire.OpenRef,
		openHuman:     wire.OpenHuman,
		openHumanExit: wire.OpenHumanExit,
		openStderr:    wire.OpenStderr,
		sync:          wire.Sync,
		syncExit:      wire.SyncExit,
		syncSleep:     wire.SyncSleep,
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
	home, err := os.MkdirTemp("/private/tmp", "trawl-147-home-")
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
	if crawler.doctor == "" && crawler.doctorExit == 0 {
		crawler.doctor = `{"checks":[{"id":"source_store","state":"ok"}]}`
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
	switch {
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
	}
}

func (f *fakeSource) fakeCrawler() {}

func (f *fakeSource) Verbs() []trawlkit.Verb {
	if f.crawler.metadataExit != 0 || strings.TrimSpace(f.crawler.metadata) == "not-json" {
		return []trawlkit.Verb{{Name: "status", Help: "invalid metadata"}}
	}
	commands := orderedFakeCommands(f.manifest)
	verbs := make([]trawlkit.Verb, 0, len(commands))
	for _, command := range commands {
		tokens := fixedVerbTokens(command)
		if len(tokens) == 0 {
			continue
		}
		name := strings.Join(tokens, " ")
		headline := fakeCommandHeadline(f.manifest, command)
		if fakeSpineVerb(name) {
			continue
		}
		verbName := name
		limit := ""
		verbs = append(verbs, trawlkit.Verb{
			Name:     verbName,
			Help:     command.Title,
			Headline: headline,
			Store:    trawlkit.StoreNone,
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

func orderedFakeCommands(manifest control.Manifest) []control.Command {
	keys := make([]string, 0, len(manifest.Commands))
	for key := range manifest.Commands {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	used := map[string]struct{}{}
	out := make([]control.Command, 0, len(keys))
	for _, headline := range manifest.Headlines {
		for _, key := range keys {
			if _, ok := used[key]; ok {
				continue
			}
			command := manifest.Commands[key]
			if fakeHeadlineVerb(command) != headline {
				continue
			}
			out = append(out, command)
			used[key] = struct{}{}
		}
	}
	for _, key := range keys {
		if _, ok := used[key]; ok {
			continue
		}
		out = append(out, manifest.Commands[key])
	}
	return out
}

func fakeCommandHeadline(manifest control.Manifest, command control.Command) bool {
	if command.Headline {
		return true
	}
	headline := fakeHeadlineVerb(command)
	for _, want := range manifest.Headlines {
		if headline == want {
			return true
		}
	}
	return false
}

func fakeHeadlineVerb(command control.Command) string {
	tokens := fixedVerbTokens(command)
	if len(tokens) == 0 {
		return ""
	}
	return tokens[0]
}

func fakeSpineVerb(name string) bool {
	switch strings.Join(strings.Fields(name), " ") {
	case "metadata", "status", "doctor", "sync", "search", "open", "who", "contacts export":
		return true
	default:
		return false
	}
}

func (f *fakeSource) Status(ctx context.Context, req *trawlkit.Request) (*control.Status, error) {
	_ = ctx
	_ = req
	if f.crawler.statusExit != 0 {
		return nil, errors.New("status failed")
	}
	var status control.Status
	if err := decodeContractJSON([]byte(f.crawler.status), &status); err != nil {
		return nil, err
	}
	return &status, nil
}

func (f *fakeSource) Doctor(ctx context.Context, req *trawlkit.Request) (*trawlkit.Doctor, error) {
	_ = ctx
	_ = req
	if f.crawler.doctorExit != 0 {
		return nil, errors.New("doctor failed")
	}
	var envelope DoctorEnvelope
	if err := decodeContractJSON([]byte(f.crawler.doctor), &envelope); err != nil {
		return nil, err
	}
	checks := make([]trawlkit.Check, 0, len(envelope.Checks))
	for _, check := range envelope.Checks {
		checks = append(checks, trawlkit.Check{
			ID:      check.ID,
			State:   check.State,
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return &trawlkit.Doctor{Checks: checks}, nil
}

func (f *fakeSource) search(ctx context.Context, req *trawlkit.Request, query trawlkit.Query) (trawlkit.SearchResult, error) {
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
		hits = append(hits, trawlkit.Hit{
			Ref:          row.Ref,
			ShortRef:     firstNonEmpty(row.ShortRef, row.Alias),
			Time:         parsed,
			Who:          row.Who,
			Where:        row.Where,
			Calendar:     row.Calendar,
			Snippet:      row.Snippet,
			AllDay:       row.AllDay,
			Availability: row.Availability,
		})
	}
	return trawlkit.SearchResult{Results: hits, TotalMatches: envelope.TotalMatches, Truncated: envelope.Truncated}, nil
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
			if envelope, ok := shortRefErrorEnvelope([]byte(f.crawler.open)); ok {
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
	_ = req
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

type fakeSearchOpenSyncWho struct{ *fakeSearchOpenSync }

func (f *fakeSearchOpenSyncWho) Who(ctx context.Context, req *trawlkit.Request, person string) ([]whomatch.Candidate, error) {
	return f.who(ctx, req, person)
}

func metadataJSON(id string) string {
	return fmt.Sprintf(`{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":%q,"display_name":%q}`, id, id)
}

func statusJSON(id, state string) string {
	return fmt.Sprintf(`{"app_id":%q,"state":%q,"last_sync_at":"2026-07-02T14:03:00Z","freshness":{"last_sync":"2026-07-02T14:03:00Z"},"counts":[{"id":"messages","label":"messages","value":12345}]}`, id, state)
}

func searchJSON(query string) string {
	return fmt.Sprintf(`{"query":%q,"results":[],"total_matches":0,"truncated":false}`, query)
}

func failingDoctorJSON() string {
	return `{"checks":[{"id":"tcc_full_disk_access","state":"fail","message":"cannot read the source database","remedy":"grant Full Disk Access to Trawl in System Settings > Privacy"}]}`
}
