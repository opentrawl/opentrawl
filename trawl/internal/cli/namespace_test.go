package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// namespaceManifest is an iMessage-shaped manifest: verbs whose invocation
// matches the user token (pins, search) plus one whose key differs from the
// tokens the user types (thread-export -> "threads export"). pins stands in
// for any bespoke single-token verb; chats is now a reserved spine verb, so a
// bespoke command may not use that key.
const namespaceManifest = `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"iMessage","binary":{"name":"imsgcrawl"},"capabilities":["pins","search","sync","doctor"],"commands":{"pins":{"title":"Pins","argv":["imsgcrawl","pins","--json"],"json":true},"search":{"title":"Search","argv":["imsgcrawl","search","QUERY","--json"],"json":true},"sync":{"title":"Sync","argv":["imsgcrawl","sync","--json"],"json":true,"mutates":true},"doctor":{"title":"Diagnostics","argv":["imsgcrawl","doctor","--json"],"json":true},"thread-export":{"title":"Export threads","argv":["imsgcrawl","threads","export","--json"],"json":true},"raw":{"title":"Raw","argv":["imsgcrawl","raw"],"json":false}}}`

func setupNamespace(t *testing.T) {
	t.Helper()
	writeFakeCrawlers(t, fakeCrawler{name: "imsgcrawl", metadata: namespaceManifest})
	t.Setenv("HOME", syntheticHome(t))
}

func TestNamespaceListingHuman(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage")
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	for _, want := range []string{
		"iMessage",
		"Verbs:",
		"pins",
		"threads export",
		"search QUERY",
		"Export threads",
		"Run a verb: trawl imessage <verb>",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("listing missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "doctor") || strings.Contains(stdout, "Diagnostics") || strings.Contains(stdout, "sync") {
		t.Fatalf("listing exposed a root-owned command:\n%s", stdout)
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
	if got.Source != "imessage" || got.Surface != "iMessage" {
		t.Fatalf("listing header = %#v", got)
	}
	verbs := map[string]bool{}
	for _, verb := range got.Verbs {
		verbs[verb.Verb] = true
	}
	for _, want := range []string{"pins", "raw", "search QUERY", "threads export"} {
		if !verbs[want] {
			t.Fatalf("verbs missing %q: %#v", want, got.Verbs)
		}
	}
	if len(got.Verbs) < 4 {
		t.Fatalf("verbs = %#v", got.Verbs)
	}
	if verbs["doctor"] {
		t.Fatalf("JSON listing exposed removed diagnostics navigation: %#v", got.Verbs)
	}
	if verbs["sync"] {
		t.Fatalf("JSON listing exposed the root-owned sync command: %#v", got.Verbs)
	}
}

func TestNamespaceVerbPassthrough(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "pins", "--limit", "5")
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != "verb=pins args= limit=5" {
		t.Fatalf("passthrough stdout = %q", stdout)
	}
}

func TestNamespaceDoctorIsNotDispatchable(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "doctor")
	if code != 1 || stdout != "" || !strings.Contains(stderr, `iMessage has no verb "doctor"`) {
		t.Fatalf("removed namespace command code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if strings.Contains(stderr, "trawl doctor") {
		t.Fatalf("removed command was offered as a remedy:\n%s", stderr)
	}
}

func TestNamespaceSyncIsNotDispatchable(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "sync")
	if code != 1 || stdout != "" || !strings.Contains(stderr, `iMessage has no verb "sync"`) {
		t.Fatalf("second sync spelling code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "trawl imessage") {
		t.Fatalf("error did not return to the source read surface:\n%s", stderr)
	}
}

func TestNamespaceJSONInjectedBeforeSource(t *testing.T) {
	setupNamespace(t)
	// --json sits before the source token, so trawl must forward it.
	stdout, stderr, code := runCLI(t, "--json", "imessage", "search", "falafel")
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, `"query": "falafel"`) {
		t.Fatalf("passthrough stdout = %q", stdout)
	}
}

// A trawl global flag placed between the source and the verb must not hide
// the verb — the structured script path relies on it.
func TestNamespaceGlobalFlagBeforeVerb(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "--json", "pins", "--limit", "5")
	if code != 0 {
		t.Fatalf("code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, `"verb": "pins"`) || !strings.Contains(stdout, `"limit": "5"`) {
		t.Fatalf("passthrough stdout = %q", stdout)
	}
}

// A crawler flag before the verb is an unsupported shape; the error names
// the shape, never the flag's value, and never runs the crawler.
func TestNamespaceChildFlagBeforeVerb(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "--archive", "/tmp/x", "pins")
	if code == 0 {
		t.Fatalf("crawler flag before verb should fail: stdout=%s", stdout)
	}
	if !strings.Contains(stderr, "needs the verb first") {
		t.Fatalf("stderr should name the shape:\n%s", stderr)
	}
	if strings.Contains(stderr, "/tmp/x") || strings.Contains(stdout, "verb=pins") {
		t.Fatalf("named the flag value or ran the crawler:\nout=%s err=%s", stdout, stderr)
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
	if strings.Contains(stdout, "verb=") {
		t.Fatalf("crawler ran for an unknown verb:\n%s", stdout)
	}
}

// An incomplete multi-word verb ("threads" without "export") must not
// reach trawlkit — trawl owns the error, no module name leaks.
func TestNamespaceIncompleteMultiWordVerb(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "threads")
	if code == 0 {
		t.Fatalf("incomplete verb should fail: stdout=%s", stdout)
	}
	if !strings.Contains(stderr, "has no verb") {
		t.Fatalf("stderr missing unknown-verb message:\n%s", stderr)
	}
	if strings.Contains(stdout, "verb=") {
		t.Fatalf("crawler ran for an incomplete verb:\n%s", stdout)
	}
}

// A single user -v after the verb is treated as a trawlkit global flag, not
// doubled into -vv by a separate injection.
func TestNamespaceVerbosePassthroughNotDoubled(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "pins", "-v")
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != "verb=pins args= limit=" {
		t.Fatalf("passthrough stdout = %q", stdout)
	}
}

// Generated trawlkit manifests declare bespoke verbs as JSON-capable, so
// the namespace path renders the fake raw verb as JSON when the global flag
// asks for JSON.
func TestNamespaceJSONForBespokeVerb(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "--json", "imessage", "raw")
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, `"verb": "raw"`) {
		t.Fatalf("passthrough stdout = %q", stdout)
	}
}

// The full multi-word verb reaches the crawler intact.
func TestNamespaceMultiWordVerbPassthrough(t *testing.T) {
	setupNamespace(t)
	stdout, stderr, code := runCLI(t, "imessage", "threads", "export")
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != "verb=threads export args= limit=" {
		t.Fatalf("passthrough stdout = %q", stdout)
	}
}
