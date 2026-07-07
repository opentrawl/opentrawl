package birdcrawl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/control"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

func TestGeneratedManifestListsRunnerVerbs(t *testing.T) {
	stateRoot := t.TempDir()
	out := runBirdcrawl(t, stateRoot, "metadata", "--json")
	var manifest control.Manifest
	if err := json.Unmarshal(out, &manifest); err != nil {
		t.Fatal(err)
	}
	wantCommands := []string{
		"bookmarks",
		"doctor",
		"import_archive",
		"likes",
		"mentions",
		"metadata",
		"open",
		"search",
		"spend",
		"stats",
		"status",
		"sync",
		"tweets",
	}
	gotCommands := mapKeys(manifest.Commands)
	if !equalStrings(gotCommands, wantCommands) {
		t.Fatalf("commands = %v, want %v", gotCommands, wantCommands)
	}
	wantCaps := []string{
		"bookmarks",
		"doctor",
		"import_archive",
		"likes",
		"mentions",
		"metadata",
		"open",
		"search",
		"short_refs",
		"spend",
		"stats",
		"status",
		"sync",
		"tweets",
	}
	gotCaps := append([]string(nil), manifest.Capabilities...)
	sort.Strings(gotCaps)
	sort.Strings(wantCaps)
	if !equalStrings(gotCaps, wantCaps) {
		t.Fatalf("capabilities = %v, want %v", gotCaps, wantCaps)
	}
	wantConfig := filepath.Join(stateRoot, "birdcrawl", "config.toml")
	if manifest.Paths.DefaultConfig != wantConfig {
		t.Fatalf("default config = %q, want %q", manifest.Paths.DefaultConfig, wantConfig)
	}
	if manifest.Paths.ConfigEnv != "" {
		t.Fatalf("config env survived in manifest: %q", manifest.Paths.ConfigEnv)
	}
	if _, ok := manifest.Commands["version"]; ok {
		t.Fatal("version command survived in manifest")
	}
}

func TestSpendFiguresReachable(t *testing.T) {
	stateRoot := t.TempDir()
	month := time.Now().UTC().Format("2006-01")
	seedSpend(t, stateRoot, month, 2_500_000)
	out := runBirdcrawl(t, stateRoot, "spend", "--json")
	var got spendEnvelope
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Month != month {
		t.Fatalf("month = %q, want %q", got.Month, month)
	}
	if got.SpentUSD != "2.50" || got.MonthlyBudgetUSD != "10.00" || got.RemainingUSD != "7.50" {
		t.Fatalf("spend = %#v, want spent 2.50 cap 10.00 remaining 7.50", got)
	}
}

func TestHandlerUsageErrorExitsTwo(t *testing.T) {
	result := runBirdcrawlRaw(t, t.TempDir(), "import", "archive")
	if result.code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", result.code, result.stdout, result.stderr)
	}
	if !strings.Contains(result.stderr, "import archive takes exactly one path") {
		t.Fatalf("stderr missing usage error:\n%s", result.stderr)
	}
}

func TestDirectVersionVerbRejected(t *testing.T) {
	result := runBirdcrawlRaw(t, t.TempDir(), "version")
	if result.code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", result.code, result.stdout, result.stderr)
	}
	if !strings.Contains(result.stderr, `unknown verb "version"`) {
		t.Fatalf("stderr missing rejected version verb:\n%s", result.stderr)
	}
}

func TestRunnerConfigPathAcceptsExistingBudgetShape(t *testing.T) {
	stateRoot := t.TempDir()
	base := filepath.Join(stateRoot, "birdcrawl")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(base, "config.toml")
	if err := os.WriteFile(configPath, []byte("monthly_budget_usd = \"10\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runBirdcrawl(t, stateRoot, "status", "--json")
	var status control.Status
	if err := json.Unmarshal(out, &status); err != nil {
		t.Fatal(err)
	}
	if status.ConfigPath != configPath {
		t.Fatalf("config path = %q, want %q", status.ConfigPath, configPath)
	}
}

type birdcrawlResult struct {
	stdout []byte
	stderr string
	code   int
}

func runBirdcrawl(t *testing.T, stateRoot string, args ...string) []byte {
	t.Helper()
	result := runBirdcrawlRaw(t, stateRoot, args...)
	if result.code != 0 {
		t.Fatalf("birdcrawl %v exited %d\nstdout:\n%s\nstderr:\n%s", args, result.code, result.stdout, result.stderr)
	}
	return result.stdout
}

func runBirdcrawlRaw(t *testing.T, stateRoot string, args ...string) birdcrawlResult {
	t.Helper()
	binary := buildBirdcrawl(t)
	allArgs := append([]string{"--state-root", stateRoot}, args...)
	cmd := exec.Command(binary, allArgs...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return birdcrawlResult{stdout: stdout.Bytes(), stderr: stderr.String(), code: exitErr.ExitCode()}
		}
		t.Fatalf("birdcrawl %v: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
	}
	return birdcrawlResult{stdout: stdout.Bytes(), stderr: stderr.String()}
}

func buildBirdcrawl(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "birdcrawl")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/birdcrawl")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		t.Fatalf("build birdcrawl: %v\nstderr:\n%s", err, stderr.String())
	}
	return binary
}

func seedSpend(t *testing.T, stateRoot, month string, micros int64) {
	t.Helper()
	base := filepath.Join(stateRoot, "birdcrawl")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "config.toml"), []byte("monthly_budget_usd = \"10\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(base, "birdcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	at := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	if err := st.AddSpend(context.Background(), month, micros, at); err != nil {
		t.Fatal(err)
	}
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
