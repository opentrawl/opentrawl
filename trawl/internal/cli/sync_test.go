package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSyncRunsSourcesSequentiallyAndRendersSummary(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
			sync:     "{\"event\":\"progress\",\"stage\":\"messages\",\"done\":1,\"total\":2}\n{\"state\":\"ok\",\"added\":2}",
		},
		fakeCrawler{
			name:     "telecrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
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
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
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
					metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
					sync:     `{"state":"ok","added":2}`,
				},
				{
					name:     "telecrawl",
					metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
					sync:     `not-json`,
				},
			},
			args:       []string{"sync"},
			wantCode:   3,
			wantStdout: "Telegram  error  sync failed",
			wantStderr: "Telegram sync failed.\n  Remedy: run trawl doctor telegram",
		},
		{
			name: "all failed",
			crawlers: []fakeCrawler{{
				name:     "telecrawl",
				metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
				sync:     `not-json`,
			}},
			args:       []string{"sync", "telegram"},
			wantCode:   1,
			wantStdout: "Telegram  error  sync failed",
			wantStderr: "Telegram sync failed.\n  Remedy: run trawl doctor telegram",
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
		metadata:  `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"slow-sync","display_name":"Slow sync"}`,
		sync:      `{"state":"ok","added":1}`,
		syncSleep: "120ms",
	})

	r := &Runtime{
		ctx:     context.Background(),
		timeout: 50 * time.Millisecond,
	}
	source := discoverCrawlers(context.Background())[0]
	started := time.Now()
	data, stderr, err := r.runSourceSync(source)
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
