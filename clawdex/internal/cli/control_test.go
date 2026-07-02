package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/openclaw/crawlkit/control"
)

func TestExecuteMetadataJSON(t *testing.T) {
	cfg, _ := testPaths(t)
	var out, errOut bytes.Buffer
	missingRepo := filepath.Join(t.TempDir(), "missing")
	if err := Execute([]string{"--config", cfg, "--repo", missingRepo, "metadata", "--json"}, &out, &errOut); err != nil {
		t.Fatalf("metadata: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}

	var manifest control.Manifest
	if err := json.Unmarshal(out.Bytes(), &manifest); err != nil {
		t.Fatalf("manifest json = %s err=%v", out.String(), err)
	}
	if manifest.ID != "clawdex" || manifest.DisplayName != "Contacts" {
		t.Fatalf("manifest identity = %#v", manifest)
	}
	if !reflect.DeepEqual(manifest.Capabilities, []string{"status", "doctor"}) {
		t.Fatalf("capabilities = %#v", manifest.Capabilities)
	}
	if _, ok := manifest.Commands["contact-export"]; ok {
		t.Fatalf("unexpected contact-export command = %#v", manifest.Commands)
	}
	for name, argv := range map[string][]string{
		"metadata": {"clawdex", "metadata", "--json"},
		"status":   {"clawdex", "status", "--json"},
		"doctor":   {"clawdex", "doctor", "--json"},
	} {
		got := manifest.Commands[name]
		if !reflect.DeepEqual(got.Argv, argv) || !got.JSON {
			t.Fatalf("%s command = %#v", name, got)
		}
	}

	var payload struct {
		control.Manifest
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Version != Version {
		t.Fatalf("version = %q, want %q", payload.Version, Version)
	}
}

func TestExecuteStatusJSONStates(t *testing.T) {
	cfg, data := testPaths(t)
	missingRepo := filepath.Join(t.TempDir(), "missing")
	var out, errOut bytes.Buffer

	if err := Execute([]string{"--config", cfg, "--repo", missingRepo, "status", "--json"}, &out, &errOut); err != nil {
		t.Fatalf("status missing: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	status := decodeStatus(t, out.Bytes())
	if status.State != "missing" || countValue(status.Counts, "people") != 0 || status.Freshness != nil {
		t.Fatalf("missing status = %#v", status)
	}

	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatalf("init: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "status", "--json"}, &out, &errOut); err != nil {
		t.Fatalf("status empty: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	status = decodeStatus(t, out.Bytes())
	if status.State != "empty" || countValue(status.Counts, "people") != 0 || hasCount(status.Counts, "sources") {
		t.Fatalf("empty status = %#v", status)
	}

	telecrawl := writeFakeContactCrawler(t, "telecrawl", `{"contacts":[{"display_name":"Riley Stone","phone_numbers":["+1555010100"]}]}`)
	wacrawl := writeFakeContactCrawler(t, "wacrawl", `{"contacts":[{"display_name":"Riley Stone","phone_numbers":["+1555010100"]}]}`)
	for _, crawler := range []string{telecrawl, wacrawl} {
		out.Reset()
		errOut.Reset()
		if err := Execute([]string{"--config", cfg, "import", "contacts", "--from", crawler}, &out, &errOut); err != nil {
			t.Fatalf("import contacts from %s: %v stderr=%s stdout=%s", crawler, err, errOut.String(), out.String())
		}
	}
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "status", "--json"}, &out, &errOut); err != nil {
		t.Fatalf("status after staged imports: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	status = decodeStatus(t, out.Bytes())
	if status.State != "empty" || countValue(status.Counts, "people") != 0 || hasCount(status.Counts, "sources") {
		t.Fatalf("staged-only status = %#v", status)
	}
	stage, err := os.ReadFile(filepath.Join(data, "index", "unmatched.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(stage), `name="Riley Stone"`) != 2 {
		t.Fatalf("unmatched staging = %s", stage)
	}
	if status.Freshness != nil {
		t.Fatalf("freshness should be omitted: %#v", status.Freshness)
	}
}

func TestExecuteStatusHumanOutputIsProse(t *testing.T) {
	cfg, _ := testPaths(t)
	var out, errOut bytes.Buffer
	missingRepo := filepath.Join(t.TempDir(), "missing")
	if err := Execute([]string{"--config", cfg, "--repo", missingRepo, "status"}, &out, &errOut); err != nil {
		t.Fatalf("status human: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	assertHumanProseOutput(t, out.String(),
		"Status: missing",
		"contacts repo not initialised",
		"Counts:",
		"people: 0",
		"Paths:",
		"Config:",
		"Contacts repo:",
	)
}

func TestExecuteDoctorJSONMissingHealthyAndCorrupt(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer

	if err := Execute([]string{"--config", cfg, "--repo", filepath.Join(t.TempDir(), "missing"), "doctor", "--json"}, &out, &errOut); err != nil {
		t.Fatalf("doctor missing: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	report := decodeDoctorReport(t, out.Bytes())
	if len(report.Checks) != 3 || report.Checks[0].ID != "config" || report.Checks[1].ID != "contacts_repo" || report.Checks[2].ID != "index" {
		t.Fatalf("missing checks = %#v", report.Checks)
	}
	if report.Checks[1].State != "fail" || !strings.Contains(report.Checks[1].Remedy, "clawdex init") {
		t.Fatalf("contacts_repo check = %#v", report.Checks[1])
	}
	if report.Checks[2].State != "fail" || !strings.Contains(report.Checks[2].Message, "without a contacts repo") {
		t.Fatalf("index check = %#v", report.Checks[2])
	}

	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "person", "add", "Morgan Healthy", "--email", "morgan@example.com"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "doctor", "--json"}, &out, &errOut); err != nil {
		t.Fatalf("doctor healthy: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	report = decodeDoctorReport(t, out.Bytes())
	for _, check := range report.Checks {
		if check.State != "ok" {
			t.Fatalf("healthy check = %#v in %#v", check, report.Checks)
		}
	}

	personPath := filepath.Join(data, "people", "morgan-healthy", "person.md")
	if err := os.WriteFile(personPath, []byte("---\nid: person_x\nname: Morgan Healthy\ntags: [broken\n---\n# Morgan Healthy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "doctor", "--json"}, &out, &errOut); err != nil {
		t.Fatalf("doctor corrupt: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	report = decodeDoctorReport(t, out.Bytes())
	if report.Checks[1].State != "fail" || !strings.Contains(report.Checks[1].Remedy, "doctor --repair") {
		t.Fatalf("corrupt contacts_repo check = %#v", report.Checks[1])
	}
}

func TestExecuteDoctorRepairKeepsLegacyReport(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"--config", cfg, "person", "add", "Taylor Repair", "--email", "taylor@example.com"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	personPath := filepath.Join(data, "people", "taylor-repair", "person.md")
	if err := os.WriteFile(personPath, []byte("---\nid: person_x\nname: Taylor Repair\ntags: [broken\n---\n# Taylor Repair\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "--dry-run", "doctor", "--repair"}, &out, &errOut); err != nil {
		t.Fatalf("plain repair: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "repaired: 1") || strings.Contains(out.String(), "checks") {
		t.Fatalf("plain repair output = %s", out.String())
	}

	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "--dry-run", "doctor", "--repair", "--json"}, &out, &errOut); err != nil {
		t.Fatalf("json repair: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("repair json = %s err=%v", out.String(), err)
	}
	if _, ok := payload["checks"]; ok {
		t.Fatalf("repair json should keep legacy shape: %#v", payload)
	}
	if got := payload["repaired"]; got != float64(1) {
		t.Fatalf("repaired = %#v", got)
	}
}

func decodeStatus(t *testing.T, data []byte) control.Status {
	t.Helper()
	var status control.Status
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("status json = %s err=%v", string(data), err)
	}
	return status
}

func decodeDoctorReport(t *testing.T, data []byte) DoctorReport {
	t.Helper()
	var report DoctorReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("doctor json = %s err=%v", string(data), err)
	}
	return report
}

func countValue(counts []control.Count, id string) int64 {
	for _, count := range counts {
		if count.ID == id {
			return count.Value
		}
	}
	return -1
}

func hasCount(counts []control.Count, id string) bool {
	for _, count := range counts {
		if count.ID == id {
			return true
		}
	}
	return false
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
