package imsgcrawl

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

const (
	objectReplacementCharacter = "\uFFFC"
	openTranscriptMinWhoWidth  = 8
	openTranscriptMaxWhoWidth  = 32
)

func printMessagesText(w io.Writer, value messageListOutput) error {
	conversation := "chat " + value.ChatID
	if value.Chat != nil {
		conversation = chatConversation(*value.Chat)
	}
	heading := fmt.Sprintf("Messages in %s (chat %s): showing %s of %s, %s.", conversation, value.ChatID, render.FormatInteger(int64(value.Returned)), render.FormatInteger(value.Total), value.Order)
	var hints []string
	if !value.Complete {
		hints = append(hints,
			fmt.Sprintf("More: trawl imessage messages --chat %s --limit %d", value.ChatID, nextLimit(value.Limit, value.Total)),
		)
	}
	hints = append(hints, "Search: trawl imessage search QUERY")
	items := make([]render.ListItem, 0, len(value.Items))
	for _, item := range value.Items {
		items = append(items, render.ListItem{
			Time: parseArchiveTime(item.Time),
			Who:  senderName(item.FromMe, item.SenderLabel),
			Text: displayMessageText(item.Text, item.HasAttachments),
		})
	}
	return render.WriteList(w, render.List{
		Heading: heading,
		Hints:   hints,
		Items:   items,
		Empty:   fmt.Sprintf("No messages in chat %s.", value.ChatID),
	})
}

func printOpenTranscript(w io.Writer, context []openMessageOutput) error {
	width := render.OutputWidth(w)
	rows := make([]render.TranscriptRow, 0, len(context))
	for _, item := range context {
		rows = append(rows, render.TranscriptRow{
			Time:   parseArchiveTime(item.Time),
			Prefix: openTranscriptPrefix(width, item),
			Text:   displayMessageText(item.Text, item.HasAttachments),
		})
	}
	return render.WriteTranscript(w, rows)
}

func openTranscriptPrefix(width int, item openMessageOutput) string {
	marker := " "
	if item.Target {
		marker = ">"
	}
	when := "--:--"
	if t := parseArchiveTime(item.Time); !t.IsZero() {
		when = t.Local().Format("15:04")
	}
	fixed := fmt.Sprintf("%s %s  ", marker, when)
	whoWidth := width - render.DisplayWidth(fixed) - render.DisplayWidth(": ") - 1
	if whoWidth < openTranscriptMinWhoWidth {
		whoWidth = openTranscriptMinWhoWidth
	}
	if whoWidth > openTranscriptMaxWhoWidth {
		whoWidth = openTranscriptMaxWhoWidth
	}
	return fixed + render.Truncate(render.HumanIdentity(item.Who), whoWidth) + ": "
}

func openDateSpan(context []openMessageOutput) string {
	var first time.Time
	var last time.Time
	for _, item := range context {
		t := parseArchiveTime(item.Time)
		if t.IsZero() {
			continue
		}
		if first.IsZero() {
			first = t
		}
		last = t
	}
	if first.IsZero() {
		return ""
	}
	if sameTranscriptDate(first, last) {
		return first.Format("2 Jan 2006")
	}
	return first.Format("2 Jan 2006") + " to " + last.Format("2 Jan 2006")
}

func sameTranscriptDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func senderName(fromMe bool, label string) string {
	if fromMe {
		return "me"
	}
	label = strings.TrimSpace(label)
	if label != "" && label != "them" {
		return render.HumanIdentity(label)
	}
	return "them"
}

func searchSnippet(item archive.SearchResult) string {
	if snippet := strings.TrimSpace(item.Snippet); snippet != "" {
		return displayMessageText(snippet, item.HasAttachments)
	}
	return searchText(item)
}

func searchText(item archive.SearchResult) string {
	if item.Text != "" {
		return displayMessageText(item.Text, item.HasAttachments)
	}
	if item.Snippet != "" {
		return displayMessageText(item.Snippet, item.HasAttachments)
	}
	if item.HasAttachments {
		return "(attachment)"
	}
	return ""
}

func displayMessageText(text string, hasAttachments bool) string {
	if hasAttachments && strings.TrimSpace(strings.ReplaceAll(text, objectReplacementCharacter, "")) == "" {
		return "(attachment)"
	}
	return strings.ReplaceAll(text, objectReplacementCharacter, "[attachment]")
}

func outputField(value string) string {
	return strings.Join(strings.Fields(value), " ")
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

func chatConversation(item archive.ChatSummary) string {
	title := strings.TrimSpace(item.Title)
	if isMachineChatTitle(title) {
		title = ""
	}
	people := participantPreview(item.ParticipantHandles, item.ParticipantCount)
	if item.Kind != "group" && people == "me" {
		return "me"
	}
	if item.Kind == "group" {
		switch {
		case title != "" && people != "":
			return title + " (" + people + ")"
		case title != "":
			return title
		case people != "":
			return "group with " + people
		default:
			return "group chat"
		}
	}
	if title != "" && !isHandleLikeTitle(title) {
		return title
	}
	if people != "" {
		return people
	}
	if title != "" {
		return title
	}
	if item.ChatID != "" {
		return "chat " + item.ChatID
	}
	return "unknown chat"
}

func isMachineChatTitle(title string) bool {
	title = strings.ToLower(strings.TrimSpace(title))
	if len(title) >= 8 && strings.HasPrefix(title, "chat") && allRunes(title[4:], unicode.IsDigit) {
		return true
	}
	if len(title) >= 16 && allRunes(title, isHexRune) {
		return true
	}
	return false
}

func allRunes(value string, match func(rune) bool) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !match(r) {
			return false
		}
	}
	return true
}

func isHexRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
}

func isHandleLikeTitle(title string) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return false
	}
	if strings.Contains(title, "@") {
		return true
	}
	return looksPhoneLikeTitle(title)
}

func looksPhoneLikeTitle(value string) bool {
	hasDigit := false
	for _, r := range strings.TrimSpace(value) {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '+', r == ' ', r == '\t', r == '(', r == ')', r == '-', r == '.':
			continue
		default:
			return false
		}
	}
	return hasDigit
}

func participantPreview(handles []string, total int64) string {
	if len(handles) == 0 {
		if total > 0 {
			return fmt.Sprintf("%s people", render.FormatInteger(total))
		}
		return ""
	}
	limit := len(handles)
	if limit > 4 {
		limit = 4
	}
	parts := make([]string, 0, limit+1)
	for _, handle := range handles[:limit] {
		parts = append(parts, render.HumanIdentity(handle))
	}
	if remaining := int(total) - limit; remaining > 0 {
		parts = append(parts, fmt.Sprintf("+%s more", render.FormatInteger(int64(remaining))))
	}
	return strings.Join(parts, ", ")
}

func nextLimit(limit int, total int64) int {
	if limit <= 0 {
		return int(total)
	}
	next := limit * 2
	if int64(next) > total {
		return int(total)
	}
	return next
}

func parseArchiveTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return t
}

func formatArchiveTime(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return t.Format("2006-01-02 15:04")
}
