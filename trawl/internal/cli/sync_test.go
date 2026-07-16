package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSyncRunsSourcesSequentiallyAndRendersSummary(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`,
			sync:     "{\"event\":\"progress\",\"stage\":\"messages\",\"done\":1,\"total\":2}\n{\"state\":\"ok\",\"added\":2}",
		},
		fakeCrawler{
			name:     "telecrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"telegram","display_name":"Telegram"}`,
			sync:     `{"state":"ok","added":89}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "sync", "imessage", "telegram")
	if code != 0 {
		t.Fatalf("sync code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	for _, want := range []string{
		"Messages  ok     2 added",
		"Telegram  ok     89 added",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	for _, want := range []string{"Messages syncing…", "Telegram syncing…"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestSyncJSONWritesOneEventPerSource(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`,
		sync:     `{"state":"ok","added":2}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "sync", "imessage")
	if code != 0 {
		t.Fatalf("sync --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 1 {
		t.Fatalf("lines = %d stdout=%s", len(lines), stdout)
	}
	var event SyncResult
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("event JSON = %s err=%v", lines[0], err)
	}
	if event.Event != "sync" || event.Source != "imessage" || event.State != "ok" {
		t.Fatalf("event = %#v", event)
	}
	if strings.Contains(stdout, `"stage"`) {
		t.Fatalf("sync --json leaked crawler progress event:\n%s", stdout)
	}
}

func TestSyncPreservesIncompleteSnapshotFailure(t *testing.T) {
	const childError = `{"error":{"code":"snapshot_incomplete","message":"Photos snapshot was limited; audit was recorded but source state was not changed","remedy":"restore complete Photos access or wait for the snapshot to finish, then rerun sync"}}`
	const message = "Photos snapshot was limited; audit was recorded but source state was not changed"
	const remedy = "restore complete Photos access or wait for the snapshot to finish, then rerun sync"
	newCrawler := func() fakeCrawler {
		return fakeCrawler{
			name:     "photoscrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"photos","display_name":"Photos"}`,
			sync:     childError,
			syncExit: 1,
		}
	}

	t.Run("human", func(t *testing.T) {
		binDir := writeFakeCrawlers(t, newCrawler())
		t.Setenv("PATH", binDir)
		t.Setenv("HOME", syntheticHome(t))
		t.Logf("boundary=child_sync_error input=%s", childError)

		stdout, stderr, code := runCLI(t, "sync", "photos")
		t.Logf("boundary=root_sync_human output={\"stdout\":%q,\"stderr\":%q,\"code\":%d}", stdout, stderr, code)
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		wantStdout := "Photos  error  " + message + "\n"
		if stdout != wantStdout {
			t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
		}
		wantStderr := "Photos syncing…\nPhotos sync failed (snapshot_incomplete).\n  Remedy: " + remedy + "\n"
		if stderr != wantStderr {
			t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
		}
	})

	t.Run("JSON", func(t *testing.T) {
		binDir := writeFakeCrawlers(t, newCrawler())
		t.Setenv("PATH", binDir)
		t.Setenv("HOME", syntheticHome(t))
		t.Logf("boundary=child_sync_error input=%s", childError)

		stdout, stderr, code := runCLI(t, "--json", "sync", "photos")
		t.Logf("boundary=root_sync_json output={\"stdout\":%q,\"stderr\":%q,\"code\":%d}", stdout, stderr, code)
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		var result SyncResult
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatalf("stdout is not a sync result: %v\n%s", err, stdout)
		}
		if result.Event != "sync" || result.Source != "photos" || result.State != "error" || result.Message != message {
			t.Fatalf("sync result = %#v", result)
		}
		if result.Error == nil || result.Error.Code != "snapshot_incomplete" || result.Error.Message != message || result.Error.Remedy != remedy {
			t.Fatalf("sync error = %#v", result.Error)
		}
		wantStderr := "Photos syncing…\nPhotos sync failed (snapshot_incomplete).\n  Remedy: " + remedy + "\n"
		if stderr != wantStderr {
			t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
		}
	})
}

func TestSyncPartialAndTotalFailures(t *testing.T) {
	tests := []struct {
		name       string
		crawlers   []fakeCrawler
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name: "partial failure",
			crawlers: []fakeCrawler{
				{
					name:     "imsgcrawl",
					metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`,
					sync:     `{"state":"ok","added":2}`,
				},
				{
					name:     "telecrawl",
					metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"telegram","display_name":"Telegram"}`,
					sync:     `not-json`,
				},
			},
			args:       []string{"sync"},
			wantCode:   3,
			wantStdout: "Telegram  error  sync failed",
			wantStderr: "Telegram sync failed.\n  Remedy: Review OpenTrawl's logs for this source, then sync again.",
		},
		{
			name: "all failed",
			crawlers: []fakeCrawler{{
				name:     "telecrawl",
				metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"telegram","display_name":"Telegram"}`,
				sync:     `not-json`,
			}},
			args:       []string{"sync", "telegram"},
			wantCode:   1,
			wantStdout: "Telegram  error  sync failed",
			wantStderr: "Telegram sync failed.\n  Remedy: Review OpenTrawl's logs for this source, then sync again.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := writeFakeCrawlers(t, tt.crawlers...)
			t.Setenv("PATH", binDir)
			t.Setenv("HOME", syntheticHome(t))

			stdout, stderr, code := runCLI(t, tt.args...)
			if code != tt.wantCode {
				t.Fatalf("code = %d, want %d stdout=%s stderr=%s", code, tt.wantCode, stdout, stderr)
			}
			if !strings.Contains(stdout, tt.wantStdout) {
				t.Fatalf("stdout missing %q:\n%s", tt.wantStdout, stdout)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Fatalf("stderr missing %q:\n%s", tt.wantStderr, stderr)
			}
		})
	}
}

func TestSyncPartialResultExitsPartial(t *testing.T) {
	err := syncExit([]SyncResult{{State: "partial"}})
	exit, ok := err.(exitErr)
	if !ok || exit.code != 3 {
		t.Fatalf("syncExit(partial) = %v, want exit 3", err)
	}
}

func TestSyncUnknownSource(t *testing.T) {
	binDir := writeFakeCrawlers(t)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "sync", "missing")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, `Source "missing" was not found.`) {
		t.Fatalf("stderr missing source error:\n%s", stderr)
	}
}

func TestRunSourceSyncUsesChildRoutePastReadTimeout(t *testing.T) {
	t.Setenv("HOME", syntheticHome(t))
	writeFakeCrawlers(t, fakeCrawler{
		name:      "slow-sync",
		metadata:  `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"slow-sync","display_name":"Slow sync"}`,
		sync:      `{"state":"ok","added":1}`,
		syncSleep: "120ms",
	})

	r := &Runtime{
		ctx:     context.Background(),
		timeout: 50 * time.Millisecond,
	}
	source := discoverCrawlers(context.Background())[0]
	started := time.Now()
	data, stderr, err := r.runSourceSync(source, nil)
	if err != nil {
		t.Fatalf("runSourceSync err=%v stderr=%s stdout=%s", err, stderr, data)
	}
	if elapsed := time.Since(started); elapsed < 100*time.Millisecond {
		t.Fatalf("sync returned before slow child completed: elapsed=%s stdout=%s", elapsed, data)
	}
	outcome, ok := lastSyncOutcome(data)
	if !ok {
		t.Fatalf("sync stdout did not include final JSON outcome:\n%s", data)
	}
	if got := outcome.Added.String(); got != "1" {
		t.Fatalf("added = %q, want 1; stdout=%s", got, data)
	}
}

func TestSplitSyncArgsKeepsOnePublicSyncSurfaceWithoutLosingSourceFlags(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantSources []string
		wantFlags   []string
		wantErr     bool
	}{
		{name: "all defaults"},
		{name: "several sources", args: []string{"imessage", "telegram"}, wantSources: []string{"imessage", "telegram"}},
		{name: "one source flags", args: []string{"telegram", "--fetch-media", "--messages-limit", "0"}, wantSources: []string{"telegram"}, wantFlags: []string{"--fetch-media", "--messages-limit", "0"}},
		{name: "explicit separator", args: []string{"notes", "--", "--store", "/tmp/NoteStore.sqlite"}, wantSources: []string{"notes"}, wantFlags: []string{"--store", "/tmp/NoteStore.sqlite"}},
		{name: "ambiguous flags", args: []string{"imessage", "telegram", "--fetch-media"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sources, flags, err := splitSyncArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("splitSyncArgs(%q) err = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
			if !slices.Equal(sources, tt.wantSources) || !slices.Equal(flags, tt.wantFlags) {
				t.Fatalf("splitSyncArgs(%q) = (%q, %q), want (%q, %q)", tt.args, sources, flags, tt.wantSources, tt.wantFlags)
			}
		})
	}
}

func TestSyncSourceHelpUsesTheRootSpelling(t *testing.T) {
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "sync", "telegram", "--help")
	if code != 0 {
		t.Fatalf("sync help code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "trawl sync telegram: Sync the archive") {
		t.Fatalf("sync help did not use canonical root spelling:\n%s", stdout)
	}
	if strings.Contains(stdout, "trawl telegram sync") {
		t.Fatalf("sync help advertised the removed source spelling:\n%s", stdout)
	}
}
