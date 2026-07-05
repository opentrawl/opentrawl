package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// namespaceManifest is an iMessage-shaped manifest: verbs whose invocation
// matches the child token (chats, search) plus one whose key differs from
// the tokens the user types (contact-export -> "contacts export").
const namespaceManifest = `{"schema_version":1,"contract_version":1,"id":"imsgcrawl","display_name":"iMessage","description":"Local-first iMessage archive crawler.","binary":{"name":"imsgcrawl"},"capabilities":["chats","search"],"commands":{"chats":{"title":"Chats","argv":["imsgcrawl","chats","--json"],"json":true},"search":{"title":"Search","argv":["imsgcrawl","search","QUERY","--json"],"json":true},"contact-export":{"title":"Export contacts","argv":["imsgcrawl","contacts","export","--json"],"json":true},"raw":{"title":"Raw","argv":["imsgcrawl","raw"],"json":false}}}`

// writeNamespaceCrawler installs a fake imsgcrawl that reports the manifest
// on `metadata --json` and echoes its args for any other verb, so a test
// can assert exactly what trawl passed through to the child.
func writeNamespaceCrawler(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = \"metadata\" ]; then\n  printf '%s\\n' '" + namespaceManifest + "'\n  exit 0\nfi\nprintf 'child: %s\\n' \"$*\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "imsgcrawl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func setupNamespace(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", writeNamespaceCrawler(t))
	t.Setenv("HOME", t.TempDir())
}

func TestNamespaceListingHuman(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage")
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	for _, want := range []string{
		"iMessage — Local-first iMessage archive crawler.",
		"Verbs:",
		"chats",
		"contacts export",
		"search QUERY",
		"Export contacts",
		"Run a verb: trawl imessage <verb>",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("listing missing %q:\n%s", want, stdout)
		}
	}
}

func TestNamespaceListingJSON(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "--json")
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr)
	}
	var got namespaceListing
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	if got.Source != "imsgcrawl" || got.Surface != "iMessage" {
		t.Fatalf("listing header = %#v", got)
	}
	if len(got.Verbs) != 4 || got.Verbs[0].Verb != "chats" || got.Verbs[1].Verb != "contacts export" || got.Verbs[2].Verb != "raw" || got.Verbs[3].Verb != "search QUERY" {
		t.Fatalf("verbs = %#v", got.Verbs)
	}
}

func TestNamespaceVerbPassthrough(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "chats", "--limit", "5")
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != "child: chats --limit 5" {
		t.Fatalf("passthrough stdout = %q", stdout)
	}
}

func TestNamespaceJSONInjectedBeforeSource(t *testing.T) {
	setupNamespace(t)
	// --json sits before the source token, so trawl must forward it.
	stdout, stderr, code := runCLI(t, "--json", "imessage", "search", "falafel")
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != "child: search falafel --json" {
		t.Fatalf("passthrough stdout = %q", stdout)
	}
}

// A trawl global flag placed between the source and the verb must not hide
// the verb — the agent JSON path relies on it.
func TestNamespaceGlobalFlagBeforeVerb(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "--json", "chats", "--limit", "5")
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	// --json already present in rest, so it is not re-appended.
	if strings.TrimSpace(stdout) != "child: --json chats --limit 5" {
		t.Fatalf("passthrough stdout = %q", stdout)
	}
}

// A child flag before the verb is an unsupported shape; the error names
// the shape, never the flag's value, and never spawns the child.
func TestNamespaceChildFlagBeforeVerb(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "--archive", "/tmp/x", "chats")
	if code == 0 {
		t.Fatalf("child flag before verb should fail: stdout=%s", stdout)
	}
	if !strings.Contains(stderr, "needs the verb first") {
		t.Fatalf("stderr should name the shape:\n%s", stderr)
	}
	if strings.Contains(stderr, "/tmp/x") || strings.Contains(stdout, "child:") {
		t.Fatalf("named the flag value or spawned the child:\nout=%s err=%s", stdout, stderr)
	}
}

func TestNamespaceUnknownVerb(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "bogus")
	if code == 0 {
		t.Fatalf("unknown verb should fail: stdout=%s", stdout)
	}
	if !strings.Contains(stderr, "has no verb") {
		t.Fatalf("stderr missing unknown-verb message:\n%s", stderr)
	}
	if strings.Contains(stdout, "child:") {
		t.Fatalf("child was spawned for an unknown verb:\n%s", stdout)
	}
}

// An incomplete multi-word verb ("contacts" without "export") must not
// reach the child — trawl owns the error, no module name leaks.
func TestNamespaceIncompleteMultiWordVerb(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "contacts")
	if code == 0 {
		t.Fatalf("incomplete verb should fail: stdout=%s", stdout)
	}
	if !strings.Contains(stderr, "has no verb") {
		t.Fatalf("stderr missing unknown-verb message:\n%s", stderr)
	}
	if strings.Contains(stdout, "child:") {
		t.Fatalf("child was spawned for an incomplete verb:\n%s", stdout)
	}
}

// A single user -v after the verb must reach the child once, not doubled
// into -vv by a separate injection.
func TestNamespaceVerbosePassthroughNotDoubled(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "chats", "-v")
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != "child: chats -v" {
		t.Fatalf("passthrough stdout = %q, want single -v", stdout)
	}
}

// Global --json is forwarded only to verbs that declare they emit JSON.
func TestNamespaceJSONNotInjectedForNonJSONVerb(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "--json", "imessage", "raw")
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != "child: raw" {
		t.Fatalf("passthrough stdout = %q, want no --json", stdout)
	}
}

// The full multi-word verb reaches the child intact.
func TestNamespaceMultiWordVerbPassthrough(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "contacts", "export")
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != "child: contacts export" {
		t.Fatalf("passthrough stdout = %q", stdout)
	}
}
