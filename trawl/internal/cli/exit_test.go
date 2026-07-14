package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	"google.golang.org/protobuf/encoding/protojson"
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
				metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
				status:   statusJSON("imessage", "ok"),
			}},
			args:       []string{"status"},
			wantCode:   0,
			wantStdout: "Messages  ok",
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
					metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
					status:   statusJSON("imessage", "ok"),
				},
				{
					name:       "telecrawl",
					metadata:   `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
					status:     `not-json`,
					statusExit: 0,
				},
			},
			args:       []string{"status"},
			wantCode:   3,
			wantStdout: "Telegram  error",
			wantStderr: "Telegram status failed:",
		},
		{
			// A crawler whose metadata does not parse still surfaces the
			// canonical id, never the pre-rename binary name it happens to
			// self-report (TRAWL-147/194 leak: a table row saying "iMessage"
			// next to an error line saying "imsgcrawl status failed").
			name: "all failed",
			crawlers: []fakeCrawler{{
				name:     "telecrawl",
				metadata: `not-json`,
			}},
			args:       []string{"status"},
			wantCode:   1,
			wantStdout: "telegram  error",
			wantStderr: "telegram status failed:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := writeFakeCrawlers(t, tt.crawlers...)
			t.Setenv("PATH", binDir)
			t.Setenv("HOME", syntheticHome(t))
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
				metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
				doctor:   `{"checks":[{"id":"source_store","state":"ok"}]}`,
			}},
			args:       []string{"doctor"},
			wantCode:   0,
			wantStdout: "Messages  ok     1 check: source store",
		},
		{
			name: "failing check",
			crawlers: []fakeCrawler{{
				name:     "imsgcrawl",
				metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
				doctor:   failingDoctorJSON(),
			}},
			args:       []string{"doctor"},
			wantCode:   3,
			wantStdout: "  Remedy: grant Full Disk Access to Trawl in System Settings > Privacy",
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
			t.Setenv("HOME", syntheticHome(t))
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

func TestStatusRendersUniformUnsyncedSummary(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "wacrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"whatsapp","display_name":"WhatsApp"}`,
			status:   `{"app_id":"whatsapp","state":"missing","summary":"WhatsApp archive is empty."}`,
		},
		fakeCrawler{
			name:     "calcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"calendar","display_name":"Calendar"}`,
			status:   `{"app_id":"calendar","state":"error","summary":"Calendar has never synced."}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, _, code := runCLI(t, "status")
	if code != 1 {
		t.Fatalf("status code = %d stdout=%s", code, stdout)
	}
	if count := strings.Count(stdout, "Not synced yet."); count != 2 {
		t.Fatalf("status output did not normalise both unsynced summaries:\n%s", stdout)
	}
}

func TestJSONErrorAndSanitisedStatus(t *testing.T) {
	status := control.NewStatus("imessage", "Archive is fresh.")
	status.State = "ok"
	status.LastSyncAt = "2026-07-02T14:03:00Z"
	status.Freshness = &control.Freshness{Status: "fresh", AgeSeconds: 120}
	status.Counts = []control.Count{
		control.NewCount("messages", "messages", 42),
	}
	statusJSON, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		status:   string(statusJSON),
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "status", "--json")
	if code != 0 {
		t.Fatalf("status --json code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var response federationv1.StatusResponse
	if err := (protojson.UnmarshalOptions{}).Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("status JSON = %s err=%v", stdout, err)
	}
	if len(response.GetSources()) != 1 {
		t.Fatalf("sources = %#v", response.GetSources())
	}
	got := response.GetSources()[0]
	if got.GetAppId() != "imessage" || got.GetState() != "ok" || got.GetSummary() != "Archive is fresh." {
		t.Fatalf("status = %#v", got)
	}
	if got.GetLastSyncRfc3339() != "2026-07-02T14:03:00Z" || got.GetFreshness().GetStatus() != "fresh" || got.GetFreshness().GetAgeSeconds() != 120 {
		t.Fatalf("freshness fields = %#v", got)
	}
	if len(got.GetCounts()) != 1 || got.GetCounts()[0].GetId() != "messages" || got.GetCounts()[0].GetValue() != 42 {
		t.Fatalf("counts = %#v", got.Counts)
	}
	for _, want := range []string{`"failures":[]`, `"skipped_sources":[]`, `"databases":[]`, `"warnings":[]`} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("canonical JSON omitted %s: %s", want, stdout)
		}
	}

	t.Setenv("PATH", t.TempDir())
	stdout, stderr, code = runCLI(t, "--json", "status", "missing")
	if code != 1 {
		t.Fatalf("missing source code = %d", code)
	}
	if stderr != "" {
		t.Fatalf("JSON error wrote stderr: %s", stderr)
	}
	var payload federationv1.StatusResponse
	if err := (protojson.UnmarshalOptions{}).Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("error JSON = %s err=%v", stdout, err)
	}
	if payload.GetOutcome() != federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED || len(payload.GetFailures()) != 1 || payload.GetFailures()[0].GetCode() != federationv1.FailureCode_FAILURE_CODE_NOT_FOUND {
		t.Fatalf("failure response = %#v", &payload)
	}
}
