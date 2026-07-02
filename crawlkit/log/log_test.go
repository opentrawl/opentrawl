package log

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLogContract(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "grammar and self documenting header",
			run: func(t *testing.T) {
				run := newTestRun(t, "grammar-run", fixedTime())
				if err := run.Info("scan", "processed items"); err != nil {
					t.Fatal(err)
				}
				if err := run.Finish(nil); err != nil {
					t.Fatal(err)
				}

				lines := readLogLines(t, run.Path())
				if len(lines) < 4 {
					t.Fatalf("lines=%d, want at least 4", len(lines))
				}
				for _, raw := range lines {
					if _, ok := ParseLine(raw); !ok {
						t.Fatalf("line does not match grammar: %q", raw)
					}
				}

				header, _ := ParseLine(lines[0])
				if header.RunID != "-" || header.Command != "-" || header.Event != "grammar" || header.Message != Grammar {
					t.Fatalf("bad grammar header: %+v", header)
				}

				start, _ := ParseLine(lines[1])
				fields := parseFields(start.Message)
				if start.Event != "start" || fields["version"] != "0.4.1" || fields["commit"] != "8f3c2d" || fields["platform"] != "macos 15" {
					t.Fatalf("bad start line: %+v fields=%v", start, fields)
				}
			},
		},
		{
			name: "rotation bound keeps header and drops oldest lines",
			run: func(t *testing.T) {
				oldLimit := rotationLimitBytes
				rotationLimitBytes = 900
				t.Cleanup(func() { rotationLimitBytes = oldLimit })

				run := newTestRun(t, "rotate-run", fixedTime())
				for i := 0; i < 40; i++ {
					if err := run.Info("chunk", fmt.Sprintf("line=%02d marker=%s", i, strings.Repeat("x", 24))); err != nil {
						t.Fatal(err)
					}
				}

				info, err := os.Stat(run.Path())
				if err != nil {
					t.Fatal(err)
				}
				if info.Size() > rotationLimitBytes {
					t.Fatalf("log size=%d, want <= %d", info.Size(), rotationLimitBytes)
				}
				lines := readLogLines(t, run.Path())
				header, ok := ParseLine(lines[0])
				if !ok || header.Event != "grammar" {
					t.Fatalf("rotation lost grammar header: %q", lines[0])
				}
				body := strings.Join(lines, "\n")
				if strings.Contains(body, "line=00") {
					t.Fatalf("oldest line was not dropped:\n%s", body)
				}
				if !strings.Contains(body, "line=39") {
					t.Fatalf("newest line missing after rotation:\n%s", body)
				}
			},
		},
		{
			name: "interleaved run filtering skips malformed lines",
			run: func(t *testing.T) {
				stateRoot := t.TempDir()
				now := fixedTime()
				a := newTestRunAt(t, stateRoot, "run-a", now)
				b := newTestRunAt(t, stateRoot, "run-b", now)
				if err := a.Info("scan", "a one"); err != nil {
					t.Fatal(err)
				}
				if err := b.Info("scan", "b one"); err != nil {
					t.Fatal(err)
				}
				if err := a.Error("backup_failed", errors.New("backup fetch exited early")); err != nil {
					t.Fatal(err)
				}
				if err := b.Finish(nil); err != nil {
					t.Fatal(err)
				}
				if err := a.Finish(errors.New("backup fetch exited early")); err != nil {
					t.Fatal(err)
				}
				file, err := os.OpenFile(a.Path(), os.O_WRONLY|os.O_APPEND, 0o644)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.WriteString("not a crawlkit log line\n"); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}

				reader, err := NewReader(stateRoot, "crawl")
				if err != nil {
					t.Fatal(err)
				}
				lines, err := reader.RecentLines("run-a", 50)
				if err != nil {
					t.Fatal(err)
				}
				if len(lines) == 0 {
					t.Fatal("no run-a lines returned")
				}
				for _, line := range lines {
					if line.RunID != "run-a" {
						t.Fatalf("unfiltered line returned: %+v", line)
					}
				}
				errLine, ok, err := reader.MostRecentError("run-a")
				if err != nil {
					t.Fatal(err)
				}
				if !ok || errLine.RunID != "run-a" || errLine.Level != LevelError {
					t.Fatalf("bad most recent error: ok=%v line=%+v", ok, errLine)
				}
				if _, ok, err := reader.MostRecentError("run-b"); err != nil || ok {
					t.Fatalf("run-b error ok=%v err=%v", ok, err)
				}
				summary, ok, err := reader.LastRun("run-a")
				if err != nil {
					t.Fatal(err)
				}
				if !ok || summary.Outcome != "error" || summary.Error == nil || summary.RunID != "run-a" {
					t.Fatalf("bad summary: ok=%v summary=%+v", ok, summary)
				}
			},
		},
		{
			name: "redaction refuses secrets and long content values",
			run: func(t *testing.T) {
				run := newTestRun(t, "redact-run", fixedTime())
				before := strings.Join(readLogLines(t, run.Path()), "\n")
				checks := []struct {
					name    string
					message string
				}{
					{name: "secret pattern", message: "api_key=sk-12345678901234567890"},
					{name: "long content value", message: "body=" + strings.Repeat("x", maxContentValueLength+1)},
				}
				for _, check := range checks {
					t.Run(check.name, func(t *testing.T) {
						err := run.Info("unsafe", check.message)
						if !errors.Is(err, ErrUnsafeLogLine) {
							t.Fatalf("err=%v, want ErrUnsafeLogLine", err)
						}
					})
				}
				after := strings.Join(readLogLines(t, run.Path()), "\n")
				want := before + "\n" + strings.Join([]string{
					"2026-07-02 22:41:03 WARN  redact-run sync log_line_refused: event=unsafe",
					"2026-07-02 22:41:03 WARN  redact-run sync log_line_refused: event=unsafe",
				}, "\n")
				if after != want {
					t.Fatalf("unsafe lines produced wrong log:\nwant:\n%s\nafter:\n%s", want, after)
				}
				if strings.Contains(after, "sk-12345678901234567890") || strings.Contains(after, strings.Repeat("x", maxContentValueLength+1)) {
					t.Fatalf("unsafe content reached log:\n%s", after)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

func TestConcurrentRotationKeepsSharedLogWellFormed(t *testing.T) {
	oldLimit := rotationLimitBytes
	rotationLimitBytes = 12_000
	t.Cleanup(func() { rotationLimitBytes = oldLimit })

	stateRoot := t.TempDir()
	now := fixedTime()
	runA := newTestRunAt(t, stateRoot, "run-a", now)
	runB := newTestRunAt(t, stateRoot, "run-b", now)

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, item := range []struct {
		run    *Run
		writer string
	}{
		{run: runA, writer: "a"},
		{run: runB, writer: "b"},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range 120 {
				if err := item.run.Info("chunk", fmt.Sprintf("writer=%s line=%03d marker=%s", item.writer, n, strings.Repeat("x", 48))); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	markerErrs := make(chan error, 2)
	var markerWG sync.WaitGroup
	for _, item := range []struct {
		run    *Run
		writer string
	}{
		{run: runA, writer: "a"},
		{run: runB, writer: "b"},
	} {
		markerWG.Add(1)
		go func() {
			defer markerWG.Done()
			if err := item.run.Info("tail_marker", "writer="+item.writer); err != nil {
				markerErrs <- err
			}
		}()
	}
	markerWG.Wait()
	close(markerErrs)
	for err := range markerErrs {
		if err != nil {
			t.Fatal(err)
		}
	}

	info, err := os.Stat(runA.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > rotationLimitBytes {
		t.Fatalf("log size=%d, want <= %d", info.Size(), rotationLimitBytes)
	}

	lines := readLogLines(t, runA.Path())
	if len(lines) == 0 {
		t.Fatal("log is empty")
	}
	header, ok := ParseLine(lines[0])
	if !ok || header.Event != "grammar" {
		t.Fatalf("rotation lost grammar header: %q", lines[0])
	}
	hasA := false
	hasB := false
	for _, raw := range lines {
		line, ok := ParseLine(raw)
		if !ok {
			t.Fatalf("line does not match grammar: %q", raw)
		}
		if line.Event == "tail_marker" && line.RunID == "run-a" && line.Message == "writer=a" {
			hasA = true
		}
		if line.Event == "tail_marker" && line.RunID == "run-b" && line.Message == "writer=b" {
			hasB = true
		}
	}
	if !hasA || !hasB {
		t.Fatalf("tail markers missing after concurrent rotation: hasA=%v hasB=%v\n%s", hasA, hasB, strings.Join(lines, "\n"))
	}
}

func TestDebugProgressAndWorldMustChange(t *testing.T) {
	now := fixedTime()
	var human bytes.Buffer
	run := newTestRunWithOptions(t, Options{
		StateRoot: "unused",
		CrawlerID: "crawl",
		RunID:     "debug-run",
		Command:   "sync",
		Version:   "0.4.1",
		Commit:    "8f3c2d",
		Platform:  "macos 15",
		Stderr:    &human,
		Now:       func() time.Time { return now },
	})
	if err := run.Debug("detail", "hidden"); err != nil {
		t.Fatal(err)
	}
	if err := run.Progress(ProgressOptions{Total: 4, Unit: "items"}).Report(1, "fetching backup"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), "fetching backup 1/4 items elapsed=0s") {
		t.Fatalf("human progress missing: %q", human.String())
	}
	if strings.Contains(strings.Join(readLogLines(t, run.Path()), "\n"), "hidden") {
		t.Fatal("debug line was logged while debug was off")
	}

	var jsonOut bytes.Buffer
	jsonRun := newTestRunWithOptions(t, Options{
		StateRoot:    t.TempDir(),
		CrawlerID:    "crawl",
		RunID:        "json-run",
		Command:      "sync",
		Version:      "0.4.1",
		Commit:       "8f3c2d",
		Platform:     "macos 15",
		JSONProgress: true,
		Stderr:       &jsonOut,
		Now:          func() time.Time { return now },
	})
	if err := jsonRun.Progress(ProgressOptions{Total: 2}).Report(1, "reading cache"); err != nil {
		t.Fatal(err)
	}
	var event progressEvent
	if err := json.Unmarshal(bytes.TrimSpace(jsonOut.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "progress" || event.RunID != "json-run" || event.Message != "reading cache" {
		t.Fatalf("bad JSON progress event: %+v", event)
	}

	needsWorldChange := WorldMustChange{
		Err:    errors.New("calendar permission denied"),
		Remedy: "grant calendar access",
	}
	if err := jsonRun.Error("permission_denied", needsWorldChange); err != nil {
		t.Fatal(err)
	}
	errorLine, ok, err := NewReaderForTest(t, jsonRun).MostRecentError("json-run")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(errorLine.Message, `remedy="grant calendar access"`) {
		t.Fatalf("remedy missing from world-change error: ok=%v line=%+v", ok, errorLine)
	}
}

func newTestRun(t *testing.T, runID string, now time.Time) *Run {
	t.Helper()
	return newTestRunAt(t, t.TempDir(), runID, now)
}

func newTestRunAt(t *testing.T, stateRoot, runID string, now time.Time) *Run {
	t.Helper()
	return newTestRunWithOptions(t, Options{
		StateRoot: stateRoot,
		CrawlerID: "crawl",
		RunID:     runID,
		Command:   "sync",
		Version:   "0.4.1",
		Commit:    "8f3c2d",
		Platform:  "macos 15",
		Now:       func() time.Time { return now },
	})
}

func newTestRunWithOptions(t *testing.T, opts Options) *Run {
	t.Helper()
	if opts.StateRoot == "unused" {
		opts.StateRoot = t.TempDir()
	}
	run, err := NewRun(opts)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func NewReaderForTest(t *testing.T, run *Run) *Reader {
	t.Helper()
	reader, err := NewReader(run.stateRoot, run.crawlerID)
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func readLogLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func fixedTime() time.Time {
	return time.Date(2026, 7, 2, 22, 41, 3, 0, time.UTC)
}
