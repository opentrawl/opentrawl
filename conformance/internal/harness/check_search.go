package harness

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const searchProbeLimit = 3

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
	out := s.Runner.Run(ctx, "search", "test", "--json", "--limit", strconv.Itoa(searchProbeLimit))
	if !out.OK() {
		return fail(CheckSearch, out.FailureDetail(), "make search test --json --limit 3 return bounded JSON")
	}
	search, err := decodeJSONObject(out.Stdout)
	if err != nil {
		return fail(CheckSearch, "search did not return valid JSON", "emit one JSON object from search --json")
	}
	rows, ok := searchResultRows(search)
	if !ok {
		return fail(CheckSearch, "search JSON has no results or items array", "emit bounded search rows in a results array")
	}
	if len(rows) > searchProbeLimit {
		return fail(CheckSearch, fmt.Sprintf("search returned %d rows for limit %d", len(rows), searchProbeLimit), "apply --limit before writing search results")
	}
	if value, ok := search["truncated"]; ok {
		if _, ok := value.(bool); !ok {
			return fail(CheckSearch, "search truncated is not boolean", "emit truncated as true or false")
		}
	}
	if failure := validateSearchRowTimes(rows); failure.Status == StatusFail {
		return failure
	}
	if warning := missingSearchFields(rows); warning != "" {
		return warn(CheckSearch, warning)
	}
	return pass(CheckSearch, fmt.Sprintf("search returned %d rows within limit", len(rows)))
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
	if rows, ok := arrayField(search, "items"); ok {
		return rows, true
	}
	return nil, false
}

func validateSearchRowTimes(rows []any) CheckResult {
	for index, item := range rows {
		row, ok := item.(map[string]any)
		if !ok {
			return fail(CheckSearch, fmt.Sprintf("search row %d is not an object", index+1), "emit each search result as an object")
		}
		value, ok := row["time"]
		if !ok || value == nil {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return fail(CheckSearch, fmt.Sprintf("search row %d time is not a string", index+1), "emit result time as RFC 3339")
		}
		if _, err := time.Parse(time.RFC3339Nano, text); err != nil {
			return fail(CheckSearch, fmt.Sprintf("search row %d time is not RFC 3339", index+1), "emit result time as RFC 3339")
		}
	}
	return pass(CheckSearch, "search times are valid")
}

func missingSearchFields(rows []any) string {
	missingRef := false
	missingTime := false
	for _, item := range rows {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if !fieldPresent(row, "ref") {
			missingRef = true
		}
		if !fieldPresent(row, "time") {
			missingTime = true
		}
	}
	switch {
	case missingRef && missingTime:
		return "one or more search rows are missing ref and time"
	case missingRef:
		return "one or more search rows are missing ref"
	case missingTime:
		return "one or more search rows are missing time"
	default:
		return ""
	}
}
