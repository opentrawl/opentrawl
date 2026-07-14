package archive

import (
	"fmt"
	"strings"
)

func parseVenuePlausibility(value string) venuePlausibility {
	lines := []string{}
	for _, raw := range strings.Split(value, "\n") {
		line := strings.TrimSpace(stripListMarker(raw))
		if line != "" {
			lines = append(lines, line)
		}
	}
	plausibility := venuePlausibility{}
	for _, line := range lines {
		key, field, ok := strings.Cut(line, ":")
		if ok {
			switch strings.ToLower(strings.Join(strings.Fields(strings.Trim(key, "`*_ ")), " ")) {
			case "candidate_id", "candidate id", "id":
				plausibility.CandidateID = cleanCardCandidateID(field)
				continue
			case "verdict", "decision", "answer", "plausibility", "venue plausibility", "assessment":
				if verdict, err := normalizeVenueVerdict(field); err == nil {
					plausibility.Verdict = verdict
				}
				continue
			case "reason", "rationale", "why":
				plausibility.Reason = truncateReason(field)
				continue
			}
		}
		if plausibility.Verdict == "" {
			if verdict, reason, ok := inlineVenueVerdict(line); ok {
				plausibility.Verdict = verdict
				if plausibility.Reason == "" {
					plausibility.Reason = reason
				}
			}
		}
	}
	if plausibility.Verdict == "" {
		if verdict, reason, ok := inlineVenueVerdict(cleanMultiline(value)); ok {
			plausibility.Verdict = verdict
			plausibility.Reason = reason
		}
	}
	if plausibility.Verdict == "" {
		if verdict, ok := containedVenueVerdict(value); ok {
			plausibility.Verdict = verdict
		}
	}
	plausibility.Reason = truncateReason(plausibility.Reason)
	return plausibility
}

func cleanCardCandidateID(value string) string {
	return strings.TrimSpace(value)
}

func validateVenueCandidate(prepared preparedCardRequest, plausibility *venuePlausibility) error {
	if plausibility == nil {
		return fmt.Errorf("%w: missing venue_plausibility", errUnknownCardCandidate)
	}
	id := plausibility.CandidateID
	if id == "none" {
		plausibility.CandidateID = id
		return nil
	}
	if id == "" {
		return fmt.Errorf("%w: missing candidate_id", errUnknownCardCandidate)
	}
	if _, ok := prepared.CandidateByID[id]; !ok {
		return fmt.Errorf("%w: %s", errUnknownCardCandidate, id)
	}
	plausibility.CandidateID = id
	return nil
}

func normalizeVenueVerdict(value string) (string, error) {
	value = strings.ToLower(strings.Trim(strings.Join(strings.Fields(value), " "), " ."))
	if verdict, _, ok := inlineVenueVerdict(value); ok {
		return verdict, nil
	}
	if verdict, ok := containedVenueVerdict(value); ok {
		return verdict, nil
	}
	switch value {
	case venueVerdictCorroborated, venueVerdictPlausible, venueVerdictInconsistent:
		return value, nil
	default:
		return "", fmt.Errorf("%w: venue plausibility has unknown verdict %q", errModelCardParse, value)
	}
}

func containedVenueVerdict(value string) (string, bool) {
	lower := strings.ToLower(cleanMultiline(value))
	matches := []string{}
	for _, verdict := range []string{venueVerdictCorroborated, venueVerdictPlausible, venueVerdictInconsistent} {
		if strings.Contains(lower, verdict) {
			matches = append(matches, verdict)
		}
	}
	if len(matches) != 1 {
		return "", false
	}
	return matches[0], true
}

func inlineVenueVerdict(value string) (string, string, bool) {
	value = strings.TrimSpace(cleanMultiline(value))
	lower := strings.ToLower(value)
	for _, verdict := range []string{venueVerdictCorroborated, venueVerdictPlausible, venueVerdictInconsistent} {
		if lower == verdict {
			return verdict, "", true
		}
		for _, separator := range []string{":", " -", " —", " --", " because "} {
			prefix := verdict + separator
			if strings.HasPrefix(lower, prefix) {
				reason := strings.TrimSpace(value[len(prefix):])
				return verdict, truncateReason(reason), true
			}
		}
	}
	return "", "", false
}

func truncateReason(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= 200 {
		return value
	}
	return strings.TrimSpace(value[:200])
}
