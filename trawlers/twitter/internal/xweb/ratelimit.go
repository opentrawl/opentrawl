package xweb

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// WebEndpointName identifies a rate-limited X web endpoint bucket so cooldown
// windows are tracked independently per endpoint.
type WebEndpointName string

const (
	WebEndpointViewer    WebEndpointName = "viewer"
	WebEndpointBookmarks WebEndpointName = "bookmarks"
	WebEndpointLikes     WebEndpointName = "likes"
	WebEndpointAuthored  WebEndpointName = "authored"
	WebEndpointMentions  WebEndpointName = "mentions"
	WebEndpointDMs       WebEndpointName = "dms"
)

// RateLimitObservation captures the rate-limit headers reported by X on a
// response. HasQuota is true when X included x-rate-limit-remaining or
// x-rate-limit-limit.
type RateLimitObservation struct {
	Endpoint    WebEndpointName
	Limit       int
	Remaining   int
	HasQuota    bool
	ResetAt     time.Time
	ObservedAt  time.Time
	RateLimited bool
}

// RateLimitPolicy controls client-side pacing and safety margins against X web
// endpoints. Defaults are conservative so background syncs neither exhaust the
// user's browser session quota nor trigger anti-automation lockouts.
type RateLimitPolicy struct {
	MinRequestGap        time.Duration
	MaxRequestJitter     time.Duration
	RemainingSafetyFloor int
	ShortWaitThreshold   time.Duration
	FallbackCooldown     time.Duration
}

func DefaultRateLimitPolicy() RateLimitPolicy {
	return RateLimitPolicy{
		MinRequestGap:        1200 * time.Millisecond,
		MaxRequestJitter:     600 * time.Millisecond,
		RemainingSafetyFloor: 2,
		ShortWaitThreshold:   15 * time.Second,
		FallbackCooldown:     15 * time.Minute,
	}
}

func (p RateLimitPolicy) withDefaults() RateLimitPolicy {
	defaults := DefaultRateLimitPolicy()
	if p.MinRequestGap <= 0 {
		p.MinRequestGap = defaults.MinRequestGap
	}
	if p.MaxRequestJitter < 0 {
		p.MaxRequestJitter = 0
	}
	if p.RemainingSafetyFloor < 0 {
		p.RemainingSafetyFloor = defaults.RemainingSafetyFloor
	}
	if p.ShortWaitThreshold <= 0 {
		p.ShortWaitThreshold = defaults.ShortWaitThreshold
	}
	if p.FallbackCooldown <= 0 {
		p.FallbackCooldown = defaults.FallbackCooldown
	}
	return p
}

// WebRateLimitedError signals that an X web endpoint reached its rate limit or
// hit the client's remaining-quota safety floor. Callers should persist ResetAt
// and skip that endpoint until ResetAt passes.
type WebRateLimitedError struct {
	Endpoint  WebEndpointName
	ResetAt   time.Time
	Limit     int
	Remaining int
	Proactive bool
}

func (e *WebRateLimitedError) Error() string {
	if e == nil {
		return "X web rate limit reached"
	}
	if !e.ResetAt.IsZero() {
		return fmt.Sprintf("X web %s rate limit reached until %s", e.Endpoint, e.ResetAt.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("X web %s rate limit reached", e.Endpoint)
}

// WebSessionAuthError signals that X rejected the session cookies (401/403 or
// viewer verification failure). Callers should trip the session circuit breaker
// until the user reconnects.
type WebSessionAuthError struct {
	Message string
}

func (e *WebSessionAuthError) Error() string {
	if e == nil || strings.TrimSpace(e.Message) == "" {
		return "X web session expired or rejected"
	}
	return e.Message
}

func parseRateLimitHeaders(endpoint WebEndpointName, headers http.Header, statusCode int, now time.Time, fallbackCooldown time.Duration) RateLimitObservation {
	obs := RateLimitObservation{
		Endpoint:    endpoint,
		ObservedAt:  now.UTC(),
		RateLimited: statusCode == http.StatusTooManyRequests,
	}
	if rawLimit := strings.TrimSpace(headers.Get("x-rate-limit-limit")); rawLimit != "" {
		if n, err := strconv.Atoi(rawLimit); err == nil && n >= 0 {
			obs.Limit = n
			obs.HasQuota = true
		}
	}
	if rawRemaining := strings.TrimSpace(headers.Get("x-rate-limit-remaining")); rawRemaining != "" {
		if n, err := strconv.Atoi(rawRemaining); err == nil && n >= 0 {
			obs.Remaining = n
			obs.HasQuota = true
		}
	}
	if rawReset := strings.TrimSpace(headers.Get("x-rate-limit-reset")); rawReset != "" {
		if epoch, err := strconv.ParseInt(rawReset, 10, 64); err == nil && epoch > 0 {
			obs.ResetAt = time.Unix(epoch, 0).UTC()
		}
	}
	if obs.ResetAt.IsZero() {
		if retryAfter := parseRetryAfterHeader(headers.Get("retry-after"), now); !retryAfter.IsZero() {
			obs.ResetAt = retryAfter
		}
	}
	if obs.RateLimited && obs.ResetAt.IsZero() {
		obs.ResetAt = now.UTC().Add(fallbackCooldown)
	}
	return obs
}

func parseRetryAfterHeader(raw string, now time.Time) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
		return now.UTC().Add(time.Duration(seconds) * time.Second)
	}
	if parsed, err := http.ParseTime(raw); err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}

func jitterDuration(maxJitter time.Duration) time.Duration {
	if maxJitter <= 0 {
		return 0
	}
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return maxJitter / 2
	}
	n := binary.LittleEndian.Uint64(buf[:])
	return time.Duration(n % uint64(maxJitter))
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
