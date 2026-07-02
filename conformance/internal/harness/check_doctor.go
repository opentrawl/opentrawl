package harness

import (
	"context"
	"fmt"
	"strings"
)

func (s Suite) CheckDoctor(ctx context.Context) CheckResult {
	out := s.Runner.Run(ctx, "doctor", "--json")
	if !out.OK() {
		return fail(CheckDoctor, out.FailureDetail(), "make doctor --json return diagnostic checks")
	}
	doctor, err := decodeJSONObject(out.Stdout)
	if err != nil {
		return fail(CheckDoctor, "doctor --json did not return valid JSON", "emit one JSON object from doctor --json")
	}
	checks, ok := arrayField(doctor, "checks")
	if !ok {
		return fail(CheckDoctor, "doctor checks is missing or not an array", "emit doctor checks as a JSON array")
	}
	for index, item := range checks {
		check, ok := item.(map[string]any)
		if !ok {
			return fail(CheckDoctor, fmt.Sprintf("doctor check %d is not an object", index+1), "emit each doctor check as an object")
		}
		state, _ := stringField(check, "state")
		if state == "ok" {
			continue
		}
		remedy, _ := stringField(check, "remedy")
		if strings.TrimSpace(remedy) == "" {
			return fail(CheckDoctor, fmt.Sprintf("doctor check %d is not ok and has no remedy", index+1), "add a one-line remedy to every non-ok doctor check")
		}
	}
	return pass(CheckDoctor, fmt.Sprintf("%d doctor checks inspected", len(checks)))
}
