package harness

import (
	"context"
	"fmt"
)

var openProbeQueries = []string{"test", "a", "the"}

func (s Suite) CheckOpen(ctx context.Context, metadata MetadataInfo, status StatusInfo) CheckResult {
	if !metadata.Valid {
		return warn(CheckOpen, "open was not checked because metadata did not parse")
	}
	if !hasCapability(metadata.Capabilities, "search") {
		return warn(CheckOpen, "metadata does not declare search, so open was not checked")
	}
	if state := statusState(status); state == "missing" || state == "empty" {
		return warn(CheckOpen, "archive state is "+state+", so open was not checked")
	}
	if !hasCapability(metadata.Capabilities, "open") {
		return fail(CheckOpen, "metadata declares search but not open", "declare and implement the open verb")
	}
	ref, ok, failure := s.findSearchRef(ctx)
	if failure.Status == StatusFail {
		return failure
	}
	if !ok {
		return warn(CheckOpen, "open could not be probed because search returned no rows")
	}
	out := s.Runner.Run(ctx, "open", ref, "--json")
	if !out.OK() {
		return fail(CheckOpen, out.FailureDetail(), "make open expand refs issued by search")
	}
	if _, err := decodeJSONObject(out.Stdout); err != nil {
		return fail(CheckOpen, "open did not return a single JSON object", "make open expand refs issued by search")
	}
	return pass(CheckOpen, fmt.Sprintf("open expanded %s", ref))
}

func (s Suite) findSearchRef(ctx context.Context) (string, bool, CheckResult) {
	for _, query := range openProbeQueries {
		out := s.runSearchProbe(ctx, query)
		if !out.OK() {
			continue
		}
		search, err := decodeJSONObject(out.Stdout)
		if err != nil {
			continue
		}
		rows, ok := searchResultRows(search)
		if !ok || len(rows) == 0 {
			continue
		}
		ref, failure := firstSearchRef(rows[0])
		if failure.Status == StatusFail {
			return "", false, failure
		}
		return ref, true, CheckResult{}
	}
	return "", false, CheckResult{}
}

func firstSearchRef(item any) (string, CheckResult) {
	row, ok := item.(map[string]any)
	if !ok {
		return "", fail(CheckOpen, "search returned a non-object row for open", "emit search rows as objects with refs")
	}
	value, ok := row["ref"]
	if !ok || value == nil {
		return "", fail(CheckOpen, "search returned a row without a ref for open", "emit refs from search so open can expand them")
	}
	ref, ok := value.(string)
	if !ok || ref == "" {
		return "", fail(CheckOpen, "search returned a non-string ref for open", "emit refs from search so open can expand them")
	}
	return ref, CheckResult{}
}
