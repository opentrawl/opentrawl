package twitter

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/twitter/internal/store"
)

func parseStatsWindow(value string) (store.StatsWindow, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return store.StatsWindow{Duration: 30 * 24 * time.Hour}, nil
	}
	lower := strings.ToLower(value)
	if lower == "all" {
		return store.StatsWindow{AllTime: true}, nil
	}
	for _, unit := range []struct {
		suffix string
		span   time.Duration
	}{
		{suffix: "d", span: 24 * time.Hour},
		{suffix: "w", span: 7 * 24 * time.Hour},
		{suffix: "y", span: 365 * 24 * time.Hour},
	} {
		if strings.HasSuffix(lower, unit.suffix) {
			count, err := strconv.Atoi(strings.TrimSuffix(lower, unit.suffix))
			if err != nil || count <= 0 {
				return store.StatsWindow{}, fmt.Errorf("invalid window %q", value)
			}
			return store.StatsWindow{Duration: time.Duration(count) * unit.span}, nil
		}
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return store.StatsWindow{}, fmt.Errorf("invalid window %q", value)
	}
	return store.StatsWindow{Duration: parsed}, nil
}

// formatLocalTime renders timestamps in the machine's local offset.
func formatLocalTime(t time.Time) string {
	return t.Local().Format(time.RFC3339)
}
