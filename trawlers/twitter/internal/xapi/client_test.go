package xapi

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRefreshPersistsRotatedTokens(t *testing.T) {
	credentialsPath := writeTestCredentials(t, "old-access", "old-refresh")
	var meCalls int
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/2/users/me":
			meCalls++
			if meCalls == 1 {
				if got := r.Header.Get("Authorization"); got != "Bearer old-access" {
					t.Fatalf("first auth = %q", got)
				}
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer new-access" {
				t.Fatalf("retry auth = %q", got)
			}
			_, _ = w.Write([]byte(`{"data":{"id":"42","username":"example_alex","name":"Alex Example"}}`))
		case "/2/oauth2/token":
			want := "Basic " + base64.StdEncoding.EncodeToString([]byte("client-id:client-secret"))
			if got := r.Header.Get("Authorization"); got != want {
				t.Fatalf("basic auth = %q", got)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old-refresh" {
				t.Fatalf("refresh form = %v", r.Form)
			}
			_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})
	client, err := New(Options{BaseURL: "https://x.test", CredentialsPath: credentialsPath, HTTPClient: handlerClient(handler)})
	if err != nil {
		t.Fatal(err)
	}
	user, charge, err := client.Me(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "42" || charge.Users != 1 {
		t.Fatalf("user/charge = %#v/%#v", user, charge)
	}
	data, err := os.ReadFile(credentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"access_token = \"new-access\"", "refresh_token = \"new-refresh\"", "bearer_token = \"app-bearer\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("credentials missing %q:\n%s", want, text)
		}
	}
	if info, err := os.Stat(credentialsPath); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestRateLimitRetriesNearResetAndStopsOnFarReset(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	credentialsPath := writeTestCredentials(t, "access", "refresh")
	var calls int
	var slept []time.Duration
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			w.Header().Set("x-rate-limit-reset", "1001")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"42","username":"example_alex","name":"Alex Example"}}`))
	})
	client, err := New(Options{
		BaseURL:         "https://x.test",
		CredentialsPath: credentialsPath,
		HTTPClient:      handlerClient(handler),
		Now:             func() time.Time { return now },
		Sleep: func(ctx context.Context, d time.Duration) error {
			slept = append(slept, d)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.Me(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 2 || slept[0] != time.Second {
		t.Fatalf("slept = %v, want two 1s retries", slept)
	}

	far := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-rate-limit-reset", "1120")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	client, err = New(Options{BaseURL: "https://x.test", CredentialsPath: credentialsPath, HTTPClient: handlerClient(far), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.Me(context.Background())
	var rateLimited *RateLimitedError
	if !errors.As(err, &rateLimited) {
		t.Fatalf("err = %v, want RateLimitedError", err)
	}
}

func TestEndpointPathsAndQueries(t *testing.T) {
	credentialsPath := writeTestCredentials(t, "access", "refresh")
	seen := map[string]bool{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = true
		if r.URL.Query().Get("tweet.fields") == "" && r.URL.Path != "/2/users/me" {
			t.Fatalf("tweet.fields missing for %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/2/users/42/tweets":
			if r.URL.Query().Get("since_id") != "10" || r.URL.Query().Get("pagination_token") != "next" {
				t.Fatalf("tweets query = %s", r.URL.RawQuery)
			}
			emptyPage(w)
		case "/2/users/42/mentions", "/2/users/42/liked_tweets", "/2/users/42/bookmarks":
			emptyPage(w)
		case "/2/tweets":
			if r.URL.Query().Get("ids") != "1,2" {
				t.Fatalf("ids query = %s", r.URL.RawQuery)
			}
			emptyPage(w)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})
	client, err := New(Options{BaseURL: "https://x.test", CredentialsPath: credentialsPath, HTTPClient: handlerClient(handler)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := client.UserTweets(ctx, "42", PageQuery{SinceID: "10", PaginationToken: "next"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Mentions(ctx, "42", PageQuery{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.LikedTweets(ctx, "42", PageQuery{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Bookmarks(ctx, "42", PageQuery{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Tweets(ctx, []string{"1", "2"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/2/users/42/tweets", "/2/users/42/mentions", "/2/users/42/liked_tweets", "/2/users/42/bookmarks", "/2/tweets"} {
		if !seen[path] {
			t.Fatalf("path %s was not called", path)
		}
	}
}

func writeTestCredentials(t *testing.T, access, refresh string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.toml")
	data := `client_id = "client-id"
client_secret = "client-secret"
access_token = "` + access + `"
refresh_token = "` + refresh + `"
bearer_token = "app-bearer"
token_scopes = "tweet.read users.read bookmark.read like.read offline.access"
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func emptyPage(w http.ResponseWriter) {
	_, _ = w.Write([]byte(`{"data":[],"meta":{"result_count":0}}`))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func handlerClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)
		return recorder.Result(), nil
	})}
}
