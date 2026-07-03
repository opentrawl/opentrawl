package archive

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/openclaw/photoscrawl/internal/modelclient"
)

type adaptiveLimiter struct {
	mu        sync.Mutex
	cond      *sync.Cond
	current   int
	max       int
	inFlight  int
	peak      int
	successes int
}

func newAdaptiveLimiter(start, max int) *adaptiveLimiter {
	if start < 1 {
		start = 1
	}
	if max < start {
		max = start
	}
	limiter := &adaptiveLimiter{current: start, max: max}
	limiter.cond = sync.NewCond(&limiter.mu)
	return limiter
}

func (l *adaptiveLimiter) Acquire() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for l.inFlight >= l.current {
		l.cond.Wait()
	}
	l.inFlight++
	if l.inFlight > l.peak {
		l.peak = l.inFlight
	}
}

func (l *adaptiveLimiter) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight > 0 {
		l.inFlight--
	}
	l.cond.Broadcast()
}

func (l *adaptiveLimiter) RecordSuccess() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.successes++
	if l.current < l.max && l.successes >= l.current {
		l.current++
		l.successes = 0
	}
	l.cond.Broadcast()
}

func (l *adaptiveLimiter) RecordThrottle() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.current /= 2
	if l.current < 1 {
		l.current = 1
	}
	l.successes = 0
	l.cond.Broadcast()
}

func (l *adaptiveLimiter) Peak() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.peak
}

func (l *adaptiveLimiter) Current() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.current
}

type retryDecision struct {
	retry       bool
	rateLimited bool
	transient   bool
}

func retryableModelError(err error) retryDecision {
	if err == nil {
		return retryDecision{}
	}
	var httpErr *modelclient.HTTPError
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

func classifyFailureReason(err error) string {
	if err == nil {
		return ""
	}
	var httpErr *modelclient.HTTPError
	if errors.As(err, &httpErr) {
		return fmt.Sprintf("model returned %s", httpErr.Status)
	}
	return truncateReason(err.Error())
}
