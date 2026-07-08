package clawdex

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/openclaw/clawdex/internal/model"
	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/control"
)

var runMu sync.Mutex

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == crawlkit.HiddenWireSubcommand {
		os.Exit(crawlkit.Run(os.Args[1:], []crawlkit.Crawler{New()}))
	}
	os.Exit(m.Run())
}

func TestMetadataManifestGeneratedByRunner(t *testing.T) {
	home := testHome(t)
	code, stdout, stderr := runContacts(t, home, "metadata", "--json")
	if code != 0 {
		t.Fatalf("metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var manifest control.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("manifest JSON: %v\n%s", err, stdout)
	}
	if manifest.SchemaVersion != control.RunnerManifestVersion || manifest.ID != appID {
		t.Fatalf("manifest = %#v", manifest)
	}
	wantCommands := []string{"config", "contacts_export", "doctor", "export_vcard", "git", "import", "init", "metadata", "open", "person_list", "person_show", "repair", "search", "status", "sync_apple", "sync_google", "who"}
	if got := sortedKeys(manifest.Commands); !equalStrings(got, wantCommands) {
		t.Fatalf("commands = %v, want %v", got, wantCommands)
	}
	if got := manifest.Commands["contacts_export"].Store; got != "none" {
		t.Fatalf("contacts_export store = %q", got)
	}
	if got := manifest.Commands["repair"]; !got.Mutates || got.Store != "none" {
		t.Fatalf("repair command = %#v", got)
	}
	if got := manifest.Commands["sync_apple"]; got.Mutates || got.Store != "none" {
		t.Fatalf("sync_apple command = %#v", got)
	}
}

func TestRunnerCommandsAgainstSyntheticArchive(t *testing.T) {
	home := testHome(t)
	if code, stdout, stderr := runContacts(t, home, "init", "--json"); code != 0 {
		t.Fatalf("init code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	input := filepath.Join(home, "apple.ndjson")
	if err := os.WriteFile(input, []byte(`{"identifier":"a1","full_name":"Ada Example","emails":["ada@example.com"],"phones":["+1 555 0100"]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := runContacts(t, home, "import", "apple", "--input", input, "--json"); code != 0 {
		t.Fatalf("import code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr := runContacts(t, home, "search", "Ada", "--json")
	if code != 0 {
		t.Fatalf("search code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var search struct {
		Results []crawlkit.Hit `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &search); err != nil {
		t.Fatalf("search JSON: %v\n%s", err, stdout)
	}
	if len(search.Results) != 1 || search.Results[0].Who != "Ada Example" {
		t.Fatalf("search = %#v", search)
	}
	code, stdout, stderr = runContacts(t, home, "who", "Ada", "--json")
	if code != 0 {
		t.Fatalf("who code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `"who": "Ada Example"`) {
		t.Fatalf("who stdout = %s", stdout)
	}
	code, stdout, stderr = runContacts(t, home, "person", "list", "--json")
	if code != 0 {
		t.Fatalf("person list code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var people peopleEnvelope
	if err := json.Unmarshal([]byte(stdout), &people); err != nil {
		t.Fatalf("people JSON: %v\n%s", err, stdout)
	}
	if len(people.People) != 1 || people.People[0].Name != "Ada Example" {
		t.Fatalf("people = %#v", people)
	}
	ref := "contacts:person/" + people.People[0].ID
	code, stdout, stderr = runContacts(t, home, "open", ref, "--json")
	if code != 0 {
		t.Fatalf("open code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var person model.Person
	if err := json.Unmarshal([]byte(stdout), &person); err != nil {
		t.Fatalf("open JSON: %v\n%s", err, stdout)
	}
	if person.Name != "Ada Example" {
		t.Fatalf("person = %#v", person)
	}
	code, stdout, stderr = runContacts(t, home, "contacts", "contacts", "export", "--json")
	if code != 0 {
		t.Fatalf("contacts export code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var export control.ContactExport
	if err := json.Unmarshal([]byte(stdout), &export); err != nil {
		t.Fatalf("contacts JSON: %v\n%s", err, stdout)
	}
	if len(export.Contacts) != 1 || export.Contacts[0].PhoneNumbers[0] != "+1 555 0100" {
		t.Fatalf("contacts = %#v", export)
	}
}

func TestRepairIsMutatingVerbAndDoctorRepairFlagIsGone(t *testing.T) {
	home := testHome(t)
	if code, stdout, stderr := runContacts(t, home, "init", "--json"); code != 0 {
		t.Fatalf("init code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	input := filepath.Join(home, "apple.ndjson")
	if err := os.WriteFile(input, []byte(`{"identifier":"a1","full_name":"Ada Example","emails":["ada@example.com"],"phones":["+1 555 0100"]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := runContacts(t, home, "import", "apple", "--input", input, "--json"); code != 0 {
		t.Fatalf("import code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr := runContacts(t, home, "repair", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("repair code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `"id": "contacts_repo"`) || !strings.Contains(stdout, `"id": "markdown_repair"`) {
		t.Fatalf("repair stdout = %s", stdout)
	}

	code, stdout, stderr = runContacts(t, home, "doctor", "--repair", "--json")
	if code != 2 || stderr != "" || !strings.Contains(stdout, "doctor takes no arguments") {
		t.Fatalf("doctor --repair code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestSyncPreviewVerbsPreserveLegacyOutput(t *testing.T) {
	home := testHome(t)
	if code, stdout, stderr := runContacts(t, home, "sync", "apple", "--json"); code != 0 {
		t.Fatalf("sync apple code=%d stdout=%s stderr=%s", code, stdout, stderr)
	} else if !strings.Contains(stdout, `"dry_run": true`) || !strings.Contains(stdout, "use import apple") {
		t.Fatalf("sync apple stdout = %s", stdout)
	}

	if code, stdout, stderr := runContacts(t, home, "sync", "google", "--account", "ada@example.com", "--json"); code != 0 {
		t.Fatalf("sync google code=%d stdout=%s stderr=%s", code, stdout, stderr)
	} else if !strings.Contains(stdout, `"account": "ada@example.com"`) || !strings.Contains(stdout, "use import google") {
		t.Fatalf("sync google stdout = %s", stdout)
	}
}

func TestContactsRepoEnvOverridesConfig(t *testing.T) {
	home := testHome(t)
	configRepo := filepath.Join(home, "config-repo")
	envRepo := filepath.Join(home, "env-repo")
	if code, stdout, stderr := runContacts(t, home, "init", configRepo, "--json"); code != 0 {
		t.Fatalf("init config repo code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	t.Setenv("CONTACTS_REPO", envRepo)
	if code, stdout, stderr := runContacts(t, home, "init", envRepo, "--no-config", "--json"); code != 0 {
		t.Fatalf("init env repo code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr := runContacts(t, home, "status", "--json")
	if code != 0 {
		t.Fatalf("status code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `"database_path": "`+envRepo+`"`) {
		t.Fatalf("status stdout = %s", stdout)
	}
}

func TestImportContactsFromCrawlerIsRetired(t *testing.T) {
	home := testHome(t)
	if code, stdout, stderr := runContacts(t, home, "init", "--json"); code != 0 {
		t.Fatalf("init code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr := runContacts(t, home, "import", "contacts", "--json")
	if code != 2 {
		t.Fatalf("import contacts code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "import contacts from crawler binaries has been removed") {
		t.Fatalf("stdout = %s", stdout)
	}
}

func testHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func runContacts(t *testing.T, home string, args ...string) (int, string, string) {
	t.Helper()
	runMu.Lock()
	defer runMu.Unlock()
	t.Setenv("HOME", home)
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
	var stdout, stderr bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stdout, stdoutR)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stderr, stderrR)
	}()
	os.Stdout = stdoutW
	os.Stderr = stderrW
	code := crawlkit.Run(args, []crawlkit.Crawler{New()})
	_ = stdoutW.Close()
	_ = stderrW.Close()
	wg.Wait()
	os.Stdout = oldStdout
	os.Stderr = oldStderr
	_ = stdoutR.Close()
	_ = stderrR.Close()
	return code, stdout.String(), stderr.String()
}

func sortedKeys(commands map[string]control.Command) []string {
	keys := make([]string, 0, len(commands))
	for key := range commands {
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
