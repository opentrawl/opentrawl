package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJoinedQueryPreservesLauncherArguments(t *testing.T) {
	if got := joinedQuery("hello", []string{"world", "photos"}); got != "hello world photos" {
		t.Fatalf("joined query = %q", got)
	}
	if got := joinedQuery("", []string{"hello", "world"}); got != "hello world" {
		t.Fatalf("positional query = %q", got)
	}
}

func TestStripTrailingJSON(t *testing.T) {
	args, ok := stripTrailingJSON([]string{"boat", "trip", "--json"})
	if !ok {
		t.Fatal("expected trailing JSON flag")
	}
	if got := joinedQuery("", args); got != "boat trip" {
		t.Fatalf("query after strip = %q", got)
	}

	args, ok = stripTrailingJSON([]string{"--json", "boat"})
	if ok {
		t.Fatal("did not expect non-trailing JSON flag to be stripped")
	}
	if got := joinedQuery("", args); got != "--json boat" {
		t.Fatalf("query without strip = %q", got)
	}
}

func TestStatusHumanOutputIsProse(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "photos.sqlite")
	out, errOut, err := captureRunOutput(t, []string{"status", "--db", dbPath})
	if err != nil {
		t.Fatalf("status: %v stderr=%s stdout=%s", err, errOut, out)
	}
	assertHumanProseOutput(t, out,
		"Status: missing",
		"photos.sqlite has not been initialized",
		"Counts:",
		"none",
		"Paths:",
		"Database:",
	)
}

func TestDoctorHumanOutputIsProse(t *testing.T) {
	dir := t.TempDir()
	libraryPath := filepath.Join(dir, "Fixture Photos Library.photoslibrary")
	if err := os.MkdirAll(libraryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "photos.sqlite")
	out, errOut, err := captureRunOutput(t, []string{"doctor", "--db", dbPath, "--library", libraryPath})
	if err != nil {
		t.Fatalf("doctor: %v stderr=%s stdout=%s", err, errOut, out)
	}
	assertHumanProseOutput(t, out,
		"Doctor checks:",
		"source_store:",
		"archive:",
		"Remedy:",
	)
}

func captureRunOutput(t *testing.T, args []string) (string, string, error) {
	t.Helper()
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = stdoutW
	os.Stderr = stderrW
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	runErr := run(context.Background(), args)
	if err := stdoutW.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrW.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatal(err)
	}
	if err := stdoutR.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrR.Close(); err != nil {
		t.Fatal(err)
	}
	return string(stdout), string(stderr), runErr
}

func assertHumanProseOutput(t *testing.T, got string, wants ...string) {
	t.Helper()
	if strings.HasPrefix(strings.TrimSpace(got), "{") {
		t.Fatalf("human output starts like JSON or a Go struct: %q", got)
	}
	if strings.Contains(got, "{[{") {
		t.Fatalf("human output contains Go struct debris: %q", got)
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("human output missing %q:\n%s", want, got)
		}
	}
}
