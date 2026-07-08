package gogcrawl

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/whomatch"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

func searchHit(hit archive.SearchHit) (crawlkit.Hit, error) {
	t, err := parseContractTime(hit.Time)
	if err != nil {
		return crawlkit.Hit{}, err
	}
	return crawlkit.Hit{
		Ref:      hit.Ref,
		ShortRef: hit.ShortRef,
		Time:     t,
		Who:      hit.Who,
		Where:    hit.Where,
		Snippet:  hit.Snippet,
	}, nil
}

func whoCandidate(candidate archive.WhoCandidate) whomatch.Candidate {
	lastSeen, _ := parseContractTime(candidate.LastSeen)
	return whomatch.Candidate{
		Who:         candidate.Who,
		Identifiers: append([]string(nil), candidate.Identifiers...),
		LastSeen:    lastSeen,
		Messages:    candidate.Messages,
	}
}

func parseContractTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid archive time %q", value)
	}
	return t, nil
}

func versionAtLeast(raw, minimum string) bool {
	got := parseVersion(raw)
	want := parseVersion(minimum)
	for i := 0; i < len(want); i++ {
		if got[i] > want[i] {
			return true
		}
		if got[i] < want[i] {
			return false
		}
	}
	return true
}

func parseVersion(raw string) [3]int {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "v")
	if before, _, ok := strings.Cut(raw, " "); ok {
		raw = before
	}
	parts := strings.Split(raw, ".")
	var out [3]int
	for i := 0; i < len(out) && i < len(parts); i++ {
		value, _ := strconv.Atoi(strings.TrimFunc(parts[i], func(r rune) bool {
			return r < '0' || r > '9'
		}))
		out[i] = value
	}
	return out
}
