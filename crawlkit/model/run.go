package model

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Guardrail constants. Deliberately not configuration: attempt count, abort
// threshold, and concurrency are library behavior, promoted to options only
// on proven pain.
const (
	runAttempts         = 2
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
	// OutcomeRateLimited: retries exhausted on 429. Quota refusal is the
	// provider's state, not the item's — callers requeue, never fail.
	OutcomeRateLimited
	// OutcomeFailed: prepare failed, or a non-quota model error survived
	// retries.
	OutcomeFailed
)

type Result struct {
	Index           int
	Response        Response
	Err             error
	Outcome         Outcome
	Attempts        int
	RateLimitEvents int // attempts refused with 429
	TransientEvents int // attempts failed with 5xx/timeout
	Duration        time.Duration
}

type Stats struct {
	OK              int
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

// Run processes n items with the guardrails every model caller needs and
// none may reimplement: bounded retries with a 429/transient taxonomy,
// adaptive concurrency, quota abort, and one-outcome-per-item accounting.
//
// prepare is called serially, in index order. It may do expensive input work
// (downloads, rendering) and must release its own resources before
// returning; item i+1 prepares while item i generates. A prepare error
// resolves the item as OutcomeFailed without a model call and without
// touching the quota counter.
//
// Generate runs concurrently under the adaptive limiter. commit is called
// serially, in completion order; a commit error is fatal to the run.
//
// After quotaAbortThreshold consecutive rate-limited commits the run aborts:
// no new items start, in-flight calls are cancelled, and every item without
// a completed outcome counts in Stats.Skipped with commit never called — its
// state stays untouched for a later run.
func Run(
	ctx context.Context,
	client *Client,
	n int,
	prepare func(ctx context.Context, index int) (Request, error),
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
			req, err := prepare(feedCtx, index)
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
			go func(index int, req Request) {
				defer generateWG.Done()
				res, done := generateWithRetries(feedCtx, client, limiter, index, req, log)
				if done {
					results <- res
				}
			}(index, req)
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
		case OutcomeRateLimited:
			stats.RateLimited++
			consecutiveRateLimited++
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
		"items=%d ok=%d rate_limited=%d failed=%d skipped=%d aborted=%t attempts=%d model_ms=%d concurrency_peak=%d",
		n, stats.OK, stats.RateLimited, stats.Failed, stats.Skipped, stats.Aborted,
		stats.Attempts, stats.CallMillis, stats.ConcurrencyPeak))
	if fatal != nil {
		return stats, fatal
	}
	if err := ctx.Err(); err != nil {
		return stats, err
	}
	return stats, nil
}

// generateWithRetries runs the bounded retry loop for one item. The caller
// (the feeder) has already acquired a limiter slot for the first attempt.
// It returns done=false when the run was cancelled mid-item: the item has no
// outcome and its state stays untouched.
func generateWithRetries(ctx context.Context, client *Client, limiter *adaptiveLimiter, index int, req Request, log Logger) (Result, bool) {
	res := Result{Index: index}
	var lastErr error
	slotHeld := true
	for attempt := 1; attempt <= runAttempts; attempt++ {
		if !slotHeld {
			limiter.Acquire()
			slotHeld = true
		}
		if err := ctx.Err(); err != nil {
			lastErr = err
			break
		}
		startedAt := time.Now()
		response, err := client.Generate(ctx, req)
		limiter.Release()
		slotHeld = false
		res.Attempts++
		res.Duration += time.Since(startedAt)
		if err == nil {
			limiter.RecordSuccess()
			res.Response = response
			res.Outcome = OutcomeOK
			return res, true
		}
		lastErr = err
		class := retryClass(err)
		limiterBefore := limiter.Current()
		if class.rateLimited {
			res.RateLimitEvents++
			limiter.RecordThrottle()
		} else if class.transient {
			res.TransientEvents++
			limiter.RecordTransient()
		}
		if !class.retry || attempt == runAttempts {
			break
		}
		_ = log.Info("model_retry", fmt.Sprintf(
			"index=%d attempt=%d rate_limited=%t transient=%t limiter_before=%d limiter_after=%d error=%q",
			index, attempt, class.rateLimited, class.transient, limiterBefore, limiter.Current(), err.Error()))
	}
	if slotHeld {
		limiter.Release()
	}
	if ctx.Err() != nil {
		// The run was cancelled mid-item; no outcome, state untouched.
		return Result{}, false
	}
	res.Err = lastErr
	if retryClass(lastErr).rateLimited {
		res.Outcome = OutcomeRateLimited
	} else {
		res.Outcome = OutcomeFailed
	}
	return res, true
}

type retryDecision struct {
	retry       bool
	rateLimited bool
	transient   bool
}

func retryClass(err error) retryDecision {
	if err == nil {
		return retryDecision{}
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode == 429 {
			return retryDecision{retry: true, rateLimited: true}
		}
		if httpErr.StatusCode >= 500 && httpErr.StatusCode <= 599 {
			return retryDecision{retry: true, transient: true}
		}
		return retryDecision{}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return retryDecision{retry: true, transient: true}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return retryDecision{retry: true, transient: true}
	}
	return retryDecision{}
}
