package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type recordingLogger struct {
	mu     sync.Mutex
	events []string
}

func (l *recordingLogger) Info(event, message string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event+" "+message)
	return nil
}

func (l *recordingLogger) Warn(event, message string) error {
	return l.Info("warn:"+event, message)
}

func (l *recordingLogger) has(prefix string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, event := range l.events {
		if strings.HasPrefix(event, prefix) {
			return true
		}
	}
	return false
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"response": "fine", "done": true})
	})
}

func runClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := sandboxSafeServer(t, handler)
	client, err := New(Config{BaseURL: server.URL, Model: "fixture-model"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func simplePrepare(t *testing.T, client *Client) func(context.Context, int) (Call, error) {
	t.Helper()
	return func(context.Context, int) (Call, error) {
		request, err := client.Render(Request{Prompt: "item"})
		return Call{Request: request}, err
	}
}

func TestRunHappyPathPreparesSeriallyAndCommitsEveryItem(t *testing.T) {
	client := runClient(t, okHandler())
	logger := &recordingLogger{}
	var prepared []int
	var mu sync.Mutex
	committed := map[int]Outcome{}
	stats, err := Run(context.Background(), client, 5,
		func(ctx context.Context, index int) (Call, error) {
			prepared = append(prepared, index) // serial by contract; no lock needed
			request, err := client.Render(Request{Prompt: "item"})
			return Call{Request: request}, err
		},
		func(res Result) error {
			mu.Lock()
			defer mu.Unlock()
			committed[res.Index] = res.Outcome
			return nil
		}, logger)
	if err != nil {
		t.Fatal(err)
	}
	if stats.OK != 5 || stats.Failed != 0 || stats.RateLimited != 0 || stats.Skipped != 0 || stats.Aborted {
		t.Fatalf("stats = %+v", stats)
	}
	for i, index := range prepared {
		if index != i {
			t.Fatalf("prepare order = %v, want serial index order", prepared)
		}
	}
	if len(committed) != 5 {
		t.Fatalf("committed = %v", committed)
	}
	if !logger.has("model_run ") {
		t.Fatalf("missing model_run summary line: %v", logger.events)
	}
}

func TestRunQuotaRefusalIsNeverAFailureAndAbortsWhenItDominates(t *testing.T) {
	client := runClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "quota exhausted", http.StatusTooManyRequests)
	}))
	logger := &recordingLogger{}
	var mu sync.Mutex
	committed := map[int]Outcome{}
	stats, err := Run(context.Background(), client, 40, simplePrepare(t, client),
		func(res Result) error {
			mu.Lock()
			defer mu.Unlock()
			committed[res.Index] = res.Outcome
			return nil
		}, logger)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Failed != 0 {
		t.Fatalf("quota refusals recorded as failures: %+v", stats)
	}
	if !stats.Aborted || stats.RateLimited < quotaAbortThreshold {
		t.Fatalf("abort did not fire: %+v", stats)
	}
	if stats.Skipped == 0 {
		t.Fatalf("abort fired but nothing was skipped: %+v", stats)
	}
	if stats.RateLimited+stats.Skipped != 40 {
		t.Fatalf("accounting: %+v", stats)
	}
	for index, outcome := range committed {
		if outcome != OutcomeRateLimited {
			t.Fatalf("index %d committed as %v", index, outcome)
		}
	}
	if len(committed) != stats.RateLimited {
		t.Fatalf("commit called %d times for %d rate-limited items", len(committed), stats.RateLimited)
	}
	if !logger.has("warn:quota_exhausted") {
		t.Fatalf("rule 1.15 alarm not logged: %v", logger.events)
	}
}

func TestRunPrepareErrorFailsItemWithoutModelCall(t *testing.T) {
	var calls atomic.Int64
	client := runClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"response": "fine", "done": true})
	}))
	logger := &recordingLogger{}
	stats, err := Run(context.Background(), client, 3,
		func(ctx context.Context, index int) (Call, error) {
			if index == 1 {
				return Call{}, errors.New("original file vanished")
			}
			request, err := client.Render(Request{Prompt: "item"})
			return Call{Request: request}, err
		},
		func(res Result) error {
			if res.Index == 1 && (res.Outcome != OutcomeFailed || res.Attempts != 0) {
				return errors.New("prepare failure misfiled")
			}
			return nil
		}, logger)
	if err != nil {
		t.Fatal(err)
	}
	if stats.OK != 2 || stats.Failed != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if calls.Load() != 2 {
		t.Fatalf("model called %d times, want 2", calls.Load())
	}
}

func TestRunTransientErrorSendsOnce(t *testing.T) {
	var calls atomic.Int64
	client := runClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "overloaded", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"response": "fine", "done": true})
	}))
	logger := &recordingLogger{}
	stats, err := Run(context.Background(), client, 1, simplePrepare(t, client),
		func(res Result) error {
			if res.Outcome != OutcomeFailed || res.Attempts != 1 || res.TransientEvents != 1 {
				return errors.New("single-attempt accounting wrong")
			}
			return nil
		}, logger)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Failed != 1 || stats.Attempts != 1 || stats.TransientEvents != 1 || calls.Load() != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestRunRetainedResponseResumesWithoutSend(t *testing.T) {
	var calls atomic.Int64
	client := runClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"response": "unexpected", "done": true})
	}))
	request, err := client.Render(Request{Prompt: "retained synthetic item"})
	if err != nil {
		t.Fatal(err)
	}
	raw := RawResult{Response: []byte(`{"response":"retained answer","done":true}`), Status: "200 OK", StatusCode: 200, TransmissionStarted: true}
	stats, err := Run(context.Background(), client, 1,
		func(context.Context, int) (Call, error) {
			return Call{Request: request, Retained: &raw}, nil
		},
		func(res Result) error {
			if !res.Retained || res.Attempts != 0 || res.Outcome != OutcomeOK {
				return fmt.Errorf("retained result = %#v", res)
			}
			parsed, err := Parse(res.Request, res.Raw)
			if err != nil || parsed.Text != "retained answer" {
				return fmt.Errorf("retained parse = %#v, %v", parsed, err)
			}
			return nil
		}, &recordingLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || stats.Attempts != 0 || stats.OK != 1 {
		t.Fatalf("calls = %d, stats = %+v", calls.Load(), stats)
	}
}

func TestRunTimeoutAndConnectionFailureEachSendOnce(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "timeout", err: syntheticTimeoutError{}},
		{name: "connection", err: errors.New("synthetic connection refused")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := New(Config{BaseURL: "https://models.example.com", Model: "fixture-model"})
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int64
			client.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return nil, test.err
			})
			stats, err := Run(context.Background(), client, 1, simplePrepare(t, client), func(res Result) error {
				if res.Attempts != 1 || res.Outcome != OutcomeFailed {
					return fmt.Errorf("result = %#v", res)
				}
				return nil
			}, &recordingLogger{})
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 || stats.Attempts != 1 {
				t.Fatalf("calls = %d, stats = %+v", calls.Load(), stats)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type syntheticTimeoutError struct{}

func (syntheticTimeoutError) Error() string   { return "synthetic timeout" }
func (syntheticTimeoutError) Timeout() bool   { return true }
func (syntheticTimeoutError) Temporary() bool { return true }

func TestRunCommitErrorIsFatal(t *testing.T) {
	client := runClient(t, okHandler())
	logger := &recordingLogger{}
	wantErr := errors.New("database gone")
	stats, err := Run(context.Background(), client, 10, simplePrepare(t, client),
		func(res Result) error { return wantErr }, logger)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v", err)
	}
	if stats.OK != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.OK+stats.Skipped != 10 {
		t.Fatalf("accounting after fatal commit: %+v", stats)
	}
}

// TestRunStressAccountingHoldsUnderFlakyProvider hammers Run with a provider
// that randomly 429s, 5xxs, or answers, at high concurrency. It is the
// slot-leak / results-close-race guard: any leaked limiter slot or lost
// result deadlocks the run (the -race timeout fires) or breaks the
// one-outcome-per-item invariant checked here.
func TestRunStressAccountingHoldsUnderFlakyProvider(t *testing.T) {
	var seed atomic.Int64
	client := runClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch seed.Add(1) % 5 {
		case 0:
			http.Error(w, "quota", http.StatusTooManyRequests)
		case 1:
			http.Error(w, "overloaded", http.StatusServiceUnavailable)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"response": "fine", "done": true})
		}
	}))
	logger := &recordingLogger{}
	const n = 200
	var mu sync.Mutex
	committed := map[int]struct{}{}
	stats, err := Run(context.Background(), client, n, simplePrepare(t, client),
		func(res Result) error {
			mu.Lock()
			defer mu.Unlock()
			if _, dup := committed[res.Index]; dup {
				return errors.New("index committed twice")
			}
			committed[res.Index] = struct{}{}
			return nil
		}, logger)
	if err != nil {
		t.Fatal(err)
	}
	if stats.OK+stats.RateLimited+stats.Failed+stats.Skipped != n {
		t.Fatalf("accounting broke: %+v (sum != %d)", stats, n)
	}
	if len(committed) != stats.OK+stats.RateLimited+stats.Failed {
		t.Fatalf("commit called %d times, resolved outcomes = %d: %+v", len(committed), stats.OK+stats.RateLimited+stats.Failed, stats)
	}
	if stats.ConcurrencyPeak > concurrencyMax {
		t.Fatalf("limiter exceeded max: peak=%d max=%d", stats.ConcurrencyPeak, concurrencyMax)
	}
}

func TestRunRequiresLogger(t *testing.T) {
	client, err := New(Config{BaseURL: DefaultBaseURL, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), client, 1, simplePrepare(t, client),
		func(Result) error { return nil }, nil); err == nil {
		t.Fatal("nil logger accepted")
	}
}
