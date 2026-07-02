package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const searchProbeLimit = 3
const searchProbeQuery = "test"

func statusState(status StatusInfo) string {
	if !status.Valid {
		return ""
	}
	state, _ := status.Value["state"].(string)
	return state
}

func (s Suite) CheckSearch(ctx context.Context, metadata MetadataInfo, status StatusInfo) CheckResult {
	if !metadata.Valid {
		return warn(CheckSearch, "search was not checked because metadata did not parse")
	}
	if !hasCapability(metadata.Capabilities, "search") {
		return warn(CheckSearch, "metadata does not declare search, so bounded search was not checked")
	}
	if state := statusState(status); state == "missing" || state == "empty" {
		return warn(CheckSearch, "archive state is "+state+", so bounded search was not checked")
	}
	out := s.runSearchProbe(ctx, searchProbeQuery)
	if !out.OK() {
		return fail(CheckSearch, out.FailureDetail(), "make search test --json --limit 3 return bounded JSON")
	}
	search, err := decodeJSONObject(out.Stdout)
	if err != nil {
		return fail(CheckSearch, "search did not return valid JSON", "emit one JSON object from search --json")
	}
	rows, ok := searchResultRows(search)
	if !ok {
		return fail(CheckSearch, "search results is missing or not an array", "emit bounded search rows in a results array")
	}
	if failure := validateSearchEnvelope(search, searchProbeQuery, len(rows)); failure.Status == StatusFail {
		return failure
	}
	if len(rows) > searchProbeLimit {
		return fail(CheckSearch, fmt.Sprintf("search returned %d rows for limit %d", len(rows), searchProbeLimit), "apply --limit before writing search results")
	}
	if failure := validateSearchRows(rows, metadata.AppID); failure.Status == StatusFail {
		return failure
	}
	return pass(CheckSearch, fmt.Sprintf("search returned %d rows within limit", len(rows)))
}

func (s Suite) runSearchProbe(ctx context.Context, query string) CommandOutput {
	return s.Runner.Run(ctx, "search", query, "--json", "--limit", strconv.Itoa(searchProbeLimit))
}

func hasCapability(capabilities []string, want string) bool {
	for _, capability := range capabilities {
		if strings.EqualFold(strings.TrimSpace(capability), want) {
			return true
		}
	}
	return false
}

func searchResultRows(search map[string]any) ([]any, bool) {
	if rows, ok := arrayField(search, "results"); ok {
		return rows, true
	}
	return nil, false
}

func validateSearchEnvelope(search map[string]any, query string, resultCount int) CheckResult {
	if failure := validateSearchQuery(search, query); failure.Status == StatusFail {
		return failure
	}
	if failure := validateTotalMatches(search, resultCount); failure.Status == StatusFail {
		return failure
	}
	value, ok := search["truncated"]
	if !ok {
		return fail(CheckSearch, "search truncated is missing", "emit truncated as true or false")
	}
	if _, ok := value.(bool); !ok {
		return fail(CheckSearch, "search truncated is not boolean", "emit truncated as true or false")
	}
	return pass(CheckSearch, "search envelope is shaped correctly")
}

func validateSearchQuery(search map[string]any, want string) CheckResult {
	value, ok := search["query"]
	if !ok {
		return fail(CheckSearch, "search query is missing", "echo the query string in query")
	}
	query, ok := value.(string)
	if !ok {
		return fail(CheckSearch, "search query is not a string", "echo the query string in query")
	}
	if query != want {
		return fail(CheckSearch, fmt.Sprintf("search query is %q, want %q", query, want), "echo only the query text in query")
	}
	return pass(CheckSearch, "search query matches")
}

func validateTotalMatches(search map[string]any, resultCount int) CheckResult {
	value, ok := search["total_matches"]
	if !ok {
		return fail(CheckSearch, "search total_matches is missing", "emit total_matches as a JSON number")
	}
	total, ok := jsonNumber(value)
	if !ok {
		return fail(CheckSearch, "search total_matches is not a number", "emit total_matches as a JSON number")
	}
	if total < float64(resultCount) {
		return fail(CheckSearch, "search total_matches is less than results length", "make total_matches at least the number of returned results")
	}
	return pass(CheckSearch, "search total_matches is valid")
}

func jsonNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case float64:
		return typed, true
	default:
		return 0, false
	}
}

func validateSearchRows(rows []any, appID string) CheckResult {
	for index, item := range rows {
		row, ok := item.(map[string]any)
		if !ok {
			return fail(CheckSearch, fmt.Sprintf("search row %d is not an object", index+1), "emit each search result as an object")
		}
		if failure := validateSearchRowRef(row, index, appID); failure.Status == StatusFail {
			return failure
		}
		if failure := validateSearchRowTime(row, index); failure.Status == StatusFail {
			return failure
		}
		if failure := validateOptionalSearchFields(row, index); failure.Status == StatusFail {
			return failure
		}
	}
	return pass(CheckSearch, "search rows are shaped correctly")
}

func validateSearchRowRef(row map[string]any, index int, appID string) CheckResult {
	value, ok := row["ref"]
	if !ok || value == nil {
		return fail(CheckSearch, fmt.Sprintf("search row %d ref is missing", index+1), "emit each result with a stable ref")
	}
	ref, ok := value.(string)
	if !ok {
		return fail(CheckSearch, fmt.Sprintf("search row %d ref is not a string", index+1), "emit each result ref as a string")
	}
	prefix := appID + ":"
	if !strings.HasPrefix(ref, prefix) {
		return fail(CheckSearch, fmt.Sprintf("search row %d ref does not start with %s", index+1, prefix), "prefix each result ref with the crawler id and colon")
	}
	return pass(CheckSearch, "search ref is valid")
}

func validateSearchRowTime(row map[string]any, index int) CheckResult {
	value, ok := row["time"]
	if !ok || value == nil {
		return fail(CheckSearch, fmt.Sprintf("search row %d time is missing", index+1), "emit result time as RFC 3339")
	}
	text, ok := value.(string)
	if !ok {
		return fail(CheckSearch, fmt.Sprintf("search row %d time is not a string", index+1), "emit result time as RFC 3339")
	}
	if _, err := time.Parse(time.RFC3339Nano, text); err != nil {
		return fail(CheckSearch, fmt.Sprintf("search row %d time is not RFC 3339", index+1), "emit result time as RFC 3339")
	}
	return pass(CheckSearch, "search time is valid")
}

func validateOptionalSearchFields(row map[string]any, index int) CheckResult {
	for _, field := range []string{"who", "where", "snippet"} {
		value, ok := row[field]
		if !ok {
			continue
		}
		if _, ok := value.(string); !ok {
			return fail(CheckSearch, fmt.Sprintf("search row %d %s is not a string", index+1, field), "emit who, where and snippet as strings when present")
		}
	}
	return pass(CheckSearch, "optional search fields are valid")
}
