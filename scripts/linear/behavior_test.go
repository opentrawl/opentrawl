package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDecodeGraphResponseKeepsDataWithWarning(t *testing.T) {
	logger, stderr := testRequestLogger(t)
	var out struct {
		Value string `json:"value"`
	}
	err := decodeGraphResponse([]byte(`{"data":{"value":"kept"},"errors":[{"message":"partial failure"}]}`), &out, logger)
	if err != nil {
		t.Fatalf("decodeGraphResponse returned error: %v", err)
	}
	if out.Value != "kept" {
		t.Fatalf("Value = %q, want kept", out.Value)
	}
	if count := strings.Count(stderr.String(), "Linear GraphQL returned data with errors"); count != 1 {
		t.Fatalf("warning count = %d, want 1; stderr %q", count, stderr.String())
	}
}

func TestDecodeGraphResponseErrorsWithoutDataDoNotWarn(t *testing.T) {
	logger, stderr := testRequestLogger(t)
	var out struct {
		Value string `json:"value"`
	}
	err := decodeGraphResponse([]byte(`{"data":null,"errors":[{"message":"bad token"}]}`), &out, logger)
	if err == nil {
		t.Fatal("expected error")
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestTokenCacheInvalidFileIsTreatedAsAbsent(t *testing.T) {
	logger, stderr := testRequestLogger(t)
	path := filepath.Join(t.TempDir(), "token.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &TokenStore{path: path, logger: logger, now: time.Now}
	_, ok, err := store.load()
	if err != nil {
		t.Fatalf("load returned error: %v", err)
	}
	if ok {
		t.Fatal("load reported a token for invalid JSON")
	}
	if count := strings.Count(stderr.String(), "token cache was invalid; minting a new token"); count != 1 {
		t.Fatalf("warning count = %d, want 1; stderr %q", count, stderr.String())
	}
}

func TestParseFlagsInterspersed(t *testing.T) {
	fs := newFlagSet("test")
	team := fs.String("team", "", "")
	state := fs.String("state", "", "")
	positionals, err := parseFlags([]string{"first", "--team", "TRAWL", "second", "--state=Done"}, fs)
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if *team != "TRAWL" {
		t.Fatalf("team = %q, want TRAWL", *team)
	}
	if *state != "Done" {
		t.Fatalf("state = %q, want Done", *state)
	}
	want := []string{"first", "second"}
	if !reflect.DeepEqual(positionals, want) {
		t.Fatalf("positionals = %#v, want %#v", positionals, want)
	}
}

func TestMatchStateRefusesUnknownWithCandidates(t *testing.T) {
	states := []IssueState{
		{ID: "id-todo", Name: "Todo"},
		{ID: "id-done", Name: "Done"},
		{ID: "id-progress", Name: "In Progress"},
	}
	_, err := matchState(states, "TRAWL", "Finished")
	if err == nil {
		t.Fatal("expected error for unknown state")
	}
	want := `state "Finished" was not found for team TRAWL. Valid states: Done, In Progress, Todo`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestMatchStateMatchesCaseInsensitively(t *testing.T) {
	states := []IssueState{
		{ID: "id-progress", Name: "In Progress"},
		{ID: "id-done", Name: "Done"},
	}
	match, err := matchState(states, "TRAWL", "in progress")
	if err != nil {
		t.Fatalf("matchState returned error: %v", err)
	}
	if match.ID != "id-progress" || match.Name != "In Progress" {
		t.Fatalf("match = %+v, want In Progress", match)
	}
}

func TestMatchStateRefusesAmbiguousName(t *testing.T) {
	states := []IssueState{
		{ID: "id-1", Name: "Done"},
		{ID: "id-2", Name: "done"},
	}
	_, err := matchState(states, "TRAWL", "Done")
	if err == nil {
		t.Fatal("expected error for ambiguous state")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %q, want ambiguous", err.Error())
	}
}

func TestIssueStateRequiresActorAndState(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := execute([]string{"issue", "state", "TRAWL-1", "--state", "Done"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || err.Error() != "--as is required for write commands" {
		t.Fatalf("err = %v, want --as is required for write commands", err)
	}
	err = execute([]string{"issue", "state", "TRAWL-1", "--as", "coordinator"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || err.Error() != "--state is required" {
		t.Fatalf("err = %v, want --state is required", err)
	}
}

func testRequestLogger(t *testing.T) (*requestLogger, *bytes.Buffer) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "linear.log")
	if err != nil {
		t.Fatal(err)
	}
	stderr := &bytes.Buffer{}
	logger := &requestLogger{stderr: stderr, file: file}
	t.Cleanup(func() {
		_ = logger.Close()
	})
	return logger, stderr
}
