package federation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

func TestFederatedSearchPreservesFacts(t *testing.T) {
	availability := int64(2)
	unread := true
	manifest := manifestFixture("calendar", "Calendar")
	result := trawlkit.SearchResult{
		WhoResolved: &trawlkit.WhoResolved{Who: "Casey Example", Identifiers: []string{"casey@example.com", "+15550001001"}},
		Results: []trawlkit.Hit{{
			Source: "calendar", Ref: "calendar:event/example-1", ShortRef: "cal:1",
			Time:     time.Date(2026, 7, 12, 9, 30, 0, 123000000, time.FixedZone("CEST", 2*60*60)),
			AnchorID: trawlkit.MatchAnchorID,
			Summary:  trawlkit.ResultSummary{Title: "Synthetic launch review", Subtitle: "Work"},
			Archive:  []trawlkit.ArchiveContext{{Kind: "calendar", Label: "In Work calendar"}},
			Evidence: []trawlkit.EvidenceFragment{
				trawlkit.FieldMatch("Attendee", "attendee", "Casey Example"),
				trawlkit.FieldMatch("Location", "location", "Canal room"),
			},
			AllDay:       false,
			Availability: &availability, Unread: &unread,
		}},
		TotalMatches: 3,
		Truncated:    true,
	}
	projected, err := ProjectSearch(manifest, result)
	if err != nil {
		t.Fatal(err)
	}
	if projected.SourceId != "calendar" || projected.DisplayName != "Calendar" || projected.TotalMatches != 3 || !projected.TotalIsExact || !projected.Truncated {
		t.Fatalf("projected summary = %#v", projected)
	}
	if projected.WhoResolved == nil || len(projected.WhoResolved.Identifiers) != 2 {
		t.Fatalf("who = %#v", projected.WhoResolved)
	}
	hit := projected.Hits[0]
	if hit.SourceId != "calendar" || hit.OpenRef != "calendar:event/example-1" || hit.ShortRef != "cal:1" || hit.TimeRfc3339 != "2026-07-12T09:30:00.123+02:00" {
		t.Fatalf("hit identity = %#v", hit)
	}
	if hit.AnchorId != trawlkit.MatchAnchorID || hit.GetSummary().GetTitle() != "Synthetic launch review" || hit.GetSummary().GetSubtitle() != "Work" || len(hit.ArchiveContext) != 1 || hit.ArchiveContext[0].GetLabel() != "In Work calendar" || len(hit.Evidence) != 2 {
		t.Fatalf("hit match contract = %#v", hit)
	}
	if hit.Availability == nil || hit.GetAvailability() != 2 || hit.Unread == nil || !hit.GetUnread() {
		t.Fatalf("optional hit facts = %#v", hit)
	}
	*hit.Availability = 9
	*hit.Unread = false
	if *result.Results[0].Availability != 2 || !*result.Results[0].Unread {
		t.Fatalf("projection aliases input: %#v", result.Results[0])
	}
}

func TestProjectSearchRoundTripsEveryEvidenceKind(t *testing.T) {
	projected, err := ProjectSearch(manifestFixture("gmail", "Gmail"), trawlkit.SearchResult{
		Results: []trawlkit.Hit{{
			Ref: "gmail:msg/example-1", AnchorID: "body",
			Summary: trawlkit.ResultSummary{Title: "Synthetic message"},
			Evidence: []trawlkit.EvidenceFragment{
				trawlkit.TextMatch("Message body", "Synthetic message text"),
				trawlkit.FieldMatch("Subject", "subject", "Synthetic subject"),
				trawlkit.MediaMatch("Attachment", "gmail:resource/example-1", "Synthetic attachment"),
				trawlkit.RelationMatch("Replying to", "reply", "Synthetic parent message"),
			},
		}},
		TotalMatches: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := proto.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	decoded := &federationv1.SearchSourceResult{}
	if err := proto.Unmarshal(encoded, decoded); err != nil {
		t.Fatal(err)
	}
	evidence := decoded.GetHits()[0].GetEvidence()
	if len(evidence) != 4 || evidence[0].GetText() == nil || evidence[1].GetField() == nil || evidence[2].GetMedia() == nil || evidence[3].GetRelation() == nil {
		t.Fatalf("round-tripped evidence = %#v", evidence)
	}
	if got := evidence[2].GetMedia().GetResourceRef(); got != "gmail:resource/example-1" {
		t.Fatalf("attachment resource ref = %q", got)
	}
}

func TestProjectSearchPinsCompleteProtobufText(t *testing.T) {
	projected, err := ProjectSearch(manifestFixture("notes", "Notes"), trawlkit.SearchResult{
		Results:      []trawlkit.Hit{federationSearchHit("notes:note/example-1", "Synthetic note", "Synthetic note", mustTime("2026-07-12T09:00:00Z"))},
		TotalMatches: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "" +
		"source_id: \"notes\"\n" +
		"display_name: \"Notes\"\n" +
		"hits: {\n" +
		"  source_id: \"notes\"\n" +
		"  open_ref: \"notes:note/example-1\"\n" +
		"  time_rfc3339: \"2026-07-12T09:00:00Z\"\n" +
		"  anchor_id: \"match\"\n" +
		"  summary: {\n" +
		"    title: \"Synthetic note\"\n" +
		"  }\n" +
		"  evidence: {\n" +
		"    label: \"Matching text\"\n" +
		"    text: {\n" +
		"      runs: {\n" +
		"        text: \"Synthetic note\"\n" +
		"        matched: true\n" +
		"      }\n" +
		"    }\n" +
		"  }\n" +
		"  archive_context: {\n" +
		"    kind: \"notes\"\n" +
		"    label: \"In Notes\"\n" +
		"  }\n" +
		"}\n" +
		"total_matches: 1\n" +
		"total_is_exact: true\n"
	if got := prototext.Format(projected); got != want {
		t.Fatalf("search protobuf text changed\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestSearchOrdersAndBoundsDeterministically(t *testing.T) {
	one := manifestFixture("one", "One")
	two := manifestFixture("two", "Two")
	results := map[string]trawlkit.SearchResult{
		"one": {Results: []trawlkit.Hit{
			federationSearchHit("one:new", "One new", "one rank zero", mustTime("2026-07-12T10:00:00Z")),
			federationSearchHit("one:old", "One old", "one rank one", mustTime("2026-07-12T08:00:00Z")),
		}, TotalMatches: 2},
		"two": {Results: []trawlkit.Hit{
			federationSearchHit("two:middle", "Two middle", "two rank zero", mustTime("2026-07-12T09:00:00Z")),
			federationSearchHit("two:untimed", "Two untimed", "two rank one", time.Time{}),
		}, TotalMatches: 5, TotalIsLowerBound: true, Truncated: true},
	}
	limits := make(chan int, 2)
	sources := []SearchSource{
		{Manifest: one, Run: func(_ context.Context, query trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			limits <- query.Limit
			return results["one"], nil
		}},
		{Manifest: two, Run: func(_ context.Context, query trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			limits <- query.Limit
			return results["two"], nil
		}},
	}
	recency := Search(context.Background(), sources, trawlkit.Query{Text: "launch"}, federationv1.SearchOrder_SEARCH_ORDER_RECENCY, 3)
	for range 2 {
		if limit := <-limits; limit != 3 {
			t.Fatalf("source limit = %d", limit)
		}
	}
	if got := hitRefs(recency.Hits); !equalStrings(got, []string{"one:new", "two:middle", "one:old"}) {
		t.Fatalf("recency = %#v", got)
	}
	if !recency.Truncated || recency.Outcome != federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE {
		t.Fatalf("recency outcome = %s, truncated=%t", recency.Outcome, recency.Truncated)
	}
	if !recency.Sources[0].GetTotalIsExact() || recency.Sources[1].GetTotalIsExact() {
		t.Fatalf("source exactness = %#v", recency.Sources)
	}

	relevance := Search(context.Background(), sources, trawlkit.Query{Text: "launch"}, federationv1.SearchOrder_SEARCH_ORDER_RELEVANCE, 4)
	for range 2 {
		if limit := <-limits; limit != 4 {
			t.Fatalf("source limit = %d", limit)
		}
	}
	if got := hitRefs(relevance.Hits); !equalStrings(got, []string{"one:new", "two:middle", "one:old", "two:untimed"}) {
		t.Fatalf("relevance = %#v", got)
	}
}

func TestSearchPreservesEmptyPartialFailedSkippedAndTimeout(t *testing.T) {
	empty := Search(context.Background(), []SearchSource{{
		Manifest: manifestFixture("notes", "Notes"),
		Run: func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			return trawlkit.SearchResult{Results: []trawlkit.Hit{}, TotalMatches: 0}, nil
		},
	}}, trawlkit.Query{Text: "none"}, federationv1.SearchOrder_SEARCH_ORDER_RECENCY, 20)
	if empty.Outcome != federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE || len(empty.Hits) != 0 {
		t.Fatalf("empty = %#v", empty)
	}

	partial := Search(context.Background(), []SearchSource{
		{Manifest: manifestFixture("notes", "Notes"), Run: func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			return trawlkit.SearchResult{Results: []trawlkit.Hit{federationSearchHit("notes:one", "One note", "one", time.Time{})}, TotalMatches: 1}, nil
		}},
		{Manifest: manifestFixture("photos", "Photos"), SkipReason: "not selected"},
		{Manifest: manifestFixture("calendar", "Calendar"), Run: func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			return trawlkit.SearchResult{}, &federationv1.SourceFailure{Code: federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE, Message: "Calendar is unavailable."}
		}},
	}, trawlkit.Query{Text: "one"}, federationv1.SearchOrder_SEARCH_ORDER_RECENCY, 20)
	if partial.Outcome != federationv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(partial.Failures) != 1 || len(partial.SkippedSources) != 1 {
		t.Fatalf("partial = %#v", partial)
	}

	failed := Search(context.Background(), []SearchSource{{
		Manifest: manifestFixture("calendar", "Calendar"),
		Run: func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			return trawlkit.SearchResult{}, &federationv1.SourceFailure{Code: federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE, Message: "Calendar is unavailable."}
		},
	}}, trawlkit.Query{Text: "one"}, federationv1.SearchOrder_SEARCH_ORDER_RECENCY, 20)
	if failed.Outcome != federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED {
		t.Fatalf("failed = %s", failed.Outcome)
	}

	onlySkipped := Search(context.Background(), []SearchSource{{Manifest: manifestFixture("photos", "Photos"), SkipReason: "not selected"}}, trawlkit.Query{Text: "one"}, federationv1.SearchOrder_SEARCH_ORDER_RECENCY, 20)
	if onlySkipped.Outcome != federationv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(onlySkipped.Failures) != 0 {
		t.Fatalf("only skipped = %#v", onlySkipped)
	}

	deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	timedOut := Search(deadline, []SearchSource{{
		Manifest: manifestFixture("calendar", "Calendar"),
		Run: func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			return trawlkit.SearchResult{}, &federationv1.SourceFailure{Message: "late"}
		},
	}}, trawlkit.Query{Text: "one"}, federationv1.SearchOrder_SEARCH_ORDER_RECENCY, 20)
	if timedOut.Failures[0].Code != federationv1.FailureCode_FAILURE_CODE_TIMEOUT {
		t.Fatalf("timeout = %#v", timedOut.Failures)
	}
}

func TestSearchRejectsForeignHitSourceAndConflictingLimit(t *testing.T) {
	if _, err := ProjectSearch(manifestFixture("notes", "Notes"), trawlkit.SearchResult{Results: []trawlkit.Hit{{Source: "gmail", Ref: "gmail:one"}}}); err == nil {
		t.Fatal("foreign source was accepted")
	}
	for _, test := range []struct {
		name string
		hit  trawlkit.Hit
	}{
		{name: "foreign open ref", hit: federationSearchHit("gmail:message/example-1", "Synthetic message", "matching text", time.Time{})},
		{name: "padded open ref", hit: federationSearchHit(" notes:note/example-1 ", "Synthetic note", "matching text", time.Time{})},
		{name: "invalid anchor", hit: func() trawlkit.Hit {
			hit := federationSearchHit("notes:note/example-1", "Synthetic note", "matching text", time.Time{})
			hit.AnchorID = "matching passage"
			return hit
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ProjectSearch(manifestFixture("notes", "Notes"), trawlkit.SearchResult{Results: []trawlkit.Hit{test.hit}}); err == nil {
				t.Fatalf("invalid hit was accepted: %#v", test.hit)
			}
		})
	}
	called := false
	response := Search(context.Background(), []SearchSource{{
		Manifest: manifestFixture("notes", "Notes"),
		Run: func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			called = true
			return trawlkit.SearchResult{}, nil
		},
	}}, trawlkit.Query{Text: "one", Limit: 10}, federationv1.SearchOrder_SEARCH_ORDER_RECENCY, 20)
	if called || response.Outcome != federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED || response.Failures[0].Code != federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT {
		t.Fatalf("conflicting limit response = %#v, called=%t", response, called)
	}
}

func TestSearchPreservesInputOrderWhenCallbacksFinishOutOfOrder(t *testing.T) {
	secondFinished := make(chan struct{})
	response := Search(context.Background(), []SearchSource{
		{Manifest: manifestFixture("one", "One"), Run: func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			<-secondFinished
			return trawlkit.SearchResult{Results: []trawlkit.Hit{federationSearchHit("one:hit", "One hit", "hit", time.Time{})}, TotalMatches: 1}, nil
		}},
		{Manifest: manifestFixture("two", "Two"), Run: func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			close(secondFinished)
			return trawlkit.SearchResult{Results: []trawlkit.Hit{federationSearchHit("two:hit", "Two hit", "hit", time.Time{})}, TotalMatches: 1}, nil
		}},
	}, trawlkit.Query{Text: "hit"}, federationv1.SearchOrder_SEARCH_ORDER_RELEVANCE, 20)
	if response.Sources[0].SourceId != "one" || response.Sources[1].SourceId != "two" || response.Hits[0].OpenRef != "one:hit" || response.Hits[1].OpenRef != "two:hit" {
		t.Fatalf("response order = %#v", response)
	}
}

func TestSearchMapsPanicAndCancellation(t *testing.T) {
	panicResult := Search(context.Background(), []SearchSource{{
		Manifest: manifestFixture("notes", "Notes"),
		Run: func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			panic("synthetic panic")
		},
	}}, trawlkit.Query{Text: "one"}, federationv1.SearchOrder_SEARCH_ORDER_RECENCY, 20)
	if panicResult.Failures[0].Code != federationv1.FailureCode_FAILURE_CODE_INTERNAL {
		t.Fatalf("panic = %#v", panicResult.Failures)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cancelResult := Search(cancelled, []SearchSource{{
		Manifest: manifestFixture("notes", "Notes"),
		Run: func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			return trawlkit.SearchResult{}, &federationv1.SourceFailure{Message: "stopped"}
		},
	}}, trawlkit.Query{Text: "one"}, federationv1.SearchOrder_SEARCH_ORDER_RECENCY, 20)
	if cancelResult.Failures[0].Code != federationv1.FailureCode_FAILURE_CODE_CANCELLED {
		t.Fatalf("cancel = %#v", cancelResult.Failures)
	}
}

func TestSearchBoundaryEvidence(t *testing.T) {
	availability := int64(1)
	unread := true
	one := manifestFixture("gmail", "Gmail")
	two := manifestFixture("notes", "Notes")
	oneResult := trawlkit.SearchResult{
		WhoResolved: &trawlkit.WhoResolved{Who: "Avery Example", Identifiers: []string{"avery@example.com"}},
		Results: []trawlkit.Hit{
			{Ref: "gmail:message:example-1", ShortRef: "mail:1", Time: mustTime("2026-07-12T10:00:00+02:00"), AnchorID: trawlkit.MatchAnchorID, Summary: trawlkit.ResultSummary{Title: "Canal room", Subtitle: "Avery Example"}, Evidence: []trawlkit.EvidenceFragment{trawlkit.TextMatch("Message body", "Synthetic launch note"), trawlkit.FieldMatch("Mailbox", "mailbox", "Work")}, AllDay: true, Availability: &availability, Unread: &unread},
			federationSearchHit("gmail:message:example-2", "Casey Example", "Synthetic follow-up", mustTime("2026-07-12T08:00:00Z")),
		},
		TotalMatches: 3,
		Truncated:    true,
	}
	twoResult := trawlkit.SearchResult{Results: []trawlkit.Hit{}, TotalMatches: 0}
	query := trawlkit.Query{Text: "launch", Limit: 3, Who: "Avery Example"}
	input := struct {
		Query   trawlkit.Query        `json:"query"`
		Gmail   trawlkit.SearchResult `json:"gmail"`
		Notes   trawlkit.SearchResult `json:"notes"`
		Failed  string                `json:"failed"`
		Skipped string                `json:"skipped"`
	}{query, oneResult, twoResult, "calendar timeout", "photos not selected"}
	inputBytes, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	inputBytes = append(inputBytes, '\n')
	oneProjected, err := ProjectSearch(one, oneResult)
	if err != nil {
		t.Fatal(err)
	}
	twoProjected, err := ProjectSearch(two, twoResult)
	if err != nil {
		t.Fatal(err)
	}
	projected := &federationv1.SearchResponse{Order: federationv1.SearchOrder_SEARCH_ORDER_RECENCY, Sources: []*federationv1.SearchSourceResult{oneProjected, twoProjected}, ResultLimit: 3}
	response := Search(context.Background(), []SearchSource{
		{Manifest: one, Run: func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			return oneResult, nil
		}},
		{Manifest: two, Run: func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			return twoResult, nil
		}},
		{Manifest: manifestFixture("calendar", "Calendar"), Run: func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure) {
			return trawlkit.SearchResult{}, &federationv1.SourceFailure{Code: federationv1.FailureCode_FAILURE_CODE_TIMEOUT, Message: "Calendar search timed out."}
		}},
		{Manifest: manifestFixture("photos", "Photos"), SkipReason: "not selected"},
	}, query, federationv1.SearchOrder_SEARCH_ORDER_RECENCY, 3)
	writeEvidence(t, "search-input.json", inputBytes)
	writeEvidence(t, "search-projected.pbtxt", []byte(prototext.Format(projected)))
	writeEvidence(t, "search-response.pbtxt", []byte(prototext.Format(response)))
}

func federationSearchHit(ref, title, matchingText string, at time.Time) trawlkit.Hit {
	return trawlkit.Hit{
		Ref:      ref,
		Time:     at,
		AnchorID: trawlkit.MatchAnchorID,
		Summary:  trawlkit.ResultSummary{Title: title},
		Archive:  []trawlkit.ArchiveContext{{Kind: "notes", Label: "In Notes"}},
		Evidence: []trawlkit.EvidenceFragment{trawlkit.TextMatch("Matching text", matchingText)},
	}
}

func mustTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func hitRefs(hits []*federationv1.SearchHit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.OpenRef)
	}
	return out
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
