package telecrawl

import (
	"fmt"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/telecrawl/internal/store"
	"github.com/openclaw/telecrawl/internal/telegramdesktop"
)

func shortLocalTime(t time.Time) string {
	return render.ShortLocalTime(t)
}

func (r *runtime) printManifest(value control.Manifest) error {
	return render.WriteCard(r.stdout, render.Card{
		Title: "Telegram crawler",
		Fields: []render.CardField{
			{Label: "ID", Value: value.ID},
			{Label: "Version", Value: value.Version},
			{Label: "Database", Value: value.Paths.DefaultDatabase},
			{Label: "Logs", Value: value.Paths.DefaultLogs},
		},
		Hints: []string{"JSON: trawl telegram metadata --json"},
	})
}

func (r *runtime) printChats(value chatsEnvelope) error {
	if _, err := fmt.Fprintf(r.stdout, "Chats: showing %s of %s, newest first.\n", groupDigits(len(value.Chats)), groupDigits(value.Total)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.stdout, "Messages: trawl telegram messages --chat ID"); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(r.stdout); err != nil {
		return err
	}
	if len(value.Chats) == 0 {
		_, err := fmt.Fprintln(r.stdout, "No chats.")
		return err
	}
	rows := make([][]string, 0, len(value.Chats))
	for _, chat := range value.Chats {
		rows = append(rows, []string{
			shortLocalTime(chat.LastMessageAt),
			chat.JID,
			chat.Kind,
			render.FormatInteger(int64(chat.UnreadCount)),
			chatMessageCount(chat),
			chatName(chat),
		})
	}
	return render.WriteTable(r.stdout, []render.TableColumn{
		{Header: "last"},
		{Header: "chat"},
		{Header: "kind"},
		{Header: "unread", AlignRight: true},
		{Header: "messages", AlignRight: true},
		{Header: "name", Wrap: true},
	}, rows)
}

func (r *runtime) printTopics(value topicsEnvelope) error {
	if len(value.Topics) == 0 {
		_, err := fmt.Fprintln(r.stdout, "No topics: this chat has no forum topics.")
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "Topics: showing %s of %s.\n", groupDigits(len(value.Topics)), groupDigits(value.Total)); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(r.stdout); err != nil {
		return err
	}
	rows := make([][]string, 0, len(value.Topics))
	for _, topic := range value.Topics {
		rows = append(rows, []string{
			shortLocalTime(topic.LastMessageAt),
			render.FormatInteger(int64(topic.UnreadCount)),
			topic.TopicID,
			topic.Title,
		})
	}
	return render.WriteTable(r.stdout, []render.TableColumn{
		{Header: "last"},
		{Header: "unread", AlignRight: true},
		{Header: "topic"},
		{Header: "title", Wrap: true},
	}, rows)
}

func (r *runtime) printMessages(value messagesEnvelope) error {
	hints := []string{"Open: trawl telegram open REF"}
	if value.Total > len(value.Messages) {
		hints = append(hints, "Narrow: trawl telegram messages --chat ID --after DATE --before DATE")
	}
	return render.WriteList(r.stdout, render.List{
		Heading:   fmt.Sprintf("Messages: showing %s of %s, newest first.", groupDigits(len(value.Messages)), groupDigits(value.Total)),
		Hints:     hints,
		Items:     messageListItems(value.Messages, value.ShortRefs),
		ClampText: 0,
		Empty:     "No messages.",
	})
}

func (r *runtime) printContacts(value contactsEnvelope) error {
	if len(value.Contacts) == 0 {
		_, err := fmt.Fprintln(r.stdout, "No contacts.")
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "Contacts: showing %s of %s, A to Z.\n", groupDigits(len(value.Contacts)), groupDigits(value.Total)); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(r.stdout); err != nil {
		return err
	}
	rows := make([][]string, 0, len(value.Contacts))
	for _, contact := range value.Contacts {
		rows = append(rows, []string{
			contactDisplayName(contact),
			cleanTelegramUsername(contact.Username),
			render.FormatPhone(strings.TrimSpace(contact.Phone)),
		})
	}
	return render.WriteTable(r.stdout, []render.TableColumn{
		{Header: "name", Wrap: true},
		{Header: "username"},
		{Header: "phone"},
	}, rows)
}

func (r *runtime) printFolders(value foldersEnvelope) error {
	if len(value.Folders) == 0 {
		_, err := fmt.Fprintln(r.stdout, "No folders.")
		return err
	}
	rows := make([][]string, 0, len(value.Folders))
	for _, folder := range value.Folders {
		rows = append(rows, []string{
			folder.ID,
			folder.Title,
			render.FormatInteger(int64(folder.ChatCount)),
		})
	}
	return render.WriteTable(r.stdout, []render.TableColumn{
		{Header: "id"},
		{Header: "title", Wrap: true},
		{Header: "count", AlignRight: true},
	}, rows)
}

func chatName(chat store.Chat) string {
	if name := strings.TrimSpace(chat.Name); name != "" {
		return name
	}
	if username := cleanTelegramUsername(chat.Username); username != "" {
		return username
	}
	return "Telegram chat"
}

func chatMessageCount(chat store.Chat) string {
	if chat.MessageCount == telegramdesktop.DefaultMessagesLimit {
		return groupDigits(chat.MessageCount) + "+"
	}
	return groupDigits(chat.MessageCount)
}

func messageListItems(messages []store.Message, shortRefs map[string]string) []render.ListItem {
	items := make([]render.ListItem, 0, len(messages))
	for _, message := range messages {
		ref := messageRef(message.SourcePK)
		who := messageWho(message)
		items = append(items, render.ListItem{
			Time:  message.Timestamp,
			Who:   who,
			Where: messageWhereForList(message),
			Ref:   ref,
			Text:  messageText(message),
		})
	}
	return items
}

func messageText(message store.Message) string {
	if text := strings.TrimSpace(message.Text); text != "" {
		return text
	}
	switch {
	case strings.TrimSpace(message.MediaTitle) != "":
		return "[" + strings.TrimSpace(message.MediaTitle) + "]"
	case strings.TrimSpace(message.MediaType) != "":
		return "[" + strings.TrimSpace(message.MediaType) + "]"
	default:
		return "[empty message]"
	}
}
