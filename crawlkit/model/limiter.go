package model

import "sync"

// adaptiveLimiter self-tunes model-call concurrency: halve on 429, one step
// down (with a floor) on transient errors, ramp up on sustained success.
// Deliberately not configurable — the whole point is that callers cannot
// tune themselves into hammering a provider.
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

// RecordTransient eases off one step with a floor: isolated capacity blips
// (5xx, timeouts) are the provider shedding load, not a rate limit — halving
// to 1 on each blip collapsed throughput to a tenth of the provider ceiling.
func (l *adaptiveLimiter) RecordTransient() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.current--
	if l.current < 4 {
		l.current = 4
	}
	if l.current > l.max {
		l.current = l.max
	}
	l.successes = 0
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
