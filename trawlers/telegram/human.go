package telecrawl

import (
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func shortLocalTime(t time.Time) string {
	return render.ShortLocalTime(t)
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
