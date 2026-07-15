package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func searchResultsJSON(query string, count int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"query":%q,"results":[`, query)
	for i := 1; i <= count; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"ref":"imessage:msg/%d","time":"2026-05-14T09:12:00Z","who":"Alice","where":"Family","snippet":"match %d"}`, i, i)
	}
	fmt.Fprintf(&b, `],"total_matches":%d,"truncated":false}`, count)
	return b.String()
}

func childStderrLines(source string, count int) string {
	var b strings.Builder
	for i := 1; i <= count; i++ {
		fmt.Fprintf(&b, "%s child-line %03d\n", source, i)
	}
	return b.String()
}

func testSearchRow(t *testing.T, source, ref, timestamp string, sourceRank int) SearchRow {
	t.Helper()
	parsed, ok := parseSearchTime(timestamp)
	if timestamp != "" && !ok {
		t.Fatalf("bad test timestamp %q", timestamp)
	}
	return SearchRow{
		Source:     source,
		Ref:        ref,
		Time:       timestamp,
		sourceRank: sourceRank,
		parsedTime: parsed,
		timeOK:     ok,
	}
}

func searchRefs(rows []SearchRow) []string {
	refs := make([]string, 0, len(rows))
	for _, row := range rows {
		refs = append(refs, row.Ref)
	}
	return refs
}

func TestRankTierSortInterleavesBySourceRank(t *testing.T) {
	rows := []SearchRow{
		testSearchRow(t, "alpha", "alpha:rank-1", "2026-07-01T09:00:00Z", 1),
		testSearchRow(t, "charlie", "charlie:rank-0", "2026-05-01T09:00:00Z", 0),
		testSearchRow(t, "delta", "delta:b", "2026-01-01T09:00:00Z", 2),
		testSearchRow(t, "beta", "beta:rank-1", "2026-06-01T09:00:00Z", 1),
		testSearchRow(t, "alpha", "alpha:rank-0", "2026-01-01T09:00:00Z", 0),
		testSearchRow(t, "delta", "delta:a", "2026-01-01T09:00:00Z", 2),
		testSearchRow(t, "beta", "beta:rank-0", "2026-05-01T09:00:00Z", 0),
	}

	rankTierSort(rows)

	want := []string{
		"beta:rank-0",
		"charlie:rank-0",
		"alpha:rank-0",
		"alpha:rank-1",
		"beta:rank-1",
		"delta:a",
		"delta:b",
	}
	if got := strings.Join(searchRefs(rows), ","); got != strings.Join(want, ",") {
		t.Fatalf("refs = %v, want %v", searchRefs(rows), want)
	}
}

func TestMergedSearchRowsInvoiceSortMode(t *testing.T) {
	results := []searchSourceResult{
		{
			Total: 1,
			Rows: []SearchRow{
				testSearchRow(t, "imessage", "imessage:strong-old-invoice", "2023-04-01T09:00:00Z", 0),
			},
		},
		{
			Total: 2,
			Rows: []SearchRow{
				testSearchRow(t, "gmail", "gmail:recent-rank-0", "2026-06-02T09:00:00Z", 0),
				testSearchRow(t, "gmail", "gmail:weak-recent-footer", "2026-06-01T09:00:00Z", 1),
			},
		},
	}

	relevance := mergedSearchRows(results, 10, searchSortRelevance)
	if got := strings.Join(searchRefs(relevance.Rows), ","); got != "gmail:recent-rank-0,imessage:strong-old-invoice,gmail:weak-recent-footer" {
		t.Fatalf("relevance refs = %v", searchRefs(relevance.Rows))
	}
	if relevance.TotalMatches != 3 || relevance.Truncated || relevance.More != 0 {
		t.Fatalf("relevance counters = total %d truncated %t more %d", relevance.TotalMatches, relevance.Truncated, relevance.More)
	}

	recency := mergedSearchRows(results, 10, searchSortRecency)
	if got := strings.Join(searchRefs(recency.Rows), ","); got != "gmail:recent-rank-0,gmail:weak-recent-footer,imessage:strong-old-invoice" {
		t.Fatalf("recency refs = %v", searchRefs(recency.Rows))
	}
}

func TestSearchSortModeSelection(t *testing.T) {
	tests := []struct {
		name  string
		query string
		sort  string
		want  searchSortMode
	}{
		{name: "query defaults recency", query: "invoice", want: searchSortRecency},
		{name: "empty query defaults recency", want: searchSortRecency},
		{name: "empty query ignores relevance override", sort: "relevance", want: searchSortRecency},
		{name: "query allows relevance override", query: "invoice", sort: "relevance", want: searchSortRelevance},
		{name: "query allows recency override", query: "invoice", sort: "recency", want: searchSortRecency},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveSearchSort(tt.query, tt.sort)
			if err != nil {
				t.Fatalf("resolveSearchSort returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("mode = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSearchHeadingNamesSortMode(t *testing.T) {
	if got := searchHeading("invoice", "", 2, 5, searchSortRelevance); got != `Search "invoice": showing 2 of 5, best matches first.` {
		t.Fatalf("relevance heading = %q", got)
	}
	if got := searchHeading("", "Alex Example", 2, 5, searchSortRecency); got != `Search with Alex Example: showing 2 of 5, newest first.` {
		t.Fatalf("recency heading = %q", got)
	}
}

func TestSearchQuerylessRecencyGolden(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:          "imsgcrawl",
			metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
			searchNoQuery: true,
			search: `{"query":"","results":[
				{"ref":"imessage:msg/old","time":"2026-05-11T08:00:00Z","who":"Alice","where":"Invoices","snippet":"older filter match"}
			],"total_matches":5,"truncated":true}`,
		},
		fakeCrawler{
			name:          "telecrawl",
			metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
			searchNoQuery: true,
			search: `{"query":"","results":[
				{"ref":"telegram:msg/new","time":"2026-05-14T09:00:00Z","who":"Bob","where":"Ops","snippet":"newer filter match"},
				{"ref":"telegram:msg/mid","time":"2026-05-12T10:00:00Z","who":"Cara","where":"Ops","snippet":"middle filter match"}
			],"total_matches":2,"truncated":false}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))
	t.Setenv("COLUMNS", "200")

	stdout, stderr, code := runCLI(t, "search", "--source", "imessage,telegram", "--after", "2026-01-01", "--limit", "2")
	if code != 0 {
		t.Fatalf("search code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	want := strings.Join([]string{
		"Search filters: showing 2 of 7, newest first.",
		"Open: trawl open REF",
		"More: trawl search --source imessage,telegram --after 2026-01-01 --limit 4",
		"",
		fmt.Sprintf("%-16s  %-8s  %-16s  %s", "date", "source", "ref", "text"),
		fmt.Sprintf("%-16s  %-8s  %-16s  %s", shortLocalTestTime(t, "2026-05-14T09:00:00Z"), "Telegram", "telegram:msg/new", "Ops — Bob · Received · Message from Bob: newer filter match"),
		fmt.Sprintf("%-16s  %-8s  %-16s  %s", shortLocalTestTime(t, "2026-05-12T10:00:00Z"), "Telegram", "telegram:msg/mid", "Ops — Cara · Received · Message from Cara: middle filter match"),
	}, "\n") + "\n"
	if stdout != want {
		t.Fatalf("stdout = %q\nwant   = %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchQuerylessRecencyJSONGolden(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:          "imsgcrawl",
			metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
			searchNoQuery: true,
			search: `{"query":"","results":[
				{"ref":"imessage:msg/old","time":"2026-05-11T08:00:00Z","who":"Alice","where":"Invoices","snippet":"older filter match"}
			],"total_matches":5,"truncated":true}`,
		},
		fakeCrawler{
			name:          "telecrawl",
			metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
			searchNoQuery: true,
			search: `{"query":"","results":[
				{"ref":"telegram:msg/new","time":"2026-05-14T09:00:00Z","who":"Bob","where":"Ops","snippet":"newer filter match"},
				{"ref":"telegram:msg/mid","time":"2026-05-12T10:00:00Z","who":"Cara","where":"Ops","snippet":"middle filter match"}
			],"total_matches":2,"truncated":false}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "search", "--source", "imessage,telegram", "--after", "2026-01-01", "--limit", "2")
	if code != 0 {
		t.Fatalf("search --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	response := decodeCanonicalSearchResponse(t, stdout)
	if len(response.GetSources()) != 2 || len(response.GetHits()) != 2 || response.GetHits()[0].GetOpenRef() != "telegram:msg/new" || !response.GetTruncated() {
		t.Fatalf("search response = %#v", response)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchQuerylessRelevanceSortRendersLikePlainQueryless(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:          "imsgcrawl",
		metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		searchNoQuery: true,
		search: `{"query":"","results":[
			{"ref":"imessage:msg/new","time":"2026-05-14T09:00:00Z","who":"Alice","where":"Invoices","snippet":"newer filter match"},
			{"ref":"imessage:msg/old","time":"2026-05-11T08:00:00Z","who":"Bob","where":"Invoices","snippet":"older filter match"}
		],"total_matches":2,"truncated":false}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))
	t.Setenv("COLUMNS", "200")

	args := []string{"search", "--source", "imessage", "--after", "2026-01-01", "--limit", "1"}
	plainStdout, plainStderr, plainCode := runCLI(t, args...)
	if plainCode != 0 {
		t.Fatalf("plain search code = %d stderr=%s stdout=%s", plainCode, plainStderr, plainStdout)
	}
	sortArgs := append(append([]string{}, args...), "--sort", "relevance")
	sortStdout, sortStderr, sortCode := runCLI(t, sortArgs...)
	if sortCode != 0 {
		t.Fatalf("search --sort=relevance code = %d stderr=%s stdout=%s", sortCode, sortStderr, sortStdout)
	}
	if sortStdout != plainStdout {
		t.Fatalf("search --sort=relevance stdout = %q\nplain stdout = %q", sortStdout, plainStdout)
	}
	for _, want := range []string{
		"Search filters: showing 1 of 2, newest first.",
		"More: trawl search --source imessage --after 2026-01-01 --limit 2",
	} {
		if !strings.Contains(sortStdout, want) {
			t.Fatalf("search --sort=relevance stdout missing %q:\n%s", want, sortStdout)
		}
	}
	if strings.Contains(sortStdout, "--sort") {
		t.Fatalf("queryless More line must not echo inert --sort:\n%s", sortStdout)
	}
	if plainStderr != "" {
		t.Fatalf("plain stderr = %s", plainStderr)
	}
	if sortStderr != "" {
		t.Fatalf("search --sort=relevance stderr = %s", sortStderr)
	}
}

func TestSearchMergesSortsAndTruncates(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
			search: `{"query":"boat trip","results":[
				{"ref":"imessage:msg/8842","time":"2026-05-14T09:12:00Z","who":"Alice","where":"Family","snippet":"…the boat trip is on Saturday…"},
				{"ref":"imessage:msg/8841","time":"not-a-time","who":"","where":"","snippet":"unparsed time stays visible"}
			],"total_matches":3,"truncated":true}`,
		},
		fakeCrawler{
			name:     "telecrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
			search: `{"query":"boat trip","results":[
				{"ref":"telegram:msg/1930","time":"2026-05-12T10:00:00Z","who":"Bob","snippet":"…book the boat before June…"}
			],"total_matches":1,"truncated":false}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))
	t.Setenv("COLUMNS", "200")

	stdout, stderr, code := runCLI(t, "search", "boat trip", "--source", "imessage,telegram", "--limit", "2")
	if code != 0 {
		t.Fatalf("search code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	for _, want := range []string{
		`Search "boat trip": showing 2 of 4, newest first.`,
		"Open: trawl open REF",
		`More: trawl search "boat trip" --source imessage,telegram --limit 4`,
		"date              source    ref                text",
		shortLocalTestTime(t, "2026-05-14T09:12:00Z") + "  Messages  imessage:msg/8842  Family — Alice · Received · Message from Alice: …the boat trip is on Saturday…",
		shortLocalTestTime(t, "2026-05-12T10:00:00Z") + "  Telegram  telegram:msg/1930  Conversation — Bob · Received · Message from Bob: …book the boat before June…",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

// TestSearchAllDayRowsRenderDateOnly is the TRAWL-104 tripwire: a
// source that marks a result all_day gets a bare date in the federated
// table, never a fake midnight, and the federated JSON carries the bit.
func TestSearchAllDayRowsRenderDateOnly(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "calcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"calendar","display_name":"Calendar"}`,
			search: `{"query":"fair","results":[
				{"ref":"calendar:event/aaa","time":"2026-03-27T00:00:00+01:00","all_day":true,"who":"me","where":"Privé","calendar":"Personal","snippet":"Art fair"},
				{"ref":"calendar:event/bbb","time":"2026-03-26T20:00:00+01:00","all_day":false,"who":"me","where":"Josh","calendar":"Work","snippet":"fair prep call"}
			],"total_matches":2,"truncated":false}`,
		},
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
			search: `{"query":"fair","results":[
				{"ref":"imessage:msg/1","time":"2026-03-25T09:12:00+01:00","who":"Alice","where":"Family","snippet":"see you at the fair"}
			],"total_matches":1,"truncated":false}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))
	t.Setenv("COLUMNS", "200")

	stdout, stderr, code := runCLI(t, "search", "fair")
	if code != 0 {
		t.Fatalf("search code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "2026-03-27  ") || strings.Contains(stdout, "2026-03-27 00:00") {
		t.Fatalf("all-day row must show a bare date, never 00:00:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Calendar") || !strings.Contains(stdout, "Personal") || !strings.Contains(stdout, "Work") {
		t.Fatalf("calendar field missing from human search rows:\n%s", stdout)
	}
	for _, want := range []string{
		shortLocalTestTime(t, "2026-03-26T20:00:00+01:00"),
		shortLocalTestTime(t, "2026-03-25T09:12:00+01:00"),
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("timed row missing %q:\n%s", want, stdout)
		}
	}

	stdout, stderr, code = runCLI(t, "--json", "search", "fair")
	if code != 0 {
		t.Fatalf("search --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	response := decodeCanonicalSearchResponse(t, stdout)
	byRef := map[string]bool{}
	calendarByRef := map[string]string{}
	for _, row := range response.GetHits() {
		byRef[row.GetOpenRef()] = row.GetAllDay()
		calendarByRef[row.GetOpenRef()] = row.GetSummary().GetSubtitle()
	}
	if !byRef["calendar:event/aaa"] || byRef["calendar:event/bbb"] || byRef["imessage:msg/1"] {
		t.Fatalf("federated all_day bits wrong: %#v\n%s", byRef, stdout)
	}
	if calendarByRef["calendar:event/aaa"] != "Personal" || calendarByRef["calendar:event/bbb"] != "Work" || calendarByRef["imessage:msg/1"] != "Alice" {
		t.Fatalf("federated calendar fields wrong: %#v\n%s", calendarByRef, stdout)
	}
	if strings.Count(stdout, "all_day") != 6 {
		t.Fatalf("canonical JSON must retain all default all_day fields:\n%s", stdout)
	}
}

func TestSearchJSONHonorsLimitAboveOldCap(t *testing.T) {
	limit := 205
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:        "imsgcrawl",
		metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		searchLimit: strconv.Itoa(limit),
		search:      searchResultsJSON("boat trip", limit),
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip", "--limit", strconv.Itoa(limit))
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	response := decodeCanonicalSearchResponse(t, stdout)
	if got := len(response.GetHits()); got != limit {
		t.Fatalf("results = %d, want %d", got, limit)
	}
	if len(response.GetSources()) != 1 || response.GetSources()[0].GetTotalMatches() != uint64(limit) {
		t.Fatalf("sources = %#v", response.GetSources())
	}
	if response.GetTruncated() {
		t.Fatalf("truncated = true, want false")
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchHumanHonorsLimitAboveOldCap(t *testing.T) {
	limit := 205
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:        "imsgcrawl",
		metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		searchLimit: strconv.Itoa(limit),
		search:      searchResultsJSON("boat trip", limit),
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))
	t.Setenv("COLUMNS", "200")

	stdout, stderr, code := runCLI(t, "search", "boat trip", "--limit", strconv.Itoa(limit))
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if got := strings.Count(stdout, "imessage:msg/"); got != limit {
		t.Fatalf("rendered rows = %d, want %d\n%s", got, limit, stdout)
	}
	if strings.Contains(stdout, "More: ") {
		t.Fatalf("stdout offered more rows when none are hidden:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchRejectsNonPositiveLimit(t *testing.T) {
	for _, args := range [][]string{
		{"search", "boat trip", "--limit", "0"},
		{"search", "boat trip", "--limit=-1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Setenv("PATH", writeFakeCrawlers(t))
			t.Setenv("HOME", syntheticHome(t))

			var stdout, stderr strings.Builder
			err := Execute(args, &stdout, &stderr)
			if code := ExitCode(err); code != 2 {
				t.Fatalf("code = %d, want 2 stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			var usage usageErr
			if !errors.As(err, &usage) {
				t.Fatalf("err = %T %[1]v, want usageErr", err)
			}
			if !strings.Contains(err.Error(), "search --limit must be at least 1") {
				t.Fatalf("missing limit error: %v", err)
			}
		})
	}
}

func TestSearchRejectsInvalidSort(t *testing.T) {
	t.Setenv("PATH", writeFakeCrawlers(t))
	t.Setenv("HOME", syntheticHome(t))

	var stdout, stderr strings.Builder
	err := Execute([]string{"search", "boat trip", "--sort", "freshest"}, &stdout, &stderr)
	if code := ExitCode(err); code != 2 {
		t.Fatalf("code = %d, want 2 stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var usage usageErr
	if !errors.As(err, &usage) {
		t.Fatalf("err = %T %[1]v, want usageErr", err)
	}
	if !strings.Contains(err.Error(), "search --sort must be relevance or recency") {
		t.Fatalf("missing sort error: %v", err)
	}
}

func TestSearchHumanOutputUsesFullRefsWhenEveryDisplayedRowHasShortRef(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"imessage","display_name":"Messages"}`,
			search: `{"query":"boat trip","results":[
				{"ref":"imessage:msg/8842","short_ref":"t7k3f","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}
			],"total_matches":1,"truncated":false}`,
		},
		fakeCrawler{
			name:     "telecrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"telegram","display_name":"Telegram"}`,
			search: `{"query":"boat trip","results":[
				{"ref":"telegram:msg/1930","alias":"q8n4c","time":"2026-05-12T10:00:00Z","who":"Bob","snippet":"Older match"}
			],"total_matches":1,"truncated":false}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "search", "boat trip")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{"imessage:msg/8842", "telegram:msg/1930"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing full ref %q:\n%s", want, stdout)
		}
	}
	for _, unwanted := range []string{"t7k3f", "q8n4c"} {
		if strings.Contains(stdout, unwanted) {
			t.Fatalf("stdout leaked short ref %q:\n%s", unwanted, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchHumanOutputUsesFullRefsPerRow(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"imessage","display_name":"Messages"}`,
			search: `{"query":"boat trip","results":[
				{"ref":"imessage:msg/8842","short_ref":"t7k3f","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}
			],"total_matches":1,"truncated":false}`,
		},
		fakeCrawler{
			name:     "telecrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
			search: `{"query":"boat trip","results":[
				{"ref":"telegram:msg/1930","time":"2026-05-12T10:00:00Z","who":"Bob","snippet":"Older match"}
			],"total_matches":1,"truncated":false}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "search", "boat trip")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "imessage:msg/8842") {
		t.Fatalf("stdout missing full ref for the capable source:\n%s", stdout)
	}
	if !strings.Contains(stdout, "telegram:msg/1930") {
		t.Fatalf("stdout missing full ref for the source without short refs:\n%s", stdout)
	}
	if strings.Contains(stdout, "t7k3f") {
		t.Fatalf("stdout leaked a short ref:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

// TestSearchJSONPreservesCanonicalShortRef keeps JSON lossless while the
// human renderer chooses the full ref for a stable copy-paste command.
func TestSearchJSONPreservesCanonicalShortRef(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"imessage","display_name":"Messages"}`,
		search: `{"query":"boat trip","results":[
			{"ref":"imessage:msg/8842","short_ref":"t7k3f","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}
		],"total_matches":1,"truncated":false}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var response federationv1.SearchResponse
	if err := (protojson.UnmarshalOptions{}).Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("search JSON = %s err=%v", stdout, err)
	}
	if len(response.GetHits()) != 1 || response.GetHits()[0].GetOpenRef() != "imessage:msg/8842" || response.GetHits()[0].GetShortRef() != "t7k3f" {
		t.Fatalf("canonical hits = %#v", response.GetHits())
	}
	for _, want := range []string{`"sources":`, `"hits":`, `"failures":[]`, `"skipped_sources":[]`, `"short_ref":"t7k3f"`} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("canonical JSON omitted %s: %s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchJSONIncludesFederatedEnvelope(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:        "imsgcrawl",
		metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		searchLimit: "1",
		search: `{"query":"boat trip","results":[
			{"ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","where":"Family","snippet":"Example match"},
			{"ref":"imessage:msg/2","time":"2026-05-13T09:12:00Z","who":"Bob","where":"Family","snippet":"Older match"}
		],"total_matches":2,"truncated":false}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip", "--limit", "1")
	if code != 0 {
		t.Fatalf("search --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	response := decodeCanonicalSearchResponse(t, stdout)
	if len(response.GetSources()) != 1 || response.GetSources()[0].GetTotalMatches() != 2 || len(response.GetHits()) != 1 || !response.GetTruncated() {
		t.Fatalf("search response = %#v", response)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchJSONIncludesSourceTruncation(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		search: `{"query":"boat trip","results":[
			{"ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","where":"Family","snippet":"Example match"}
		],"total_matches":5,"truncated":true}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip")
	if code != 0 {
		t.Fatalf("search --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	response := decodeCanonicalSearchResponse(t, stdout)
	if len(response.GetSources()) != 1 || response.GetSources()[0].GetTotalMatches() != 5 || len(response.GetHits()) != 1 || !response.GetTruncated() {
		t.Fatalf("search response = %#v", response)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchVerboseLogsSourceOutcomeAndPropagates(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "gogcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","verbose_logs"],"id":"gmail","display_name":"Gmail","paths":{"default_logs":"~/.opentrawl/gmail/logs"}}`,
		search: `{"query":"boat trip","results":[
			{"ref":"gmail:msg/m1","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}
		],"total_matches":1,"truncated":false}`,
	})
	home := syntheticHome(t)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", home)

	stdout, stderr, code := runCLI(t, "-vv", "search", "boat trip", "--source", "gmail", "--limit", "1")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{" search start: version=dev", " search finish: outcome=success"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	logPath := filepath.Join(home, ".opentrawl", "trawl", "logs", "trawl.log")
	logTextBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logTextBytes)
	if !strings.Contains(logText, "search finish: outcome=success") {
		t.Fatalf("log missing source outcome:\n%s", logText)
	}
}

func TestSearchVerboseStreamsSourceLogLines(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:         "gogcrawl",
		metadata:     `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","verbose_logs"],"id":"gmail","display_name":"Gmail"}`,
		search:       `{"query":"boat trip","results":[],"total_matches":0,"truncated":false}`,
		searchStderr: "first child line\nsecond child line\nfinal child line",
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "-v", "search", "boat trip", "--source", "gmail", "--limit", "1")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"fake_stderr: first child line",
		"fake_stderr: second child line",
		"fake_stderr: final child line",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestSearchDoesNotForwardChildStderrWithoutVerbose(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:         "gogcrawl",
		metadata:     `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","verbose_logs"],"id":"gmail","display_name":"Gmail"}`,
		search:       `{"query":"boat trip","results":[],"total_matches":0,"truncated":false}`,
		searchStderr: "hidden child line\n",
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "search", "boat trip", "--source", "gmail", "--limit", "1")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchVerboseConcurrentSourceLogLinesStayWhole(t *testing.T) {
	const lineCount = 100
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:         "imsgcrawl",
			metadata:     `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","verbose_logs"],"id":"imessage","display_name":"Messages"}`,
			search:       `{"query":"boat trip","results":[],"total_matches":0,"truncated":false}`,
			searchStderr: childStderrLines("imessage", lineCount),
		},
		fakeCrawler{
			name:         "telecrawl",
			metadata:     `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","verbose_logs"],"id":"telegram","display_name":"Telegram"}`,
			search:       `{"query":"boat trip","results":[],"total_matches":0,"truncated":false}`,
			searchStderr: childStderrLines("telegram", lineCount),
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "-v", "search", "boat trip", "--source", "imessage,telegram", "--limit", "1")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	seen := map[string]int{}
	for _, line := range strings.Split(stderr, "\n") {
		if !strings.Contains(line, "child-line") {
			continue
		}
		switch {
		case strings.Contains(line, "fake_stderr: imessage child-line "):
			seen["imessage"]++
		case strings.Contains(line, "fake_stderr: telegram child-line "):
			seen["telegram"]++
		default:
			t.Fatalf("source log line was sheared or unattributed: %q\n%s", line, stderr)
		}
		if strings.Contains(line, "imessage child-line") && strings.Contains(line, "telegram child-line") {
			t.Fatalf("source log line contains multiple sources: %q\n%s", line, stderr)
		}
	}
	for _, source := range []string{"imessage", "telegram"} {
		if seen[source] != lineCount {
			t.Fatalf("%s source log lines = %d, want %d\n%s", source, seen[source], lineCount, stderr)
		}
	}
}

func TestSearchJSONEmptyResultsEnvelope(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		search:   `{"query":"boat trip","results":[],"total_matches":0,"truncated":false}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip")
	if code != 0 {
		t.Fatalf("search --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	response := decodeCanonicalSearchResponse(t, stdout)
	if len(response.GetSources()) != 1 || len(response.GetHits()) != 0 || response.GetTruncated() {
		t.Fatalf("search response = %#v", response)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchHelpDocumentsWho(t *testing.T) {
	stdout, stderr, code := runCLI(t, "search", "--help")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"--who=person",
		"Resolve a person or sender, then filter by the exact",
		"match",
		`trawl search invoice --who alex`,
		`trawl search --who "Vendor Support" --after 2026-01-01`,
		"Diagnostics: run with -v, or read ~/.opentrawl/trawl/logs/trawl.log",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchJSONNoCrawlersEnvelope(t *testing.T) {
	binDir := writeFakeCrawlers(t)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip")
	if code != 1 {
		t.Fatalf("search --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	response := decodeCanonicalSearchResponse(t, stdout)
	if response.GetOutcome().String() != "OPERATION_OUTCOME_FAILED" || len(response.GetSources()) != 0 || len(response.GetHits()) != 0 {
		t.Fatalf("no-source response = %#v", response)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchPartialAndTotalFailures(t *testing.T) {
	tests := []struct {
		name       string
		crawlers   []fakeCrawler
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name: "partial failure",
			crawlers: []fakeCrawler{
				{
					name:     "imsgcrawl",
					metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
					search:   `{"query":"boat trip","results":[{"ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}],"total_matches":1}`,
				},
				{
					name:     "telecrawl",
					metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
					search:   `{"query":"boat trip"}`,
				},
			},
			wantCode:   3,
			wantStdout: "Search \"boat trip\": showing 1 of 1, newest first.",
			wantStderr: "Telegram search failed:",
		},
		{
			name: "all failed",
			crawlers: []fakeCrawler{{
				name:     "telecrawl",
				metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
				search:   `not-json`,
			}},
			wantCode:   1,
			wantStderr: "Telegram search failed:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := writeFakeCrawlers(t, tt.crawlers...)
			t.Setenv("PATH", binDir)
			t.Setenv("HOME", syntheticHome(t))

			stdout, stderr, code := runCLI(t, "search", "boat trip")
			if code != tt.wantCode {
				t.Fatalf("code = %d, want %d stdout=%s stderr=%s", code, tt.wantCode, stdout, stderr)
			}
			if tt.wantStdout != "" && !strings.Contains(stdout, tt.wantStdout) {
				t.Fatalf("stdout missing %q:\n%s", tt.wantStdout, stdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr, tt.wantStderr) {
				t.Fatalf("stderr missing %q:\n%s", tt.wantStderr, stderr)
			}
		})
	}
}

func TestSearchJSONIncludesFailedSources(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
			search:   `{"query":"boat trip","results":[{"ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}],"total_matches":1}`,
		},
		fakeCrawler{
			name:     "telecrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
			search:   `not-json`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip")
	if code != 3 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	response := decodeCanonicalSearchResponse(t, stdout)
	if len(response.GetFailures()) != 1 || response.GetFailures()[0].GetSourceId() != "telegram" || len(response.GetHits()) != 1 {
		t.Fatalf("search response = %#v", response)
	}
	if stderr != "" {
		t.Fatalf("JSON search wrote stderr: %s", stderr)
	}
}

// TestSearchTimeoutIsLoudAndDistinctFromError is the regression for
// TRAWL-58: a source that blows the per-source deadline under fan-out
// (the photoscrawl-timed-out-during-federation case) must surface as a
// timeout — never a silent drop, and never conflated with a plain
// crawler error. A fake crawler is held past a short real deadline.
func TestSearchTimeoutIsLoudAndDistinctFromError(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
			search:   `{"query":"grill","results":[{"ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}],"total_matches":1}`,
		},
		fakeCrawler{
			name:        "photoscrawl",
			metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"photos","display_name":"Photos"}`,
			searchSleep: "3",
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLITimeout(t, 100*time.Millisecond, "search", "grill")
	if code != 3 {
		t.Fatalf("partial timeout exit = %d, want 3 stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "Photos search failed: command timed out\n  Remedy: trawl doctor photos") {
		t.Fatalf("stderr missing loud timeout line:\n%s", stderr)
	}
	if !strings.Contains(stdout, "imessage:msg/1") {
		t.Fatalf("surviving source dropped from results:\n%s", stdout)
	}
}

func TestSearchTimeoutJSONCarriesReason(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
			search:   `{"query":"grill","results":[{"ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}],"total_matches":1}`,
		},
		fakeCrawler{
			name:        "photoscrawl",
			metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"photos","display_name":"Photos"}`,
			searchSleep: "3",
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLITimeout(t, 100*time.Millisecond, "--json", "search", "grill")
	if code != 3 {
		t.Fatalf("timeout exit = %d, want 3 stdout=%s stderr=%s", code, stdout, stderr)
	}
	response := decodeCanonicalSearchResponse(t, stdout)
	if len(response.GetFailures()) != 1 || response.GetFailures()[0].GetSourceId() != "photos" || len(response.GetHits()) != 1 {
		t.Fatalf("timeout response = %#v", response)
	}
}

// TestSearchTotalTimeoutExitsOne pins the deterministic exit contract:
// every source failing (here all time out) is exit 1, a partial failure
// is exit 3 — the same failure shape always yields the same code.
func TestSearchTotalTimeoutExitsOne(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:        "photoscrawl",
		metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"photos","display_name":"Photos"}`,
		searchSleep: "3",
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLITimeout(t, 100*time.Millisecond, "search", "grill")
	if code != 1 {
		t.Fatalf("total timeout exit = %d, want 1 stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "Photos search failed: command timed out") {
		t.Fatalf("stderr missing timeout line:\n%s", stderr)
	}
}

func TestSearchUnknownSource(t *testing.T) {
	binDir := writeFakeCrawlers(t)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "search", "boat trip", "--source", "missing")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, `Source "missing" was not found.`) {
		t.Fatalf("stderr missing source error:\n%s", stderr)
	}
}

// TestSearchOmitsAllEmptyColumns is the tripwire from the TRAWL-95
// adversarial review: a column with no values (tweets have no "where")
// must be omitted, never rendered as a strip of dashes.
func TestSearchOmitsAllEmptyColumns(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "othercrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"other","display_name":"Other"}`,
		search: `{"query":"boat","results":[
			{"ref":"other:item/1","short_ref":"hmn42","time":"2026-05-14T09:12:00Z","who":"me","where":"","snippet":"item one"},
			{"ref":"other:item/2","short_ref":"eygc4","time":"2026-05-13T09:12:00Z","who":"Anthony","where":"","snippet":"item two"}
		],"total_matches":2,"truncated":false}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))
	t.Setenv("COLUMNS", "200")

	stdout, stderr, code := runCLI(t, "search", "boat")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "who") || strings.Contains(stdout, "where") {
		t.Fatalf("stdout invented universal who or where columns:\n%s", stdout)
	}
	if !strings.Contains(stdout, "date              source  ref           text") {
		t.Fatalf("stdout missing compacted header:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}
