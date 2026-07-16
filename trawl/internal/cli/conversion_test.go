package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

type canonicalBoundarySource struct {
	Manifest   control.Manifest `json:"manifest"`
	Callback   bool             `json:"callback"`
	SkipReason string           `json:"skip_reason,omitempty"`
}

type canonicalBoundaryLogger struct {
	t                 *testing.T
	operations        int
	presentationCalls int
}

func logCanonicalBoundaryJSON(t *testing.T, label string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s:\n%s", label, data)
}

func (l *canonicalBoundaryLogger) observeStatus(sources []federation.StatusSource, response *federationv1.StatusResponse) {
	l.operations++
	input := make([]canonicalBoundarySource, 0, len(sources))
	for _, source := range sources {
		input = append(input, canonicalBoundarySource{Manifest: source.Manifest, Callback: source.Run != nil, SkipReason: source.SkipReason})
	}
	logCanonicalBoundaryJSON(l.t, "federation status input", struct {
		Sources []canonicalBoundarySource `json:"sources"`
	}{Sources: input})
	l.t.Logf("federation status protobuf output:\n%s", prototext.Format(response))
}

func (l *canonicalBoundaryLogger) observeSearch(sources []federation.SearchSource, query trawlkit.Query, order federationv1.SearchOrder, limit int, response *federationv1.SearchResponse) {
	l.operations++
	input := make([]canonicalBoundarySource, 0, len(sources))
	for _, source := range sources {
		input = append(input, canonicalBoundarySource{Manifest: source.Manifest, Callback: source.Run != nil, SkipReason: source.SkipReason})
	}
	logCanonicalBoundaryJSON(l.t, "federation search input", struct {
		Sources []canonicalBoundarySource `json:"sources"`
		Query   trawlkit.Query            `json:"query"`
		Order   string                    `json:"order"`
		Limit   int                       `json:"limit"`
	}{Sources: input, Query: query, Order: order.String(), Limit: limit})
	l.t.Logf("federation search protobuf output:\n%s", prototext.Format(response))
}

func (l *canonicalBoundaryLogger) observeOpen(sources []federation.OpenSource, sourceID, requestedRef string, response *openv1.OpenResponse) {
	l.operations++
	input := make([]canonicalBoundarySource, 0, len(sources))
	for _, source := range sources {
		input = append(input, canonicalBoundarySource{Manifest: source.Manifest, Callback: source.Run != nil, SkipReason: source.SkipReason})
	}
	logCanonicalBoundaryJSON(l.t, "federation open input", struct {
		Sources          []canonicalBoundarySource `json:"sources"`
		SelectedSourceID string                    `json:"selected_source_id"`
		RequestedRef     string                    `json:"requested_ref"`
	}{Sources: input, SelectedSourceID: sourceID, RequestedRef: requestedRef})
	l.t.Logf("federation open protobuf output:\n%s", prototext.Format(response))
}

func (l *canonicalBoundaryLogger) observePresentation(document *presentationv1.PresentationDocument) {
	l.presentationCalls++
	l.t.Logf("presentation protobuf input:\n%s", prototext.Format(document))
}

func TestStatusEnvelopeFromControlUsesTypedTrawlkitStatus(t *testing.T) {
	status := control.NewStatus("imessage", "Archive is fresh.")
	status.State = "ok"
	status.LastSyncAt = "2026-07-02T14:03:00Z"
	status.Counts = []control.Count{
		control.NewCount("messages", "messages", 42),
	}

	got, err := statusEnvelopeFromControl(Source{ID: "imessage"}, &status)
	if err != nil {
		t.Fatal(err)
	}
	if got.AppID != "imessage" || got.State != "ok" || got.Summary != "Recently synced." {
		t.Fatalf("status = %#v", got)
	}
	if got.LastSyncAt != "2026-07-02T14:03:00Z" || len(got.Counts) != 1 {
		t.Fatalf("typed fields were not preserved: %#v", got)
	}
	if got.Counts[0].Value.text("messages", "messages") != "42" {
		t.Fatalf("count value = %#v", got.Counts[0].Value)
	}
}

func TestWhoWrapperKeepsExistingSkipsAndDoesNotRunMissingIdentifier(t *testing.T) {
	called := 0
	sources := []federation.SearchSource{
		{Manifest: control.NewManifest("notes", "Notes", "notescrawl"), Run: func(_ context.Context, query trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			called++
			if query.Who != "notes-person" {
				return trawlkit.SearchResult{}, &federationv1.SourceFailure{Message: "wrong person"}
			}
			return trawlkit.SearchResult{}, nil
		}},
		{Manifest: control.NewManifest("calendar", "Calendar", "calcrawl"), SkipReason: "Search is not supported."},
		{Manifest: control.NewManifest("mail", "Mail", "mailcrawl"), Run: func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			return trawlkit.SearchResult{}, &federationv1.SourceFailure{Message: "must not run"}
		}},
	}
	wrapped := wrapWhoSearchSources(sources, map[string]string{"notes": "notes-person"})
	if _, failure := wrapped[0].Run(context.Background(), trawlkit.Query{}); failure != nil || called != 1 {
		t.Fatalf("resolved source = %v calls=%d", failure, called)
	}
	if wrapped[1].SkipReason != "Search is not supported." || wrapped[2].SkipReason != "Cannot filter by the resolved person." {
		t.Fatalf("skip reasons = %q, %q", wrapped[1].SkipReason, wrapped[2].SkipReason)
	}
}

func TestCanonicalJSONRoundTripsOptionalFields(t *testing.T) {
	present := int64(0)
	response := &federationv1.SearchResponse{
		Outcome:     federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE,
		Order:       federationv1.SearchOrder_SEARCH_ORDER_RECENCY,
		Hits:        []*federationv1.SearchHit{{SourceId: "notes", OpenRef: "notes:note/example-1", Availability: &present}},
		ResultLimit: 2,
	}
	var output bytes.Buffer
	if err := writeCanonicalJSON(&output, response); err != nil {
		t.Fatal(err)
	}
	var roundTrip federationv1.SearchResponse
	if err := (protojson.UnmarshalOptions{}).Unmarshal(output.Bytes(), &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(response, &roundTrip) || roundTrip.GetHits()[0].Availability == nil {
		t.Fatalf("canonical JSON lost optional presence: %s", output.String())
	}
}

func TestCanonicalJSONRoundTripsEveryResponseAndOptionalPresence(t *testing.T) {
	presentZero := int64(0)
	presentFalse := false
	responses := []proto.Message{
		&federationv1.StatusResponse{Outcome: federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE},
		&federationv1.SearchResponse{Outcome: federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE, Hits: []*federationv1.SearchHit{{SourceId: "notes", OpenRef: "notes:note/1"}, {SourceId: "notes", OpenRef: "notes:note/2", Availability: &presentZero, Unread: &presentFalse}}},
		&openv1.OpenResponse{Outcome: federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED, RequestedRef: "notes:note/1", Failure: &federationv1.SourceFailure{Code: federationv1.FailureCode_FAILURE_CODE_NOT_FOUND}},
	}
	for _, response := range responses {
		var output bytes.Buffer
		if err := writeCanonicalJSON(&output, response); err != nil {
			t.Fatal(err)
		}
		roundTrip := response.ProtoReflect().New().Interface()
		if err := (protojson.UnmarshalOptions{}).Unmarshal(output.Bytes(), roundTrip); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(response, roundTrip) {
			t.Fatalf("round trip changed %T: %s", response, output.String())
		}
	}
}

func decodeCanonicalSearchResponse(t *testing.T, text string) *federationv1.SearchResponse {
	t.Helper()
	var response federationv1.SearchResponse
	if err := (protojson.UnmarshalOptions{}).Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("search JSON = %s err=%v", text, err)
	}
	return &response
}

func TestCanonicalConsumerBoundaries(t *testing.T) {
	for _, width := range []string{"80", "120"} {
		t.Run("width-"+width, func(t *testing.T) {
			t.Setenv("COLUMNS", width)
			for _, test := range cliEvidenceCases() {
				t.Run(test.name, func(t *testing.T) {
					for _, mode := range []struct {
						name string
						args []string
					}{
						{name: "human", args: test.args},
						{name: "json", args: append([]string{"--json"}, test.args...)},
					} {
						t.Run(mode.name, func(t *testing.T) {
							record := &fakeCrawlerEvidence{}
							crawlers := test.crawlers(record)
							binDir := writeFakeCrawlers(t, crawlers...)
							t.Setenv("PATH", binDir)
							t.Setenv("HOME", syntheticHome(t))
							observer := &canonicalBoundaryLogger{t: t}
							stdout, stderr, exit := runCanonicalConsumerCLI(t, observer, mode.args...)
							t.Logf("CLI argv=%q\nstdout:\n%s\nstderr:\n%s\nexit=%d", mode.args, stdout, stderr, exit)
							logCrawlerBoundaries(t, record)
							if len(record.Inputs) == 0 && test.expectCallbacks {
								t.Fatal("expected at least one crawler callback")
							}
							if record.StatusCalls != test.statusCalls || record.SearchCalls != test.searchCalls || record.OpenCalls != test.openCalls {
								t.Fatalf("callback counts status=%d search=%d open=%d, want status=%d search=%d open=%d", record.StatusCalls, record.SearchCalls, record.OpenCalls, test.statusCalls, test.searchCalls, test.openCalls)
							}
							wantPresentationCalls := 0
							if mode.name == "human" {
								wantPresentationCalls = test.presentationCalls
							}
							if observer.presentationCalls != wantPresentationCalls {
								t.Fatalf("renderer calls = %d, want %d", observer.presentationCalls, wantPresentationCalls)
							}
							t.Logf("callback counts: inputs=%d status=%d search=%d open=%d renderer=%d canonical=%d", len(record.Inputs), record.StatusCalls, record.SearchCalls, record.OpenCalls, observer.presentationCalls, observer.operations)
						})
					}
				})
			}
		})
	}
}

func runCanonicalConsumerCLI(t *testing.T, observer canonicalConsumerObserver, args ...string) (string, string, int) {
	t.Helper()
	ensureSyntheticHome(t)
	ensureFakeArchives(t)
	var stdout, stderr bytes.Buffer
	err := executeWithCanonicalObserver(args, &stdout, &stderr, crawlerCommandTimeout, observer)
	return stdout.String(), stderr.String(), ExitCode(err)
}

type cliEvidenceCase struct {
	name              string
	args              []string
	expectCallbacks   bool
	statusCalls       int
	searchCalls       int
	openCalls         int
	presentationCalls int
	crawlers          func(*fakeCrawlerEvidence) []fakeCrawler
}

func evidenceMetadata(id, display string, capabilities ...string) string {
	quoted := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		quoted = append(quoted, fmt.Sprintf("%q", capability))
	}
	return fmt.Sprintf(`{"schema_version":1,"contract_version":1,"capabilities":[%s],"id":%q,"display_name":%q}`, strings.Join(quoted, ","), id, display)
}

func cliEvidenceCases() []cliEvidenceCase {
	status := func(state string, exit int, record *fakeCrawlerEvidence) fakeCrawler {
		return fakeCrawler{name: "notescrawl", metadata: evidenceMetadata("notes", "Notes", "status", "search", "open", "who"), status: statusJSON("notes", state), statusExit: exit, statusCalls: new(int), evidence: record}
	}
	search := func(payload string, record *fakeCrawlerEvidence) fakeCrawler {
		return fakeCrawler{name: "notescrawl", metadata: evidenceMetadata("notes", "Notes", "status", "search", "open", "who"), search: payload, searchCalls: new(int), evidence: record}
	}
	open := func(record *fakeCrawlerEvidence) fakeCrawler {
		return fakeCrawler{name: "notescrawl", metadata: evidenceMetadata("notes", "Notes", "status", "search", "open", "short_refs"), openRef: "notes:note/example-1", openHuman: "Synthetic note\nBody", openCalls: new(int), evidence: record}
	}
	return []cliEvidenceCase{
		{name: "status-complete", args: []string{"status"}, expectCallbacks: true, statusCalls: 1, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler { return []fakeCrawler{status("ok", 0, r)} }},
		{name: "status-partial", args: []string{"status"}, expectCallbacks: true, statusCalls: 2, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler {
			return []fakeCrawler{status("ok", 0, r), {name: "failcrawl", metadata: evidenceMetadata("calendar", "Calendar", "status"), statusExit: 1, statusCalls: new(int), evidence: r}}
		}},
		{name: "status-all-failed", args: []string{"status"}, expectCallbacks: true, statusCalls: 2, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler {
			return []fakeCrawler{{name: "fail-a", metadata: evidenceMetadata("a", "A", "status"), statusExit: 1, statusCalls: new(int), evidence: r}, {name: "fail-b", metadata: evidenceMetadata("b", "B", "status"), statusExit: 1, statusCalls: new(int), evidence: r}}
		}},
		{name: "status-selected-failure", args: []string{"status", "calendar"}, expectCallbacks: true, statusCalls: 1, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler {
			return []fakeCrawler{{name: "failcrawl", metadata: evidenceMetadata("calendar", "Calendar", "status"), statusExit: 1, statusCalls: new(int), evidence: r}}
		}},
		{name: "status-missing-with-failure", args: []string{"status", "notes"}, expectCallbacks: true, statusCalls: 1, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler {
			return []fakeCrawler{status("missing", 0, r)}
		}},
		{name: "search-order-truncation", args: []string{"search", "synthetic", "--limit", "2"}, expectCallbacks: true, searchCalls: 2, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler {
			return []fakeCrawler{search(`{"query":"synthetic","results":[{"ref":"notes:note/new","time":"2026-07-12T10:00:00Z","snippet":"new"},{"ref":"notes:note/old","time":"2026-07-12T08:00:00Z","snippet":"old"}],"total_matches":3,"truncated":true}`, r), {name: "calendarcrawl", metadata: evidenceMetadata("calendar", "Calendar", "search"), search: `{"query":"synthetic","results":[{"ref":"calendar:event/mid","time":"2026-07-12T09:00:00Z","snippet":"mid"}],"total_matches":1}`, searchCalls: new(int), evidence: r}}
		}},
		{name: "search-failure-skip", args: []string{"search", "synthetic"}, expectCallbacks: true, searchCalls: 2, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler {
			return []fakeCrawler{{name: "failcrawl", metadata: evidenceMetadata("fail", "Failure", "search"), searchExit: 1, searchCalls: new(int), evidence: r}, {name: "statuscrawl", metadata: evidenceMetadata("status", "Status", "status"), evidence: r}, search(`{"query":"synthetic","results":[],"total_matches":0}`, r)}
		}},
		{name: "search-who-resolved", args: []string{"search", "synthetic", "--who", "Alex"}, expectCallbacks: true, searchCalls: 1, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler {
			return []fakeCrawler{
				{name: "imsgcrawl", metadata: evidenceMetadata("imessage", "Messages", "status", "sync", "search", "open", "who"), whoQuery: "Alex", who: `{"query":"Alex","candidates":[{"who":"Alex Example","identifiers":["alex@example.com"],"match_quality":"exact","sources":["imessage"],"messages":1}]}`, searchWho: "alex@example.com", search: `{"query":"synthetic","results":[],"total_matches":0}`, searchCalls: new(int), evidence: r},
				{name: "notescrawl", metadata: evidenceMetadata("notes", "Notes", "search"), search: `{"query":"synthetic","results":[],"total_matches":0}`, searchCalls: new(int), evidence: r},
				{name: "calendarcrawl", metadata: evidenceMetadata("calendar", "Calendar", "status"), evidence: r},
			}
		}},
		{name: "open-full-record", args: []string{"open", "notes:note/example-1"}, expectCallbacks: true, openCalls: 1, presentationCalls: 1, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler { return []fakeCrawler{open(r)} }},
		{name: "open-unique-short-ref", args: []string{"open", "  t7k3f  "}, expectCallbacks: true, openCalls: 2, presentationCalls: 1, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler {
			c := open(r)
			c.shortRefAlias = "t7k3f"
			return []fakeCrawler{c}
		}},
		{name: "open-unknown-short-ref", args: []string{"open", "z9x8c"}, expectCallbacks: true, openCalls: 1, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler {
			c := open(r)
			c.shortRefAlias = "t7k3f"
			c.openUnknownShortRef = true
			return []fakeCrawler{c}
		}},
		{name: "open-invalid-short-ref", args: []string{"open", "bad"}, expectCallbacks: false, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler { return []fakeCrawler{open(r)} }},
		{name: "open-all-candidate-failure", args: []string{"open", "t7k3f"}, expectCallbacks: true, openCalls: 1, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler {
			c := open(r)
			c.shortRefAlias = "t7k3f"
			c.openExit = 1
			return []fakeCrawler{c}
		}},
		{name: "open-ambiguous-short-ref", args: []string{"open", "t7k3f"}, expectCallbacks: true, openCalls: 1, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler {
			c := open(r)
			c.shortRefAlias = "t7k3f"
			c.openAmbiguousShortRef = true
			return []fakeCrawler{c}
		}},
		{name: "open-malformed-full-ref", args: []string{"open", "notes:"}, expectCallbacks: false, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler { return []fakeCrawler{open(r)} }},
		{name: "open-foreign-full-ref", args: []string{"open", "calendar:event/example-1"}, expectCallbacks: false, crawlers: func(r *fakeCrawlerEvidence) []fakeCrawler { return []fakeCrawler{open(r)} }},
	}
}

func logCrawlerBoundaries(t *testing.T, evidence *fakeCrawlerEvidence) {
	t.Helper()
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	t.Logf("crawler callback input:\n%s", strings.Join(evidence.Inputs, "\n"))
	status := strings.Builder{}
	for _, response := range evidence.StatusResponses {
		status.WriteString(prototext.Format(response))
		status.WriteByte('\n')
	}
	t.Logf("crawler status protobuf output:\n%s", status.String())
	search := strings.Builder{}
	for _, response := range evidence.SearchResponses {
		search.WriteString(prototext.Format(response))
		search.WriteByte('\n')
	}
	t.Logf("crawler search protobuf output:\n%s", search.String())
	open := strings.Builder{}
	for _, response := range evidence.OpenRecords {
		open.WriteString(prototext.Format(response))
		open.WriteByte('\n')
	}
	t.Logf("crawler open protobuf output:\n%s", open.String())
}

func TestStatusResultsJoinFailuresAndSkipsInAdapterOrder(t *testing.T) {
	sources := []Source{{ID: "notes", DisplayName: "Notes"}, {ID: "calendar", DisplayName: "Calendar"}}
	response := &federationv1.StatusResponse{
		Failures:       []*federationv1.SourceFailure{{SourceId: "calendar", Message: "Permission denied."}},
		SkippedSources: []*federationv1.SkippedSource{{SourceId: "notes", Reason: "Status is not supported."}},
	}
	results, err := statusResultsFromResponse(sources, response)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Source.ID != "notes" || results[0].Status.State != "skipped" || results[1].Status.Summary != "Permission denied." {
		t.Fatalf("status results = %#v", results)
	}
}

func TestStatusSkippedResponseRendersTableAndDetail(t *testing.T) {
	manifest := control.NewManifest("calendar", "Calendar", "calcrawl")
	response := federation.Status(context.Background(), []federation.StatusSource{{Manifest: manifest, SkipReason: "Status is not supported."}})
	sources := []Source{{ID: "calendar", DisplayName: "Calendar"}}
	results, err := statusResultsFromResponse(sources, response)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status.State != "skipped" {
		t.Fatalf("results = %#v", results)
	}
	var output bytes.Buffer
	if err := renderStatusDetail(&output, results[0], time.Time{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "state: skipped") || !strings.Contains(output.String(), "summary: Status is not supported.") {
		t.Fatalf("detail = %s", output.String())
	}
	var jsonOutput bytes.Buffer
	if err := writeCanonicalJSON(&jsonOutput, response); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOutput.String(), `"skipped_sources"`) || response.GetOutcome() != federationv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL {
		t.Fatalf("response = %s", jsonOutput.String())
	}
}

func TestStatusMissingWithFailureRendersOneResult(t *testing.T) {
	manifest := control.NewManifest("calendar", "Calendar", "calcrawl")
	status := control.NewStatus("calendar", "Not synced yet.")
	status.State = "missing"
	response := federation.Status(context.Background(), []federation.StatusSource{{Manifest: manifest, Run: func(context.Context) (*control.Status, *federationv1.SourceFailure) { return &status, nil }}})
	results, err := statusResultsFromResponse([]Source{{ID: "calendar", DisplayName: "Calendar"}}, response)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status.State != "missing" {
		t.Fatalf("results = %#v", results)
	}
	var stdout, stderr bytes.Buffer
	if err := renderStatusDetail(&stdout, results[0], time.Time{}); err != nil {
		t.Fatal(err)
	}
	(&Runtime{stderr: &stderr}).reportFederationOutcomes(response.GetFailures(), nil, "status")
	if strings.Count(stdout.String(), "state:") != 1 || len(response.GetFailures()) != 1 || !strings.Contains(stderr.String(), "Calendar status failed:") {
		t.Fatalf("stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func TestNormalizeStatusOwnsUnsyncedSummary(t *testing.T) {
	for _, state := range []string{"missing", "error"} {
		got := normalizeStatus(Source{ID: "gmail", DisplayName: "Gmail"}, StatusEnvelope{
			State:   state,
			Summary: "crawler-specific unsynced wording",
		})
		if got.Summary != "Not synced yet." {
			t.Fatalf("state %s summary = %q, want uniform unsynced summary", state, got.Summary)
		}
	}
}

func TestWhoCandidatesFromMatchesConvertsTypedMatches(t *testing.T) {
	lastSeen := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	got := whoCandidatesFromMatches([]whomatch.Candidate{{
		Who:         "Alex Example",
		Identifiers: []string{"alex@example.com"},
		LastSeen:    lastSeen,
		Messages:    7,
	}}, "imessage", "alex@example.com")

	if len(got) != 1 {
		t.Fatalf("candidates = %#v", got)
	}
	candidate := got[0]
	if candidate.Who != "Alex Example" || candidate.MatchQuality != "exact" || candidate.LastSeen != "2026-07-01T08:00:00Z" || candidate.Messages != 7 {
		t.Fatalf("candidate = %#v", candidate)
	}
	if len(candidate.Sources) != 1 || candidate.Sources[0] != "imessage" {
		t.Fatalf("sources = %#v", candidate.Sources)
	}
}
