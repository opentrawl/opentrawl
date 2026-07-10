package cli

import (
	"bufio"
	"bytes"
	"io"
	"testing"

	appv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/app/v1"
	"github.com/opentrawl/opentrawl/trawlkit/prototransport"
)

func readAppStatuses(t *testing.T, data string) []*appv1.SourceStatus {
	t.Helper()
	reader := bufio.NewReader(bytes.NewBufferString(data))
	var statuses []*appv1.SourceStatus
	for {
		status := new(appv1.SourceStatus)
		err := prototransport.ReadDelimited(reader, status)
		if err == io.EOF {
			return statuses
		}
		if err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, status)
	}
}

func readAppHits(t *testing.T, data string) []*appv1.SearchHit {
	t.Helper()
	reader := bufio.NewReader(bytes.NewBufferString(data))
	var hits []*appv1.SearchHit
	for {
		hit := new(appv1.SearchHit)
		err := prototransport.ReadDelimited(reader, hit)
		if err == io.EOF {
			return hits
		}
		if err != nil {
			t.Fatal(err)
		}
		hits = append(hits, hit)
	}
}

func TestAppStatusWritesFramedProtobuf(t *testing.T) {
	writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"iMessage"}`,
		status:   statusJSON("imessage", "ok"),
	})
	stdout, _, code := runCLI(t, "__app", "status")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	statuses := readAppStatuses(t, stdout)
	if len(statuses) != 1 {
		t.Fatalf("statuses = %d, want 1", len(statuses))
	}
	status := statuses[0]
	if status.GetAppId() != "imessage" || status.GetSurface() != "iMessage" {
		t.Fatalf("status = %+v", status)
	}
	if len(status.GetCounts()) != 1 || status.GetCounts()[0].GetDisplay() != "12,345 messages" {
		t.Fatalf("counts = %+v", status.GetCounts())
	}
	if status.GetArchiveBytes() <= 0 {
		t.Fatalf("archive bytes = %d, want positive synthetic archive size", status.GetArchiveBytes())
	}
}

func TestAppSearchKeepsHitsOnPartialFailure(t *testing.T) {
	writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"iMessage"}`,
			search:   `{"query":"example","results":[{"ref":"imessage:msg/example","time":"2026-07-09T12:00:00Z","who":"Example Person","where":"Example chat","snippet":"Synthetic result"}],"total_matches":1,"truncated":false}`,
		},
		fakeCrawler{
			name:     "calcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"calendar","display_name":"Calendar"}`,
			search:   `not-json`, searchExit: 1,
		},
	)
	stdout, _, code := runCLI(t, "__app", "search", "example")
	if code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	hits := readAppHits(t, stdout)
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	if hits[0].GetOpenRef() != "imessage:msg/example" || hits[0].GetSnippet() != "Synthetic result" {
		t.Fatalf("hit = %+v", hits[0])
	}
}

func TestAppSearchScopeQueriesOneSource(t *testing.T) {
	writeFakeCrawlers(t,
		fakeCrawler{
			name: "imsgcrawl", metadata: metadataJSON("imessage"),
			search: searchResultsJSON("example", 1),
		},
		fakeCrawler{
			name: "calcrawl", metadata: metadataJSON("calendar"),
			search: searchResultsJSON("example", 1),
		},
	)
	stdout, _, code := runCLI(t, "__app", "search", "--source", "calendar", "example")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	hits := readAppHits(t, stdout)
	if len(hits) != 1 || hits[0].GetAppId() != "calendar" {
		t.Fatalf("hits = %+v, want one calendar hit", hits)
	}
}

func TestAppSyncReportsMixedOutcomeInExitCode(t *testing.T) {
	writeFakeCrawlers(t,
		fakeCrawler{name: "sourcea", metadata: metadataJSON("sourcea"), sync: `{"state":"ok"}`},
		fakeCrawler{name: "sourceb", metadata: metadataJSON("sourceb"), sync: `not-json`},
	)
	stdout, _, code := runCLI(t, "__app", "sync")
	if code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want no frames", stdout)
	}
}
