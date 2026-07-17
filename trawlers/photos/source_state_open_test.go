package photoscrawl

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/encoding/protojson"
)

type staticSnapshotProvider struct {
	snapshot photos.LibrarySnapshot
}

func (p staticSnapshotProvider) Snapshot(context.Context, string) (photos.LibrarySnapshot, error) {
	return p.snapshot, nil
}

func testSnapshotProviderFromEnv() photos.Provider {
	if os.Getenv("PHOTOSCRAWL_TEST_SNAPSHOT_COMPLETENESS") != "limited" {
		return nil
	}
	return staticSnapshotProvider{snapshot: photos.LibrarySnapshot{
		Provider: "fixture",
		Completeness: photos.SnapshotCompleteness{
			State:    photos.SnapshotLimited,
			Evidence: map[string]string{"fixture_result": "limited"},
		},
	}}
}

func TestDeletedUpstreamSearchSnippetSurvivesCrawlerBoundary(t *testing.T) {
	t.Parallel()
	input := archive.SearchHit{
		Ref:      "photos:asset/fixture",
		Time:     "2026-07-11T10:00:00Z",
		Snippet:  "Deleted upstream · Synthetic beach card.",
		AnchorID: "description",
		Matches:  []archive.SearchMatch{{Field: "description", Runs: []store.FTS5TextRun{{Text: "Synthetic beach card.", Matched: true}}}},
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=search_renderer input=%s", inputJSON)
	output, err := searchHit(input)
	if err != nil {
		t.Fatal(err)
	}
	outputJSON, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=search_renderer output=%s", outputJSON)
	if output.AnchorID != "description" || len(output.Evidence) != 1 || output.Evidence[0].Field == nil || output.Evidence[0].Field.Name != "description" || len(output.Evidence[0].Field.Value) != 1 || output.Evidence[0].Field.Value[0].Text != "Synthetic beach card." || !output.Evidence[0].Field.Value[0].Matched {
		t.Fatalf("search match = %#v", output)
	}
}

func TestStaleSearchProjectsClearly(t *testing.T) {
	t.Parallel()
	searchInput := archive.SearchHit{
		Ref:      "photos:asset/stale-fixture",
		Time:     "2026-07-10T12:00:00Z",
		Snippet:  "Synthetic beach scene.",
		Stale:    true,
		AnchorID: "summary",
		Matches:  []archive.SearchMatch{{Field: "summary", Runs: []store.FTS5TextRun{{Text: "Synthetic beach scene.", Matched: true}}}},
	}
	searchInputJSON, err := json.Marshal(searchInput)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=search_renderer input=%s", searchInputJSON)
	searchOutput, err := searchHit(searchInput)
	if err != nil {
		t.Fatal(err)
	}
	searchOutputJSON, err := json.Marshal(searchOutput)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=search_renderer output=%s", searchOutputJSON)
	if searchOutput.AnchorID != "summary" || len(searchOutput.Evidence) != 1 || searchOutput.Evidence[0].Field == nil || searchOutput.Evidence[0].Field.Name != "summary" || len(searchOutput.Evidence[0].Field.Value) != 1 || searchOutput.Evidence[0].Field.Value[0].Text != "Synthetic beach scene." || !searchOutput.Evidence[0].Field.Value[0].Matched {
		t.Fatalf("search match = %#v", searchOutput)
	}
}

func TestIncompleteSnapshotBecomesCommandFailure(t *testing.T) {
	t.Parallel()
	input := &archive.SnapshotIncompleteError{State: "limited"}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=sync_command input=%s", inputJSON)
	command, ok := syncCommandError(input).(commandError)
	if !ok {
		t.Fatalf("sync command error = %T, want commandError", syncCommandError(input))
	}
	output, err := json.Marshal(command.ErrorBody())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=sync_command output=%s", output)
	if command.Code != "snapshot_incomplete" || !strings.Contains(command.Message, "audit was recorded") || command.Remedy == "" {
		t.Fatalf("sync command error = %#v", command)
	}
}

func TestIncompleteSnapshotHasNoSuccessfulCrawlerReport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRAWLKIT_STATE_ROOT", "")
	t.Setenv("TRAWLKIT_RUN_ID", "")
	t.Setenv("PHOTOSCRAWL_TEST_SNAPSHOT_COMPLETENESS", "")
	libraryPath := filepath.Join(home, "Pictures", "Photos Library.photoslibrary")
	createSyntheticLibrary(t, libraryPath)
	initialStdout, initialStderr, initialCode := captureRun(t, []string{"sync", "--json"})
	t.Logf("boundary=crawler_initial_sync_json output={\"stdout\":%q,\"stderr\":%q,\"code\":%d}", initialStdout, initialStderr, initialCode)
	if initialCode != 0 || initialStderr != "" {
		t.Fatalf("initial sync code=%d stdout=%q stderr=%q", initialCode, initialStdout, initialStderr)
	}
	archivePath := filepath.Join(home, ".opentrawl", "photos", "photos.db")
	before, err := archive.Status(context.Background(), archive.Paths{Database: archivePath})
	if err != nil {
		t.Fatal(err)
	}
	if before.LastImportAt == "" || before.State != "ok" {
		t.Fatalf("initial crawler status = %#v", before)
	}
	t.Setenv("PHOTOSCRAWL_TEST_SNAPSHOT_COMPLETENESS", "limited")
	provider := testSnapshotProviderFromEnv()
	input, err := json.Marshal(provider.(staticSnapshotProvider).snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=crawler_sync input=%s", input)

	humanStdout, humanStderr, humanCode := captureRun(t, []string{"sync"})
	t.Logf("boundary=crawler_sync_human output={\"stdout\":%q,\"stderr\":%q,\"code\":%d}", humanStdout, humanStderr, humanCode)
	if humanCode == 0 || !strings.Contains(humanStderr, "audit was recorded but source state was not changed") {
		t.Fatalf("human incomplete sync code=%d stdout=%q stderr=%q", humanCode, humanStdout, humanStderr)
	}

	jsonStdout, jsonStderr, jsonCode := captureRun(t, []string{"sync", "--json"})
	t.Logf("boundary=crawler_sync_json output={\"stdout\":%q,\"stderr\":%q,\"code\":%d}", jsonStdout, jsonStderr, jsonCode)
	if jsonCode == 0 || jsonStderr != "" {
		t.Fatalf("JSON incomplete sync code=%d stdout=%q stderr=%q", jsonCode, jsonStdout, jsonStderr)
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Remedy  string `json:"remedy"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(jsonStdout), &envelope); err != nil {
		t.Fatalf("JSON incomplete sync output: %v\n%s", err, jsonStdout)
	}
	if envelope.Error.Code != "snapshot_incomplete" || envelope.Error.Message == "" || envelope.Error.Remedy == "" {
		t.Fatalf("JSON incomplete sync error = %#v", envelope.Error)
	}
	status, err := archive.Status(context.Background(), archive.Paths{Database: archivePath})
	if err != nil {
		t.Fatal(err)
	}
	if status.LastImportAt != before.LastImportAt || status.State != "ok" {
		t.Fatalf("incomplete crawler status = %#v", status)
	}

	searchJSON, searchJSONErr, searchJSONCode := captureRun(t, []string{"search", "synthetic", "--json"})
	t.Logf("boundary=crawler_search_json output={\"stdout\":%q,\"stderr\":%q,\"code\":%d}", searchJSON, searchJSONErr, searchJSONCode)
	if searchJSONCode != 0 || searchJSONErr != "" {
		t.Fatalf("search JSON code=%d stdout=%q stderr=%q", searchJSONCode, searchJSON, searchJSONErr)
	}
	var search struct {
		Results []struct {
			Ref string `json:"ref"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(searchJSON), &search); err != nil || len(search.Results) != 1 {
		t.Fatalf("search JSON = %q, err=%v", searchJSON, err)
	}
	searchHuman, searchHumanErr, searchHumanCode := captureRun(t, []string{"search", "synthetic"})
	t.Logf("boundary=crawler_search_human output={\"stdout\":%q,\"stderr\":%q,\"code\":%d}", searchHuman, searchHumanErr, searchHumanCode)
	if searchHumanCode != 0 || searchHumanErr != "" || !strings.Contains(searchHuman, "synthetic.heic") {
		t.Fatalf("search human code=%d stdout=%q stderr=%q", searchHumanCode, searchHuman, searchHumanErr)
	}

	ref := search.Results[0].Ref
	openJSON, openJSONErr, openJSONCode := captureRun(t, []string{"open", ref, "--json"})
	t.Logf("boundary=crawler_open_json output={\"stdout\":%q,\"stderr\":%q,\"code\":%d}", openJSON, openJSONErr, openJSONCode)
	var openResponse openv1.OpenResponse
	if err := (protojson.UnmarshalOptions{}).Unmarshal([]byte(openJSON), &openResponse); openJSONCode != 0 || openJSONErr != "" || err != nil || openResponse.GetRecord().GetOpenRef() != ref {
		t.Fatalf("open JSON code=%d stdout=%q stderr=%q", openJSONCode, openJSON, openJSONErr)
	}
	openHuman, openHumanErr, openHumanCode := captureRun(t, []string{"open", ref})
	t.Logf("boundary=crawler_open_human output={\"stdout\":%q,\"stderr\":%q,\"code\":%d}", openHuman, openHumanErr, openHumanCode)
	if openHumanCode != 2 || openHuman != "" || !strings.Contains(openHumanErr, "open requires --json") {
		t.Fatalf("open human code=%d stdout=%q stderr=%q", openHumanCode, openHuman, openHumanErr)
	}
}
