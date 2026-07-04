package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/conformance"
	"github.com/openclaw/crawlkit/render"
)

type statusJSON struct {
	AppID     string `json:"app_id"`
	State     string `json:"state"`
	Summary   string `json:"summary"`
	Freshness struct {
		LastSync string `json:"last_sync"`
	} `json:"freshness"`
	Counts []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Value int64  `json:"value"`
	} `json:"counts"`
	Auth struct {
		Authorized bool    `json:"authorized"`
		Expires    *string `json:"expires"`
	} `json:"auth"`
}

type doctorCheckJSON struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message"`
	Remedy  string `json:"remedy"`
}

type searchJSON struct {
	Query        string             `json:"query"`
	WhoResolved  *testWhoResolved   `json:"who_resolved"`
	Results      []testSearchResult `json:"results"`
	TotalMatches int                `json:"total_matches"`
	Truncated    bool               `json:"truncated"`
}

type testSearchResult struct {
	Ref      string `json:"ref"`
	ShortRef string `json:"short_ref"`
	Time     string `json:"time"`
	Who      string `json:"who"`
	Where    string `json:"where"`
	Snippet  string `json:"snippet"`
}

type testWhoResolved struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
}

type whoJSON struct {
	Query      string             `json:"query"`
	Candidates []testWhoCandidate `json:"candidates"`
}

type testWhoCandidate struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
	LastSeen    string   `json:"last_seen"`
	Messages    int      `json:"messages"`
}

type openJSON struct {
	Ref  string `json:"ref"`
	Chat struct {
		Ref  string `json:"ref"`
		Name string `json:"name"`
	} `json:"chat"`
	Message       openMessageJSON   `json:"message"`
	Context       []openMessageJSON `json:"context"`
	ContextWindow struct {
		Before          int  `json:"before"`
		After           int  `json:"after"`
		BeforeTruncated bool `json:"before_truncated"`
		AfterTruncated  bool `json:"after_truncated"`
	} `json:"context_window"`
	TargetPosition int `json:"target_position"`
}

type openMessageJSON struct {
	Ref      string `json:"ref"`
	IsTarget bool   `json:"is_target"`
	Time     string `json:"time"`
	Chat     struct {
		Ref  string `json:"ref"`
		Name string `json:"name"`
	} `json:"chat"`
	Sender struct {
		Ref         string `json:"ref"`
		DisplayName string `json:"display_name"`
	} `json:"sender"`
	Text string `json:"text"`
}

func runCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	if !hasArg(args, "--db") {
		args = append([]string{"--db", filepath.Join(t.TempDir(), "telecrawl.db")}, args...)
	}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), args, &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func useFixedLocalZone(t *testing.T) *time.Location {
	t.Helper()
	loc := time.FixedZone("test-local", 2*60*60)
	previous := time.Local
	time.Local = loc
	t.Cleanup(func() {
		time.Local = previous
	})
	return loc
}

func assertContainsHumanTime(t *testing.T, output string, utc time.Time) {
	t.Helper()
	want := render.ShortLocalTime(utc)
	if !strings.Contains(output, want) && !strings.Contains(output, utc.Local().Format("15:04")) {
		t.Fatalf("output missing human local time %q:\n%s", want, output)
	}
	if utcText := utc.UTC().Format(time.RFC3339); strings.Contains(output, utcText) {
		t.Fatalf("output contains UTC time %q:\n%s", utcText, output)
	}
}

func hasArg(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

func runStatusJSON(t *testing.T, db string) statusJSON {
	t.Helper()
	stdout, stderr, err := runCLI(t, "--db", db, "status", "--json")
	if err != nil {
		t.Fatalf("status: %v stderr=%s", err, stderr)
	}
	var status statusJSON
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatalf("status json = %s err=%v", stdout, err)
	}
	return status
}

func assertStatusState(t *testing.T, status statusJSON, state string) {
	t.Helper()
	if status.AppID != "telecrawl" || status.State != state || status.Summary == "" || !status.Auth.Authorized || status.Auth.Expires != nil {
		t.Fatalf("status = %#v, want state %q", status, state)
	}
	if len(status.Counts) != 3 {
		t.Fatalf("counts = %#v, want 3", status.Counts)
	}
	want := []string{"messages", "chats", "since"}
	for i, count := range status.Counts {
		if count.ID != want[i] || count.Label != want[i] {
			t.Fatalf("counts = %#v, want ids %v", status.Counts, want)
		}
	}
}

func statusCountValue(t *testing.T, status statusJSON, id string) int64 {
	t.Helper()
	for _, count := range status.Counts {
		if count.ID == id {
			return count.Value
		}
	}
	t.Fatalf("counts = %#v, missing %q", status.Counts, id)
	return 0
}

func decodeDoctorChecks(t *testing.T, stdout string) []doctorCheckJSON {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &root); err != nil {
		t.Fatalf("doctor json = %s err=%v", stdout, err)
	}
	if _, ok := root["checks"]; !ok {
		t.Fatalf("doctor keys = %#v, want checks", root)
	}
	if len(root) > 2 {
		t.Fatalf("doctor keys = %#v, want checks and optional log", root)
	}
	var payload struct {
		Checks []doctorCheckJSON `json:"checks"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("doctor json = %s err=%v", stdout, err)
	}
	return payload.Checks
}

func runSearchJSON(t *testing.T, db string, args ...string) searchJSON {
	t.Helper()
	stdout, stderr, err := runCLI(t, append([]string{"--db", db}, args...)...)
	if err != nil {
		t.Fatalf("search: %v stderr=%s stdout=%s", err, stderr, stdout)
	}
	conformance.AssertSearchEnvelope(t, []byte(stdout))
	if strings.Contains(stdout, "who_matched") {
		t.Fatalf("search json contains removed who_matched field: %s", stdout)
	}
	var payload searchJSON
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("search json = %s err=%v", stdout, err)
	}
	for _, result := range payload.Results {
		assertShortRefAlias(t, result.ShortRef)
	}
	return payload
}

func runWhoJSON(t *testing.T, db string, args ...string) whoJSON {
	t.Helper()
	stdout, stderr, err := runCLI(t, append([]string{"--db", db, "who"}, args...)...)
	if err != nil {
		t.Fatalf("who: %v stderr=%s stdout=%s", err, stderr, stdout)
	}
	assertWhoEnvelopeShape(t, stdout)
	var payload whoJSON
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("who json = %s err=%v", stdout, err)
	}
	return payload
}

func assertWhoEnvelopeShape(t *testing.T, stdout string) {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &root); err != nil {
		t.Fatalf("who json = %s err=%v", stdout, err)
	}
	wantRoot := []string{"query", "candidates"}
	if len(root) != len(wantRoot) {
		t.Fatalf("who json keys = %#v, want %v", root, wantRoot)
	}
	for _, key := range wantRoot {
		if _, ok := root[key]; !ok {
			t.Fatalf("who json missing key %q: %#v", key, root)
		}
	}
	var candidates []map[string]json.RawMessage
	if err := json.Unmarshal(root["candidates"], &candidates); err != nil {
		t.Fatalf("who candidates json = %s err=%v", root["candidates"], err)
	}
	wantCandidate := []string{"who", "identifiers", "last_seen", "messages"}
	for _, candidate := range candidates {
		if len(candidate) != len(wantCandidate) {
			t.Fatalf("who candidate keys = %#v, want %v", candidate, wantCandidate)
		}
		for _, key := range wantCandidate {
			if _, ok := candidate[key]; !ok {
				t.Fatalf("who candidate missing key %q: %#v", key, candidate)
			}
		}
	}
}

func assertWhoResolved(t *testing.T, resolved *testWhoResolved, wantWho, wantIdentifier string) {
	t.Helper()
	if resolved == nil {
		t.Fatalf("who_resolved missing, want %q", wantWho)
	}
	if resolved.Who != wantWho || !hasString(resolved.Identifiers, wantIdentifier) {
		t.Fatalf("who_resolved = %#v, want %q with %q", resolved, wantWho, wantIdentifier)
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func runOpenJSON(t *testing.T, db string, ref string) openJSON {
	t.Helper()
	stdout, stderr, err := runCLI(t, "--db", db, "open", ref, "--json")
	if err != nil {
		t.Fatalf("open: %v stderr=%s stdout=%s", err, stderr, stdout)
	}
	var payload openJSON
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("open json = %s err=%v", stdout, err)
	}
	return payload
}

func assertSearchResultShape(t *testing.T, result testSearchResult) {
	t.Helper()
	if !strings.HasPrefix(result.Ref, "telecrawl:msg/") || result.Who == "" || result.Where == "" || result.Snippet == "" {
		t.Fatalf("bad search result = %#v", result)
	}
	assertShortRefAlias(t, result.ShortRef)
	if strings.ContainsAny(result.Who+result.Where+result.Snippet, "\n\t") || strings.ContainsAny(result.Snippet, "[]") || strings.Contains(result.Snippet, "...") {
		t.Fatalf("search result kept marker or multiline fields = %#v", result)
	}
	if _, err := time.Parse(time.RFC3339, result.Time); err != nil {
		t.Fatalf("search result time = %q err=%v", result.Time, err)
	}
}

func assertShortRefAlias(t *testing.T, alias string) {
	t.Helper()
	const alphabet = "23456789abcdefghjkmnpqrstuvwxyz"
	if len(alias) < 5 {
		t.Fatalf("short_ref = %q, want at least 5 characters", alias)
	}
	for _, ch := range alias {
		if !strings.ContainsRune(alphabet, ch) {
			t.Fatalf("short_ref = %q, invalid character %q", alias, ch)
		}
	}
}

func containsTableToken(output, token string) bool {
	for _, field := range strings.Fields(output) {
		if field == token {
			return true
		}
	}
	return false
}
