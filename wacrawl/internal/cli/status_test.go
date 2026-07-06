package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/wacrawl/internal/store"
)

func TestStatusLogErrorOutputFiltersByVisibility(t *testing.T) {
	internal := statusLogErrorOutput(&logErrorEnvelope{
		Command:    "sync",
		Event:      "sync_failed",
		Message:    "boom",
		visibility: cklog.VisibilityInternal,
	})
	if internal != nil {
		t.Fatalf("internal status error = %#v, want nil", internal)
	}

	userFacing := statusLogErrorOutput(&logErrorEnvelope{
		Command:    "search",
		Event:      "archive_missing",
		Message:    errNoArchive.Error(),
		Remedy:     "run wacrawl sync",
		visibility: cklog.VisibilityUserFacing,
	})
	if userFacing == nil {
		t.Fatal("user-facing status error = nil, want line")
	}
	if userFacing.Visibility != cklog.VisibilityUserFacing {
		t.Fatalf("visibility = %q, want %q", userFacing.Visibility, cklog.VisibilityUserFacing)
	}
}

func TestStatusDropsInternalLogError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	dbPath := writeStatusArchive(t, ctx)
	writeStatusLogError(t, "sync", "sync_failed", errors.New("boom"))

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status json error = %v stderr=%s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), "boom") || strings.Contains(stdout.String(), "most_recent_error") {
		t.Fatalf("internal error leaked in status json:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status human error = %v stderr=%s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), "boom") || strings.Contains(stdout.String(), "Most recent error") {
		t.Fatalf("internal error leaked in status human output:\n%s", stdout.String())
	}
}

func TestMissingStatusDropsInternalLogError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "missing.db")
	writeStatusLogError(t, "sync", "sync_failed", errors.New("boom"))

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("missing status json error = %v stderr=%s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), "boom") || strings.Contains(stdout.String(), "most_recent_error") {
		t.Fatalf("internal error leaked in missing status json:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("missing status human error = %v stderr=%s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), "boom") || strings.Contains(stdout.String(), "Most recent error") {
		t.Fatalf("internal error leaked in missing status human output:\n%s", stdout.String())
	}
}

func TestStatusKeepsUserFacingLogError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	dbPath := writeStatusArchive(t, ctx)
	userErr := worldMustChange(errNoArchive, "run wacrawl sync")
	writeStatusLogError(t, "search", "archive_missing", userErr)

	var stdout, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status json error = %v stderr=%s", err, stderr.String())
	}
	jsonOut := stdout.String()
	for _, want := range []string{`"most_recent_error"`, errNoArchive.Error(), `"remedy": "run wacrawl sync"`} {
		if !strings.Contains(jsonOut, want) {
			t.Fatalf("status json missing %q:\n%s", want, jsonOut)
		}
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status human error = %v stderr=%s", err, stderr.String())
	}
	humanOut := stdout.String()
	for _, want := range []string{"Most recent error:", errNoArchive.Error(), "Remedy: run wacrawl sync"} {
		if !strings.Contains(humanOut, want) {
			t.Fatalf("status human missing %q:\n%s", want, humanOut)
		}
	}
}

func writeStatusArchive(t *testing.T, ctx context.Context) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "archive.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	imported := time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)
	err = st.ReplaceAll(
		ctx,
		store.ImportStats{SourcePath: "/synthetic", DBPath: dbPath, FinishedAt: imported},
		nil,
		[]store.Chat{{JID: "chat@g.us", Kind: "group", Name: "Launch Group", LastMessageAt: imported, MessageCount: 1}},
		nil,
		nil,
		[]store.Message{{SourcePK: 1, ChatJID: "chat@g.us", ChatName: "Launch Group", MessageID: "m1", Timestamp: imported, RawType: 0, Text: "hello"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

func writeStatusLogError(t *testing.T, command, event string, err error) {
	t.Helper()
	a := &app{stderr: io.Discard}
	run, runErr := a.newLogRun(command)
	if runErr != nil {
		t.Fatal(runErr)
	}
	if err := run.Error(event, err); err != nil {
		t.Fatal(err)
	}
	if err := run.Finish(err); err != nil {
		t.Fatal(err)
	}
}
