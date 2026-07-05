package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

func TestSyncUsesEndpointsPaginationAndSinceCursors(t *testing.T) {
	env := newSyncTestEnv(t)
	var authoredCalls int
	var mentionCalls int
	setXHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/2/users/42/bookmarks":
			if r.URL.Query().Get("pagination_token") == "" {
				writeTweetPage(w, []string{tweetJSON("b1", "synthetic bookmark one", "99")}, "bm2", "")
				return
			}
			writeTweetPage(w, []string{tweetJSON("b2", "synthetic bookmark two", "99")}, "", "")
		case "/2/users/42/tweets":
			authoredCalls++
			if authoredCalls == 1 && r.URL.Query().Get("since_id") != "" {
				t.Fatalf("first authored since_id = %q", r.URL.Query().Get("since_id"))
			}
			if authoredCalls == 2 && r.URL.Query().Get("since_id") != "20" {
				t.Fatalf("second authored since_id = %q, want 20", r.URL.Query().Get("since_id"))
			}
			if authoredCalls == 1 {
				writeTweetPage(w, []string{tweetJSON("20", "synthetic authored two", "42"), tweetJSON("19", "synthetic authored one", "42")}, "", "20")
				return
			}
			writeTweetPage(w, nil, "", "")
		case "/2/users/42/mentions":
			mentionCalls++
			if mentionCalls == 2 && r.URL.Query().Get("since_id") != "30" {
				t.Fatalf("second mentions since_id = %q, want 30", r.URL.Query().Get("since_id"))
			}
			if mentionCalls == 1 {
				writeTweetPage(w, []string{tweetJSON("30", "synthetic mention", "77")}, "", "30")
				return
			}
			writeTweetPage(w, nil, "", "")
		case "/2/users/42/liked_tweets":
			writeTweetPage(w, []string{tweetJSON("l1", "synthetic liked tweet", "88")}, "", "")
		case "/2/tweets":
			ids := strings.Split(r.URL.Query().Get("ids"), ",")
			items := make([]string, 0, len(ids))
			for _, id := range ids {
				if id != "" {
					items = append(items, tweetJSON(id, "synthetic refreshed metrics", "42"))
				}
			}
			writeTweetPage(w, items, "", "")
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	runSyncOK(t, env)
	status := readStatus(t, env.dbPath)
	if status.Authored != 2 || status.RepliesToMe != 1 || status.Bookmarks != 2 || status.LikesSeen != 1 {
		t.Fatalf("status after first sync = %#v", status)
	}
	runSyncOK(t, env)
	if authoredCalls < 2 || mentionCalls < 2 {
		t.Fatalf("since endpoints were not called twice: authored=%d mentions=%d", authoredCalls, mentionCalls)
	}
}

func TestSyncRateLimitCommitsAndResumes(t *testing.T) {
	env := newSyncTestEnv(t)
	blocked := true
	setXHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/2/users/42/bookmarks":
			if r.URL.Query().Get("pagination_token") == "" {
				writeTweetPage(w, []string{tweetJSON("b1", "synthetic bookmark one", "99")}, "resume-token", "")
				return
			}
			if blocked {
				w.Header().Set("x-rate-limit-reset", "4102444800")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			writeTweetPage(w, []string{tweetJSON("b2", "synthetic bookmark two", "99")}, "", "")
		case "/2/users/42/tweets", "/2/users/42/mentions", "/2/users/42/liked_tweets", "/2/tweets":
			writeTweetPage(w, nil, "", "")
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	code := runSyncErrorCode(t, env)
	if code != "rate_limited" {
		t.Fatalf("code = %q, want rate_limited", code)
	}
	if status := readStatus(t, env.dbPath); status.Bookmarks != 1 {
		t.Fatalf("bookmarks after partial = %d, want 1", status.Bookmarks)
	}
	blocked = false
	runSyncOK(t, env)
	if status := readStatus(t, env.dbPath); status.Bookmarks != 2 {
		t.Fatalf("bookmarks after resume = %d, want 2", status.Bookmarks)
	}
}

func TestSyncBookmarkIncrementalThenWeeklyFullPass(t *testing.T) {
	env := newSyncTestEnv(t)
	page := []string{tweetJSON("b1", "synthetic bookmark one", "99"), tweetJSON("b2", "synthetic bookmark two", "99")}
	setXHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/2/users/42/bookmarks":
			writeTweetPage(w, page, "", "")
		case "/2/users/42/tweets", "/2/users/42/mentions", "/2/users/42/liked_tweets", "/2/tweets":
			writeTweetPage(w, nil, "", "")
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	// Full pass stores both bookmarks.
	runSyncOK(t, env)
	if status := readStatus(t, env.dbPath); status.Bookmarks != 2 {
		t.Fatalf("bookmarks after full pass = %d, want 2", status.Bookmarks)
	}

	// Same week: incremental picks up the new bookmark on top and stops at
	// the first known one; a removal (b2 gone) is NOT noticed yet.
	page = []string{tweetJSON("b3", "synthetic bookmark three", "99"), tweetJSON("b1", "synthetic bookmark one", "99")}
	runSyncOK(t, env)
	if status := readStatus(t, env.dbPath); status.Bookmarks != 3 {
		t.Fatalf("bookmarks after incremental = %d, want 3", status.Bookmarks)
	}

	// Age the last full pass past a week: the next sync is a full pass and
	// detects that b2 and b3 were removed.
	backdateBookmarkPass(t, env.dbPath, 8*24*time.Hour)
	page = []string{tweetJSON("b1", "synthetic bookmark one", "99")}
	runSyncOK(t, env)
	if status := readStatus(t, env.dbPath); status.Bookmarks != 1 {
		t.Fatalf("bookmarks after weekly full pass = %d, want 1", status.Bookmarks)
	}
}

func backdateBookmarkPass(t *testing.T, dbPath string, age time.Duration) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	pass, err := st.SyncState(ctx, "bookmark_pass")
	if err != nil {
		t.Fatal(err)
	}
	err = st.CommitLivePage(ctx, store.LivePage{SyncedAt: time.Now().UTC(), States: []store.SyncStateUpdate{
		{Kind: "bookmark_pass", Cursor: pass.Cursor, LastResult: pass.LastResult, LastSyncAt: time.Now().UTC().Add(-age)},
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSyncBudgetCapStopsBeforeRequest(t *testing.T) {
	env := newSyncTestEnv(t)
	if err := os.WriteFile(env.configPath, []byte(`user_id = "42"
handle = "example_alex"
monthly_budget_usd = "0.0005"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	setXHandler(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeTweetPage(w, nil, "", "")
	})

	code := runSyncErrorCode(t, env)
	if code != "budget_exhausted" {
		t.Fatalf("code = %q, want budget_exhausted", code)
	}
	if calls != 0 {
		t.Fatalf("server calls = %d, want 0", calls)
	}
}

func TestSyncDeficientPageAbortsWithoutStoring(t *testing.T) {
	env := newSyncTestEnv(t)
	setXHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/2/users/42/bookmarks":
			writeTweetPage(w, []string{
				`{"id":"bad1","text":"","author_id":"99","created_at":"2026-07-04T12:00:00Z"}`,
				`{"text":"missing id","author_id":"99","created_at":"2026-07-04T12:00:00Z"}`,
				tweetJSON("good", "synthetic valid tweet", "99"),
			}, "", "")
		default:
			writeTweetPage(w, nil, "", "")
		}
	})

	code := runSyncErrorCode(t, env)
	if code != "deficient_input" {
		t.Fatalf("code = %q, want deficient_input", code)
	}
	if status := readStatus(t, env.dbPath); status.Tweets != 0 {
		t.Fatalf("tweets = %d, want 0", status.Tweets)
	}
}

func TestSyncResolvesMissingConfigIdentity(t *testing.T) {
	env := newSyncTestEnv(t)
	if err := os.WriteFile(env.configPath, []byte(`monthly_budget_usd = "10"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	setXHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/2/users/me":
			_, _ = w.Write([]byte(`{"data":{"id":"42","username":"example_alex","name":"Alex Example"}}`))
		case "/2/users/42/bookmarks", "/2/users/42/tweets", "/2/users/42/mentions", "/2/users/42/liked_tweets", "/2/tweets":
			writeTweetPage(w, nil, "", "")
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	runSyncOK(t, env)
	data, err := os.ReadFile(env.configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `user_id = "42"`) || !strings.Contains(text, `handle = "example_alex"`) {
		t.Fatalf("identity was not persisted:\n%s", text)
	}
}

func TestStatusReportsSpendAndAuthFields(t *testing.T) {
	env := newSyncTestEnv(t)
	st, err := store.Open(context.Background(), env.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddSpend(context.Background(), "2026-07", 1_250_000, timeDate2026()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAuthTokenValid(context.Background(), true, timeDate2026()); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	var stdout, stderr bytes.Buffer
	err = Run(context.Background(), []string{"--db", env.dbPath, "--config", env.configPath, "--json", "status"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("status error: %v stderr=%s", err, stderr.String())
	}
	var envelope statusEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Auth.CredentialsPresent || !envelope.Auth.TokenValidAtLastSync {
		t.Fatalf("auth = %#v", envelope.Auth)
	}
	if envelope.Spend.SpentUSD != "1.25" || envelope.Spend.MonthlyBudgetUSD != "10.00" {
		t.Fatalf("spend = %#v", envelope.Spend)
	}
	if envelope.Spend.LiveSyncPaused {
		t.Fatalf("live_sync_paused = true, want false")
	}
	var raw map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["log"]; ok {
		t.Fatalf("status json still has log: %s", stdout.String())
	}
}

func TestDoctorRunsNetworkedUserProbe(t *testing.T) {
	env := newSyncTestEnv(t)
	st, err := store.Open(context.Background(), env.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	setXHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2/users/me" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":{"id":"42","username":"example_alex","name":"Alex Example"}}`))
	})

	var stdout, stderr bytes.Buffer
	err = Run(context.Background(), []string{"--db", env.dbPath, "--config", env.configPath, "--json", "doctor"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("doctor error: %v stderr=%s", err, stderr.String())
	}
	var output doctorOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, check := range output.Checks {
		states[check.ID] = check.State
	}
	if states["credentials_present"] != "ok" || states["x_account_reachable"] != "ok" || states["monthly_budget"] != "ok" {
		t.Fatalf("doctor states = %#v", states)
	}
}

type syncTestEnv struct {
	dbPath     string
	configPath string
}

func newSyncTestEnv(t *testing.T) syncTestEnv {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, ".opentrawl", "birdcrawl")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	credentials := `client_id = "client-id"
client_secret = "client-secret"
access_token = "access"
refresh_token = "refresh"
bearer_token = "app-bearer"
token_scopes = "tweet.read users.read bookmark.read like.read offline.access"
`
	if err := os.WriteFile(filepath.Join(base, "credentials.toml"), []byte(credentials), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(base, "config.toml")
	if err := os.WriteFile(configPath, []byte(`user_id = "42"
handle = "example_alex"
monthly_budget_usd = "10"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	return syncTestEnv{dbPath: filepath.Join(base, "birdcrawl.db"), configPath: configPath}
}

func setXHandler(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access" {
			t.Fatalf("auth header = %q", got)
		}
		h(w, r)
	})
	oldBaseURL := xapiBaseURL
	oldClient := xapiHTTPClient
	xapiBaseURL = "https://x.test"
	xapiHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)
		return recorder.Result(), nil
	})}
	t.Cleanup(func() {
		xapiBaseURL = oldBaseURL
		xapiHTTPClient = oldClient
	})
}

func runSyncOK(t *testing.T, env syncTestEnv) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--db", env.dbPath, "--config", env.configPath, "--json", "sync"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("sync error: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func TestRefusedSyncDoesNotAdvanceFreshness(t *testing.T) {
	env := newSyncTestEnv(t)
	setXHandler(t, func(w http.ResponseWriter, r *http.Request) {
		writeTweetPage(w, nil, "", "")
	})
	// A completed sync stamps last_sync.
	runSyncOK(t, env)
	before := readStatus(t, env.dbPath)
	if before.LastLiveSync.IsZero() {
		t.Fatal("completed sync did not stamp last_sync")
	}

	// Exhaust the local budget: the next sync is refused before any request
	// and must not advance last_sync or relabel the previous result.
	if err := os.WriteFile(env.configPath, []byte(`user_id = "42"
handle = "example_alex"
monthly_budget_usd = "0.0000001"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runSyncErrorCode(t, env); code != "budget_exhausted" {
		t.Fatalf("code = %q, want budget_exhausted", code)
	}
	after := readStatus(t, env.dbPath)
	if !after.LastLiveSync.Equal(before.LastLiveSync) {
		t.Fatalf("refused sync moved last_sync: %v -> %v", before.LastLiveSync, after.LastLiveSync)
	}
	if after.LiveSyncResult != before.LiveSyncResult {
		t.Fatalf("refused sync relabelled result: %q -> %q", before.LiveSyncResult, after.LiveSyncResult)
	}
}

func runSyncErrorCode(t *testing.T, env syncTestEnv) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--db", env.dbPath, "--config", env.configPath, "--json", "sync"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected sync error\nstdout=%s", stdout.String())
	}
	lines := bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n"))
	if len(lines) == 0 {
		t.Fatalf("missing error envelope: %v stderr=%s", err, stderr.String())
	}
	var envelope errorEnvelope
	if jsonErr := json.Unmarshal(lines[len(lines)-1], &envelope); jsonErr != nil {
		t.Fatalf("decode error envelope: %v\nstdout=%s", jsonErr, stdout.String())
	}
	return envelope.Error.Code
}

func readStatus(t *testing.T, dbPath string) store.Status {
	t.Helper()
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	status, err := st.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return status
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func writeTweetPage(w http.ResponseWriter, tweets []string, next, newest string) {
	var out strings.Builder
	out.WriteString(`{"data":[`)
	for i, tweet := range tweets {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(tweet)
	}
	out.WriteString(`],"includes":{"users":[`)
	out.WriteString(`{"id":"42","username":"example_alex","name":"Alex Example"},`)
	out.WriteString(`{"id":"99","username":"example_blair","name":"Blair Example"},`)
	out.WriteString(`{"id":"77","username":"example_casey","name":"Casey Example"},`)
	out.WriteString(`{"id":"88","username":"example_devon","name":"Devon Example"}`)
	out.WriteString(`]},"meta":{"result_count":`)
	out.WriteString(itoa(len(tweets)))
	if next != "" {
		out.WriteString(`,"next_token":"` + next + `"`)
	}
	if newest != "" {
		out.WriteString(`,"newest_id":"` + newest + `"`)
	}
	out.WriteString(`}}`)
	_, _ = w.Write([]byte(out.String()))
}

func tweetJSON(id, text, authorID string) string {
	return `{"id":"` + id + `","text":"` + text + `","author_id":"` + authorID + `","created_at":"2026-07-04T12:00:00Z","conversation_id":"` + id + `","public_metrics":{"like_count":1,"retweet_count":2,"reply_count":3,"impression_count":4,"quote_count":5,"bookmark_count":6}}`
}

func timeDate2026() time.Time {
	return time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
}
