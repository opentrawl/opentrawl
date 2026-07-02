package cli

import (
	"strings"

	"github.com/openclaw/imsgcrawl/internal/archive"
)

var searchValueFlags = map[string]bool{
	"limit": true,
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

func newSearchListOutput(query string, items []archive.SearchResult, total int64, limit int) searchListOutput {
	results := make([]searchResultOutput, 0, len(items))
	for _, item := range items {
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
		TotalMatches: total,
		Truncated:    total > int64(len(results)),
		Limit:        limit,
		TextItems:    items,
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
	if title != "" && !isMachineChatTitle(title) {
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
	if chat.ChatID != "" {
		return "chat " + chat.ChatID
	}
	return "unknown chat"
}
