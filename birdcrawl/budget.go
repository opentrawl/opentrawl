package birdcrawl

import (
	"strings"
	"time"
)

func liveSyncPausedSentence(month string) string {
	return "Live sync is paused: the monthly X API budget is spent; it resumes " + formatHumanDate(nextSpendMonthStart(month)) + "."
}

func monthlyBudgetSpentMessage(month string) string {
	return "monthly X API budget is spent; live sync resumes " + formatHumanDate(nextSpendMonthStart(month))
}

func nextSpendMonthStart(month string) time.Time {
	t, err := time.Parse("2006-01", strings.TrimSpace(month))
	if err != nil {
		t = time.Now().UTC()
	}
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local).AddDate(0, 1, 0)
}

func formatHumanDate(t time.Time) string {
	return t.Format("2 January 2006")
}

func appendSentence(base, sentence string) string {
	base = strings.TrimSpace(base)
	sentence = strings.TrimSpace(sentence)
	if base == "" {
		return sentence
	}
	if sentence == "" {
		return base
	}
	return strings.TrimRight(base, ".") + ". " + sentence
}
