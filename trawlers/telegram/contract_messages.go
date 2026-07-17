package telecrawl

import (
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
)

func whoCandidates(candidates []store.WhoCandidate) []whoCandidate {
	out := make([]whoCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, whoCandidate{
			Who:         outputField(candidate.Who),
			Identifiers: append([]string(nil), candidate.Identifiers...),
			LastSeen:    formatOptionalTime(candidate.LastSeen),
			Messages:    candidate.Messages,
		})
	}
	return out
}

func messageRef(sourcePK int64) string {
	return store.MessageRef(sourcePK)
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return formatLocalTime(t)
}

func formatLocalTime(t time.Time) string {
	return t.Local().Format(time.RFC3339)
}

func parseRenderTime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func messageWho(message store.Message) string {
	if message.FromMe {
		return "me"
	}
	if value := strings.TrimSpace(message.SenderName); value != "" {
		return outputField(value)
	}
	if strings.TrimSpace(message.SenderJID) == "" || strings.TrimSpace(message.SenderJID) == strings.TrimSpace(message.ChatJID) {
		return outputField(messageWhere(message))
	}
	return ""
}

func messageWhere(message store.Message) string {
	if value := strings.TrimSpace(message.ChatName); value != "" {
		return outputField(value)
	}
	return "Telegram chat"
}

func messageWhereForList(message store.Message) string {
	where := messageWhere(message)
	if where == messageWho(message) {
		return "direct"
	}
	return where
}

func messageSnippet(message store.Message) string {
	if value := strings.TrimSpace(message.Snippet); value != "" {
		return outputField(value)
	}
	return outputField(message.Text)
}

func outputField(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
