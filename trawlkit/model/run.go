package model

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Guardrail constants. Deliberately not configuration: abort threshold and
// concurrency are library behaviour, promoted to options only on proven pain.
const (
	concurrencyStart    = 6
	concurrencyMax      = 10
	quotaAbortThreshold = 8
)

// Logger receives the run's structured events. It is required: a silent run
// would swallow the quota_exhausted alarm.
type Logger interface {
	Info(event, message string) error
	Warn(event, message string) error
}

type Outcome int

const (
	// OutcomeOK: the model answered and commit ran.
	OutcomeOK Outcome = iota
	// OutcomeRateLimited: the single attempt returned 429. Quota refusal is
	// the provider's state, not the item's.
	OutcomeRateLimited
	// OutcomeFailed: prepare failed, or a non-quota model error survived
	// the single attempt.
	OutcomeFailed
	// OutcomeReused: the caller already completed this exact request.
	OutcomeReused
)

// Call is either one immutable provider request, one retained raw result to
// resume without a send, or one completed request to reuse.
type Call struct {
	Request  ProviderRequest
	Retained *RawResult
	Reused   bool
}

type Result struct {
	Index           int
	Request         ProviderRequest
	Raw             RawResult
	Err             error
	Outcome         Outcome
	Attempts        int
	RateLimitEvents int // attempts refused with 429
	TransientEvents int // attempts failed with 5xx/timeout
	Duration        time.Duration
	Retained        bool
}

type Stats struct {
	OK              int
	Reused          int
	RateLimited     int
	Failed          int
	Skipped         int
	Aborted         bool
	Attempts        int
	RateLimitEvents int
	TransientEvents int
	CallMillis      int64
	ConcurrencyPeak int
	ConcurrencyEnd  int
}

// Run processes n items with adaptive concurrency, quota abort and
// one-outcome-per-item accounting. Each new call is sent once. A retained
// result is returned to commit without a send, so callers can resume parsing.
//
// prepare is called serially, in index order. It may do expensive input work
// (downloads, rendering) and must release its own resources before
// returning; item i+1 prepares while item i generates. A prepare error
// resolves the item as OutcomeFailed without a model call and without
// touching the quota counter.
//
// Send runs concurrently under the adaptive limiter. commit is called
// serially, in completion order; a commit error is fatal to the run.
//
// After quotaAbortThreshold consecutive rate-limited commits the run aborts:
// no new items start and in-flight calls are cancelled. Their raw failures
// still reach commit for retention. Items that never started count as skipped.
func Run(
	ctx context.Context,
	client *Client,
	n int,
	prepare func(ctx context.Context, index int) (Call, error),
	commit func(res Result) error,
	log Logger,
) (Stats, error) {
	if log == nil {
		return Stats{}, errors.New("model run: logger is required")
	}
	stats := Stats{}
	if n <= 0 {
		return stats, nil
	}

	// The feed context stops new work on abort; commit keeps the parent
	// context so completed results still land.
	feedCtx, stopFeeding := context.WithCancel(ctx)
	defer stopFeeding()

	limiter := newAdaptiveLimiter(concurrencyStart, concurrencyMax)
	results := make(chan Result, n)

	var generateWG sync.WaitGroup
	go func() {
		defer func() {
			generateWG.Wait()
			close(results)
		}()
		for index := 0; index < n; index++ {
			if feedCtx.Err() != nil {
				return
			}
			call, err := prepare(feedCtx, index)
			if err != nil {
				if feedCtx.Err() != nil {
					return
				}
				results <- Result{Index: index, Err: err, Outcome: OutcomeFailed}
				continue
			}
			// Acquire before spawning so prepared inputs waiting in
			// memory stay bounded by the limiter's window.
			limiter.Acquire()
			generateWG.Add(1)
			go func(index int, call Call) {
				defer generateWG.Done()
				res, done := generateOnce(feedCtx, client, limiter, index, call)
				if done {
					results <- res
				}
			}(index, call)
		}
	}()

	seen := make([]bool, n)
	committed := 0
	consecutiveRateLimited := 0
	var fatal error
	for res := range results {
		if fatal != nil {
			continue // drain after a fatal commit error
		}
		if res.Index < 0 || res.Index >= n || seen[res.Index] {
			fatal = fmt.Errorf("model run: outcome accounting broken for index %d", res.Index)
			stopFeeding()
			continue
		}
		seen[res.Index] = true
		committed++
		stats.Attempts += res.Attempts
		stats.RateLimitEvents += res.RateLimitEvents
		stats.TransientEvents += res.TransientEvents
		stats.CallMillis += res.Duration.Milliseconds()
		switch res.Outcome {
		case OutcomeOK:
			stats.OK++
			consecutiveRateLimited = 0
		case OutcomeReused:
			stats.Reused++
			consecutiveRateLimited = 0
		case OutcomeRateLimited:
			stats.RateLimited++
			if res.Attempts > 0 {
				consecutiveRateLimited++
			}
			if consecutiveRateLimited == quotaAbortThreshold && !stats.Aborted {
				stats.Aborted = true
				_ = log.Warn("quota_exhausted",
					fmt.Sprintf("consecutive_429=%d action=stop_feeding_batch", consecutiveRateLimited))
				stopFeeding()
			}
		case OutcomeFailed:
			stats.Failed++
			consecutiveRateLimited = 0
		}
		if err := commit(res); err != nil {
			fatal = err
			stopFeeding()
		}
	}
	stats.Skipped = n - committed
	stats.ConcurrencyPeak = limiter.Peak()
	stats.ConcurrencyEnd = limiter.Current()
	_ = log.Info("model_run", fmt.Sprintf(
		"items=%d ok=%d reused=%d rate_limited=%d failed=%d skipped=%d aborted=%t attempts=%d model_ms=%d concurrency_peak=%d",
		n, stats.OK, stats.Reused, stats.RateLimited, stats.Failed, stats.Skipped, stats.Aborted,
		stats.Attempts, stats.CallMillis, stats.ConcurrencyPeak))
	if fatal != nil {
		return stats, fatal
	}
	if err := ctx.Err(); err != nil {
		return stats, err
	}
	return stats, nil
}

// generateOnce sends a new call once or returns retained work without a send.
// The feeder has already acquired a limiter slot.
// It returns done=false when the run was cancelled mid-item: the item has no
// outcome and its state stays untouched.
func generateOnce(ctx context.Context, client *Client, limiter *adaptiveLimiter, index int, call Call) (Result, bool) {
	defer limiter.Release()
	res := Result{Index: index, Request: call.Request}
	if call.Reused {
		res.Outcome = OutcomeReused
		return res, true
	}
	if call.Retained != nil {
		res.Raw = *call.Retained
		res.Retained = true
		res.Err = rawResultError(res.Raw)
		classifyResult(&res, limiter)
		return res, true
	}
	if err := ctx.Err(); err != nil {
		return Result{}, false
	}
	startedAt := time.Now()
	res.Raw, res.Err = client.Send(ctx, call.Request)
	res.Attempts = 1
	res.Duration = time.Since(startedAt)
	classifyResult(&res, limiter)
	return res, true
}

func rawResultError(raw RawResult) error {
	if len(raw.Failure) > 0 {
		return fmt.Errorf("model request failed: %s", raw.Failure)
	}
	if raw.StatusCode < 200 || raw.StatusCode >= 300 {
		return &HTTPError{Status: raw.Status, StatusCode: raw.StatusCode, Body: string(raw.Response)}
	}
	return nil
}

func classifyResult(res *Result, limiter *adaptiveLimiter) {
	class := classifyFailure(res.Err)
	if class.rateLimited {
		res.RateLimitEvents = 1
		res.Outcome = OutcomeRateLimited
		limiter.RecordThrottle()
		return
	}
	if class.transient {
		res.TransientEvents = 1
		limiter.RecordTransient()
	}
	if res.Err != nil {
		res.Outcome = OutcomeFailed
		return
	}
	limiter.RecordSuccess()
	res.Outcome = OutcomeOK
}

type failureClass struct {
	rateLimited bool
	transient   bool
}

func classifyFailure(err error) failureClass {
	if err == nil {
		return failureClass{}
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode == 429 {
			return failureClass{rateLimited: true}
		}
		if httpErr.StatusCode >= 500 && httpErr.StatusCode <= 599 {
			return failureClass{transient: true}
		}
		return failureClass{}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failureClass{transient: true}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return failureClass{transient: true}
	}
	return failureClass{}
}
