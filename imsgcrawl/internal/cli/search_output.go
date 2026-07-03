package cli

import (
	"strings"

	"github.com/openclaw/imsgcrawl/internal/archive"
)

var searchValueFlags = map[string]bool{
	"limit":  true,
	"after":  true,
	"before": true,
	"who":    true,
}

func splitSearchFlagArgs(args []string) (flags, query []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			query = append(query, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			name := strings.TrimLeft(arg, "-")
			if !strings.Contains(name, "=") && searchValueFlags[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		query = append(query, arg)
	}
	return flags, query
}

func newSearchListOutput(query string, page archive.SearchPage, limit int, filters searchOutputFilters) searchListOutput {
	results := make([]searchResultOutput, 0, len(page.Items))
	for _, item := range page.Items {
		results = append(results, searchResultOutput{
			Ref:     messageRef(item.MessageID),
			Time:    item.Time,
			Who:     outputField(senderName(item.FromMe, item.SenderLabel)),
			Where:   outputField(searchChatDisplayName(item)),
			Snippet: outputField(searchSnippet(item)),
		})
	}
	return searchListOutput{
		Query:        query,
		Results:      results,
		TotalMatches: page.Total,
		Truncated:    page.Total > int64(len(results)),
		Limit:        limit,
		Who:          strings.TrimSpace(filters.WhoQuery),
		WhoResolved:  newWhoResolvedOutput(filters.WhoResolved),
		After:        strings.TrimSpace(filters.After),
		Before:       strings.TrimSpace(filters.Before),
		TextItems:    page.Items,
	}
}

type searchOutputFilters struct {
	WhoQuery    string
	WhoResolved *archive.WhoCandidate
	After       string
	Before      string
}

func newWhoResolvedOutput(candidate *archive.WhoCandidate) *whoResolvedOutput {
	if candidate == nil {
		return nil
	}
	return &whoResolvedOutput{
		Who:         candidate.Who,
		Identifiers: append([]string{}, candidate.Identifiers...),
	}
}

func searchSnippet(item archive.SearchResult) string {
	if snippet := strings.TrimSpace(item.Snippet); snippet != "" {
		return displayMessageText(snippet, item.HasAttachments)
	}
	return searchText(item)
}

func searchChatDisplayName(item archive.SearchResult) string {
	chat := archive.ChatSummary{
		ChatID:             item.ChatID,
		Title:              item.ChatTitle,
		Kind:               item.ChatKind,
		ParticipantCount:   item.ChatParticipantCount,
		ParticipantHandles: item.ChatParticipantHandles,
	}
	return chatDisplayName(chat)
}

func chatDisplayName(chat archive.ChatSummary) string {
	title := strings.TrimSpace(chat.Title)
	if chat.Kind != "group" && participantPreview(chat.ParticipantHandles, chat.ParticipantCount) == "me" {
		return "me"
	}
	if title != "" && !isMachineChatTitle(title) && !isHandleLikeTitle(title) {
		return title
	}
	if chat.Kind == "group" {
		if people := participantPreview(chat.ParticipantHandles, chat.ParticipantCount); people != "" {
			return "group with " + people
		}
		return "group chat"
	}
	if people := participantPreview(chat.ParticipantHandles, chat.ParticipantCount); people != "" {
		return people
	}
	if title != "" && !isMachineChatTitle(title) {
		return title
	}
	if chat.ChatID != "" {
		return "chat " + chat.ChatID
	}
	return "unknown chat"
}
