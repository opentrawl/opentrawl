package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStatusExitCodes(t *testing.T) {
	tests := []struct {
		name       string
		crawlers   []fakeCrawler
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name: "success",
			crawlers: []fakeCrawler{{
				name:     "imsgcrawl",
				metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"Messages"}`,
				status:   statusJSON("imessage", "ok"),
			}},
			args:       []string{"status"},
			wantCode:   0,
			wantStdout: "imessage  ok",
		},
		{
			name:       "requested source missing",
			args:       []string{"status", "missing"},
			wantCode:   1,
			wantStderr: `Source "missing" was not found.`,
		},
		{
			name: "partial",
			crawlers: []fakeCrawler{
				{
					name:     "imsgcrawl",
					metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"Messages"}`,
					status:   statusJSON("imessage", "ok"),
				},
				{
					name:       "telecrawl",
					metadata:   `{"schema_version":1,"contract_version":1,"id":"telegram","display_name":"Telegram"}`,
					status:     `not-json`,
					statusExit: 0,
				},
			},
			args:       []string{"status"},
			wantCode:   3,
			wantStdout: "telegram  error",
			wantStderr: "telegram failed. Remedy: run: trawl doctor telegram",
		},
		{
			name: "all failed",
			crawlers: []fakeCrawler{{
				name:     "telecrawl",
				metadata: `not-json`,
			}},
			args:       []string{"status"},
			wantCode:   1,
			wantStdout: "telecrawl  error",
			wantStderr: "telecrawl failed. Remedy: run: trawl doctor telecrawl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := writeFakeCrawlers(t, tt.crawlers...)
			t.Setenv("PATH", binDir)
			t.Setenv("HOME", t.TempDir())
			stdout, stderr, code := runCLI(t, tt.args...)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d stdout=%s stderr=%s", code, tt.wantCode, stdout, stderr)
			}
			if tt.wantStdout != "" && !strings.Contains(stdout, tt.wantStdout) {
				t.Fatalf("stdout missing %q:\n%s", tt.wantStdout, stdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr, tt.wantStderr) {
				t.Fatalf("stderr missing %q:\n%s", tt.wantStderr, stderr)
			}
		})
	}
}

func TestDoctorExitCodes(t *testing.T) {
	tests := []struct {
		name       string
		crawlers   []fakeCrawler
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name: "success",
			crawlers: []fakeCrawler{{
				name:     "imsgcrawl",
				metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"Messages"}`,
				doctor:   `{"checks":[{"id":"source_store","state":"ok"}]}`,
			}},
			args:       []string{"doctor"},
			wantCode:   0,
			wantStdout: "imessage  source_store",
		},
		{
			name: "failing check",
			crawlers: []fakeCrawler{{
				name:     "imsgcrawl",
				metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"Messages"}`,
				doctor:   failingDoctorJSON(),
			}},
			args:       []string{"doctor"},
			wantCode:   3,
			wantStdout: "tcc_full_disk_access",
			wantStderr: "imessage tcc_full_disk_access failed. Remedy: grant Full Disk Access to Trawl in System Settings > Privacy",
		},
		{
			name:       "requested source missing",
			args:       []string{"doctor", "missing"},
			wantCode:   1,
			wantStderr: `Source "missing" was not found.`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := writeFakeCrawlers(t, tt.crawlers...)
			t.Setenv("PATH", binDir)
			t.Setenv("HOME", t.TempDir())
			stdout, stderr, code := runCLI(t, tt.args...)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d stdout=%s stderr=%s", code, tt.wantCode, stdout, stderr)
			}
			if tt.wantStdout != "" && !strings.Contains(stdout, tt.wantStdout) {
				t.Fatalf("stdout missing %q:\n%s", tt.wantStdout, stdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr, tt.wantStderr) {
				t.Fatalf("stderr missing %q:\n%s", tt.wantStderr, stderr)
			}
		})
	}
}

func TestJSONErrorAndSanitisedStatus(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"Messages"}`,
		status:   `{"app_id":"imessage","state":"ok","auth":{"authorized":true,"token":"should-not-leak"},"token":"should-not-leak"}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "status", "--json")
	if code != 0 {
		t.Fatalf("status --json code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "should-not-leak") {
		t.Fatalf("status JSON leaked unknown fields:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"authorized":true`) {
		t.Fatalf("status JSON dropped safe auth state:\n%s", stdout)
	}

	t.Setenv("PATH", t.TempDir())
	stdout, stderr, code = runCLI(t, "--json", "status", "missing")
	if code != 1 {
		t.Fatalf("missing source code = %d", code)
	}
	if stderr != "" {
		t.Fatalf("JSON error wrote stderr: %s", stderr)
	}
	var payload ErrorEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("error JSON = %s err=%v", stdout, err)
	}
	if payload.Error.Code != "source_not_found" {
		t.Fatalf("error code = %q", payload.Error.Code)
	}
}
