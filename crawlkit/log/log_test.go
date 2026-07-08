package log

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
					"2026-07-02 22:41:03 WARN  redact-run sync log_line_refused: event=unsafe visibility=internal",
					"2026-07-02 22:41:03 WARN  redact-run sync log_line_refused: event=unsafe visibility=internal",
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
		if line.Event == "tail_marker" && line.RunID == "run-a" && parseFields(line.Message)["writer"] == "a" {
			hasA = true
		}
		if line.Event == "tail_marker" && line.RunID == "run-b" && parseFields(line.Message)["writer"] == "b" {
			hasB = true
		}
	}
	if !hasA || !hasB {
		t.Fatalf("tail markers missing after concurrent rotation: hasA=%v hasB=%v\n%s", hasA, hasB, strings.Join(lines, "\n"))
	}
}

func TestFinishEventIsReservedForRunFinish(t *testing.T) {
	run := newTestRunWithOptions(t, Options{
		StateRoot: t.TempDir(),
		CrawlerID: "crawl",
		RunID:     "reserved-finish-run",
		Command:   "status",
		Version:   "0.4.1",
		Commit:    "8f3c2d",
		Platform:  "macos 15",
		Debug:     true,
		Now:       func() time.Time { return fixedTime() },
	})
	writers := map[string]func() error{
		"info":  func() error { return run.Info("finish", "outcome=succeeded") },
		"warn":  func() error { return run.Warn("finish", "outcome=succeeded") },
		"debug": func() error { return run.Debug("finish", "outcome=succeeded") },
		"error": func() error { return run.Error("finish", errors.New("boom")) },
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			err := write()
			if err == nil || !strings.Contains(err.Error(), "reserved") {
				t.Fatalf("err=%v, want reserved finish event error", err)
			}
		})
	}
	if err := run.Finish(nil); err != nil {
		t.Fatal(err)
	}
	logText := strings.Join(readLogLines(t, run.Path()), "\n")
	if strings.Contains(logText, "succeeded") {
		t.Fatalf("caller-provided finish outcome reached log:\n%s", logText)
	}
	summary, ok, err := NewReaderForTest(t, run).LastRun(run.RunID())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || summary.Outcome != "success" {
		t.Fatalf("summary outcome = %q ok=%v, want success", summary.Outcome, ok)
	}
}

func TestReaderCanonicalizesFinishOutcome(t *testing.T) {
	cases := []struct {
		name    string
		level   Level
		outcome string
		want    string
	}{
		{name: "legacy succeeded", level: LevelInfo, outcome: "succeeded", want: "success"},
		{name: "legacy failed", level: LevelInfo, outcome: "failed", want: "error"},
		{name: "rejected finish", level: LevelInfo, outcome: "rejected", want: "rejected"},
		{name: "unknown info finish", level: LevelInfo, outcome: "done", want: "success"},
		{name: "unknown error finish", level: LevelError, outcome: "done", want: "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := newTestRunWithOptions(t, Options{
				StateRoot: t.TempDir(),
				CrawlerID: "crawl",
				RunID:     strings.ReplaceAll(tc.name, " ", "-"),
				Command:   "status",
				Version:   "0.4.1",
				Commit:    "8f3c2d",
				Platform:  "macos 15",
				Now:       func() time.Time { return fixedTime() },
			})
			line := run.formatLine(fixedTime().Add(time.Second), tc.level, run.RunID(), "status", "finish", "outcome="+tc.outcome)
			if err := run.appendLogLine(line); err != nil {
				t.Fatal(err)
			}
			summary, ok, err := NewReaderForTest(t, run).LastRun(run.RunID())
			if err != nil {
				t.Fatal(err)
			}
			if !ok || summary.Outcome != tc.want {
				t.Fatalf("summary outcome = %q ok=%v, want %q", summary.Outcome, ok, tc.want)
			}
		})
	}
}

func TestAttachRunSharesParentLifecycle(t *testing.T) {
	stateRoot := t.TempDir()
	now := fixedTime()
	parent := newTestRunWithOptions(t, Options{
		StateRoot: stateRoot,
		CrawlerID: "crawl",
		RunID:     "shared-run",
		Command:   "sync",
		Version:   "0.4.1",
		Commit:    "8f3c2d",
		Platform:  "macos 15",
		Now:       func() time.Time { return now },
	})
	child, err := AttachRun(Options{
		StateRoot: stateRoot,
		CrawlerID: "crawl",
		RunID:     parent.RunID(),
		Command:   "sync",
		Version:   "0.4.1",
		Commit:    "8f3c2d",
		Platform:  "macos 15",
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Info("sync_progress", "done=1 total=1"); err != nil {
		t.Fatal(err)
	}
	if err := parent.Finish(nil); err != nil {
		t.Fatal(err)
	}
	lines := readLogLines(t, parent.Path())
	starts := 0
	for _, raw := range lines {
		line, ok := ParseLine(raw)
		if !ok || line.RunID != parent.RunID() {
			continue
		}
		if line.Event == "start" {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("start lines = %d, want 1:\n%s", starts, strings.Join(lines, "\n"))
	}
	summary, ok, err := NewReaderForTest(t, parent).LastRun(parent.RunID())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || summary.Outcome != "success" || summary.LastEvent != "finish" {
		t.Fatalf("summary = %#v ok=%v", summary, ok)
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
	if errorLine.Visibility != VisibilityUserFacing {
		t.Fatalf("world-change error visibility = %q, want %q", errorLine.Visibility, VisibilityUserFacing)
	}
}

func TestJSONProgressDoesNotEscapeHTML(t *testing.T) {
	now := fixedTime()
	var stderr bytes.Buffer
	run := newTestRunWithOptions(t, Options{
		StateRoot:    t.TempDir(),
		CrawlerID:    "crawl",
		RunID:        "json-progress-raw-run",
		Command:      "sync",
		JSONProgress: true,
		Stderr:       &stderr,
		Now:          func() time.Time { return now },
	})
	if err := run.Progress(ProgressOptions{Total: 2}).Report(1, "reading <ada@example.com>"); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stderr.Bytes(), []byte(`\u003c`)) || !bytes.Contains(stderr.Bytes(), []byte(`<ada@example.com>`)) {
		t.Fatalf("JSON progress escaped HTML: %s", stderr.String())
	}
	var event progressEvent
	if err := json.Unmarshal(bytes.TrimSpace(stderr.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event.Message != "reading <ada@example.com>" {
		t.Fatalf("message = %q", event.Message)
	}
}

func TestNamedLogFileAndVerboseMirroring(t *testing.T) {
	now := fixedTime()
	var verbose bytes.Buffer
	run := newTestRunWithOptions(t, Options{
		StateRoot: t.TempDir(),
		CrawlerID: "crawl",
		FileName:  "crawl.log",
		RunID:     "verbose-run",
		Command:   "search",
		Version:   "0.4.1",
		Commit:    "8f3c2d",
		Platform:  "macos 15",
		Verbosity: 1,
		Stderr:    &verbose,
		Now:       func() time.Time { return now },
	})
	if err := run.Info("source_start", "source=mail verb=search"); err != nil {
		t.Fatal(err)
	}
	if err := run.Debug("source_exec", `argv="mail search dinner --json"`); err != nil {
		t.Fatal(err)
	}
	if err := run.Finish(nil); err != nil {
		t.Fatal(err)
	}

	if filepath.Base(run.Path()) != "crawl.log" {
		t.Fatalf("log path = %q, want named log", run.Path())
	}
	streamed := verbose.String()
	if !strings.Contains(streamed, "source_start: source=mail verb=search") {
		t.Fatalf("verbose stream missing info line:\n%s", streamed)
	}
	if strings.Contains(streamed, "source_exec") {
		t.Fatalf("-v streamed debug line:\n%s", streamed)
	}
	reader, err := NewReaderWithFileName(filepath.Dir(filepath.Dir(filepath.Dir(run.Path()))), "crawl", "crawl.log")
	if err != nil {
		t.Fatal(err)
	}
	lines, err := reader.RecentLines("verbose-run", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Fatal("named reader returned no lines")
	}

	var debugStream bytes.Buffer
	debugRun := newTestRunWithOptions(t, Options{
		StateRoot: t.TempDir(),
		CrawlerID: "crawl",
		FileName:  "crawl.log",
		RunID:     "debug-stream-run",
		Command:   "search",
		Verbosity: 2,
		Stderr:    &debugStream,
		Now:       func() time.Time { return now },
	})
	if err := debugRun.Debug("source_exec", `argv="mail search dinner --json"`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(debugStream.String(), "source_exec") {
		t.Fatalf("-vv did not stream debug line:\n%s", debugStream.String())
	}
}

func TestErrorWithVisibilityDeclaresUserFacingLine(t *testing.T) {
	run := newTestRun(t, "explicit-visibility-run", fixedTime())
	if err := run.ErrorWithVisibility("open_failed", errors.New("ref not found"), VisibilityUserFacing); err != nil {
		t.Fatal(err)
	}
	line, ok, err := NewReaderForTest(t, run).MostRecentError("explicit-visibility-run")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("most recent error missing")
	}
	if line.Visibility != VisibilityUserFacing {
		t.Fatalf("visibility = %q, want %q", line.Visibility, VisibilityUserFacing)
	}
	if !strings.Contains(line.Message, "visibility=user") {
		t.Fatalf("visibility field missing from message: %q", line.Message)
	}
}

func TestExplicitErrorVisibility(t *testing.T) {
	cases := []struct {
		name string
		line Line
		want bool
	}{
		{
			name: "user-facing visibility is shown",
			line: Line{Level: LevelError, Command: "sync", Event: "source_store_failed", Message: `error="cannot read the source database" visibility=user`, Visibility: VisibilityUserFacing},
			want: true,
		},
		{
			name: "message visibility field is enough after JSON rehydration",
			line: Line{Level: LevelError, Command: "sync", Event: "source_store_failed", Message: `error="cannot read the source database" visibility=user`},
			want: true,
		},
		{
			name: "internal visibility is hidden even with remedy",
			line: Line{Level: LevelError, Command: "search", Event: "usage_error", Message: `error="search --who requires an identity" remedy="run search with --who NAME"`},
			want: false,
		},
		{
			name: "internal visibility is hidden even with operational event name",
			line: Line{Level: LevelError, Command: "sync", Event: "sync_failed", Message: `error="backup fetch exited early" visibility=internal`, Visibility: VisibilityInternal},
			want: false,
		},
		{
			name: "legacy line with no visibility is internal",
			line: Line{Level: LevelError, Command: "doctor", Event: "permission_denied", Message: `error="calendar permission denied" remedy="grant Calendar access"`},
			want: false,
		},
		{
			name: "non-error line is hidden even when user-facing",
			line: Line{Level: LevelInfo, Command: "sync", Event: "sync_complete", Message: `visibility=user`, Visibility: VisibilityUserFacing},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUserFacingError(tc.line); got != tc.want {
				t.Fatalf("IsUserFacingError() = %v, want %v", got, tc.want)
			}
			if got := IsWorldStateError(tc.line); got != tc.want {
				t.Fatalf("IsWorldStateError() = %v, want %v", got, tc.want)
			}
		})
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
