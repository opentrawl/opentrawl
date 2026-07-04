package harness

import (
	"context"
	"encoding/json"
	"strings"
)

const humanProbeQuery = "conformance-human-probe-zzqx"

// CheckHumanReadable trips when a verb's human mode leaks machine output.
// Human output is a first-class contract surface (docs/contract.md); a verb
// that prints JSON without --json, or prints nothing at all, fails cold
// readers and agents alike.
func (s Suite) CheckHumanReadable(ctx context.Context) CheckResult {
	probes := [][]string{
		{"status"},
		{"doctor"},
		{"search", humanProbeQuery, "--limit", "1"},
	}
	for _, args := range probes {
		out := s.Runner.Run(ctx, args...)
		verb := strings.Join(args, " ")
		if out.TimedOut {
			return fail(CheckHumanReadable, verb+" timed out in human mode", "make human mode return promptly")
		}
		text := strings.TrimSpace(string(out.Stdout))
		if text == "" {
			return fail(CheckHumanReadable, verb+" printed nothing in human mode", "always print a human sentence, even for zero results")
		}
		if looksLikeJSON(text) {
			return fail(CheckHumanReadable, verb+" printed JSON in human mode", "render human output by default; JSON only behind --json")
		}
	}
	return pass(CheckHumanReadable, "status, doctor and search speak human without --json")
}

func looksLikeJSON(text string) bool {
	if !strings.HasPrefix(text, "{") && !strings.HasPrefix(text, "[") {
		return false
	}
	var value any
	return json.Unmarshal([]byte(text), &value) == nil
}
