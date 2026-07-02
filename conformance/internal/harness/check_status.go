package harness

import (
	"context"
	"fmt"
)

const maxStatusCounts = 4

var allowedStatusStates = map[string]bool{
	"ok":      true,
	"stale":   true,
	"empty":   true,
	"error":   true,
	"missing": true,
}

func (s Suite) CheckStatus(ctx context.Context) (CheckResult, StatusInfo) {
	out := s.Runner.Run(ctx, "status", "--json")
	if !out.OK() {
		return fail(CheckStatus, out.FailureDetail(), "make status --json return the status envelope"), StatusInfo{}
	}
	status, err := decodeJSONObject(out.Stdout)
	if err != nil {
		return fail(CheckStatus, "status --json did not return valid JSON", "emit one JSON object from status --json"), StatusInfo{}
	}
	info := StatusInfo{Value: status, Valid: true}
	state, ok := stringField(status, "state")
	if !ok || !allowedStatusStates[state] {
		return fail(CheckStatus, "status state is missing or outside ok, stale, empty, error, missing", "set state to one of ok, stale, empty, error, missing"), info
	}
	warning := ""
	if result := validateCounts(status); result.Status == StatusFail {
		return result, info
	} else if result.Status == StatusWarn {
		warning = result.Detail
	}
	if result := validateAuth(status); result.Status == StatusFail {
		return result, info
	}
	if warning != "" {
		return warn(CheckStatus, warning), info
	}
	return pass(CheckStatus, fmt.Sprintf("state is %s", state)), info
}

func validateCounts(status map[string]any) CheckResult {
	value, ok := status["counts"]
	if !ok || value == nil {
		return pass(CheckStatus, "status has no counts")
	}
	counts, ok := value.([]any)
	if !ok {
		return fail(CheckStatus, "status counts is not an array", "emit counts as an array of id, label and value objects")
	}
	for index, item := range counts {
		count, ok := item.(map[string]any)
		if !ok {
			return fail(CheckStatus, fmt.Sprintf("status count %d is not an object", index+1), "emit each count as an object with id, label and value")
		}
		for _, field := range []string{"id", "label", "value"} {
			if !fieldPresent(count, field) {
				return fail(CheckStatus, fmt.Sprintf("status count %d is missing %s", index+1, field), "emit each count with id, label and value")
			}
		}
	}
	if len(counts) > maxStatusCounts {
		return warn(CheckStatus, fmt.Sprintf("status has %d counts; use at most %d headline counts", len(counts), maxStatusCounts))
	}
	return pass(CheckStatus, "counts are shaped correctly")
}

func validateAuth(status map[string]any) CheckResult {
	value, ok := status["auth"]
	if !ok || value == nil {
		return pass(CheckStatus, "status has no auth")
	}
	auth, ok := value.(map[string]any)
	if !ok {
		return fail(CheckStatus, "status auth is not an object", "emit auth as booleans plus optional expires")
	}
	for key, authValue := range auth {
		if key == "expires" {
			if authValue == nil {
				continue
			}
			if _, ok := authValue.(string); ok {
				continue
			}
			return fail(CheckStatus, "auth expires is not a string or null", "emit auth.expires as an RFC 3339 string or null")
		}
		if _, ok := authValue.(bool); !ok {
			return fail(CheckStatus, fmt.Sprintf("auth %s is %s, not boolean", key, jsonType(authValue)), "emit auth state as booleans and expiry only")
		}
	}
	return pass(CheckStatus, "auth is shaped correctly")
}
