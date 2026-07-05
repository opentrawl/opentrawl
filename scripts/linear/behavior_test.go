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
