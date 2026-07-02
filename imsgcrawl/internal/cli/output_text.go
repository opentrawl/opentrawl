package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/imsgcrawl/internal/archive"
)

func (r *runtime) print(v any) error {
	enc := json.NewEncoder(r.stdout)
	if r.json {
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	switch value := v.(type) {
	case control.Manifest:
		return printManifestText(r.stdout, value)
	case archive.SyncResult:
		return printSyncText(r.stdout, value)
	case statusOutput:
		return printStatusText(r.stdout, value)
	case doctorOutput:
		return printDoctorText(r.stdout, value)
	case chatListOutput:
		return printChatsText(r.stdout, value)
	case messageListOutput:
		return printMessagesText(r.stdout, value)
	case searchListOutput:
		return printSearchText(r.stdout, value)
	case openOutput:
		return printOpenText(r.stdout, value)
	case control.ContactExport:
		return printContactsText(r.stdout, value)
	default:
		return enc.Encode(v)
	}
}

func printManifestText(w io.Writer, value control.Manifest) error {
	if _, err := fmt.Fprintf(w, "%s (%s)\n", value.DisplayName, value.ID); err != nil {
		return err
	}
	if value.Description != "" {
		if _, err := fmt.Fprintf(w, "%s\n", value.Description); err != nil {
			return err
		}
	}
	if len(value.Capabilities) > 0 {
		if _, err := fmt.Fprintf(w, "\nCapabilities: %s\n", strings.Join(value.Capabilities, ", ")); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "\nAgent-facing commands:\n"); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, name := range []string{"metadata", "status", "sync", "doctor", "chats", "messages", "search", "open", "contact-export"} {
		command, ok := value.Commands[name]
		if !ok {
			continue
		}
		if _, err := fmt.Fprintf(tw, "  %s\t%s\n", name, strings.Join(command.Argv, " ")); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\nMachine output: add --json to print the structured manifest.\n")
	return err
}

func printSyncText(w io.Writer, value archive.SyncResult) error {
	_, err := fmt.Fprintf(w, "Sync complete\n\nMessages source:\n  Database: %s\n  Modified: %s\n  Size: %d bytes\n\nLocal archive:\n  Database: %s\n  Synced: %s\n\nArchived rows:\n  Handles: %d\n  Chats: %d\n  Participants: %d\n  Chat-message links: %d\n  Messages: %d\n",
		value.SourcePath, emptyDash(value.SourceModifiedAt), value.SourceBytes, value.ArchivePath, value.SyncedAt, value.Handles, value.Chats, value.Participants, value.ChatMessages, value.Messages)
	return err
}

func printStatusText(w io.Writer, value statusOutput) error {
	if _, err := fmt.Fprintf(w, "Status: %s\n%s\n", value.State, value.Summary); err != nil {
		return err
	}
	if value.Source != nil {
		if _, err := fmt.Fprintf(w, "\nMessages source:\n  Database: %s\n  Handles: %d\n  Chats: %d\n  Messages: %d\n", value.Source.DatabasePath, value.Source.Handles, value.Source.Chats, value.Source.Messages); err != nil {
			return err
		}
	}
	if value.Archive != nil {
		if _, err := fmt.Fprintf(w, "\nLocal archive:\n  Database: %s\n  Last sync: %s\n  Handles: %d\n  Named contacts: %d\n  Chats: %d\n  Participants: %d\n  Chat-message links: %d\n  Messages: %d\n", value.Archive.ArchivePath, emptyDash(value.Archive.LastSyncAt), value.Archive.Handles, value.Archive.NamedContacts, value.Archive.Chats, value.Archive.Participants, value.Archive.ChatMessages, value.Archive.Messages); err != nil {
			return err
		}
	}
	if value.Freshness != nil {
		if _, err := fmt.Fprintf(w, "\nFreshness:\n  Last sync: %s\n", value.Freshness.LastSync); err != nil {
			return err
		}
	}
	if len(value.Warnings) > 0 {
		if _, err := io.WriteString(w, "\nWarnings:\n"); err != nil {
			return err
		}
		for _, warning := range value.Warnings {
			if _, err := fmt.Fprintf(w, "  - %s\n", warning); err != nil {
				return err
			}
		}
	}
	if len(value.Errors) > 0 {
		if _, err := io.WriteString(w, "\nErrors:\n"); err != nil {
			return err
		}
		for _, msg := range value.Errors {
			if _, err := fmt.Fprintf(w, "  - %s\n", msg); err != nil {
				return err
			}
		}
	}
	return printLogTailText(w, value.Log)
}

func printDoctorText(w io.Writer, value doctorOutput) error {
	if _, err := io.WriteString(w, "Doctor checks:\n"); err != nil {
		return err
	}
	for _, check := range value.Checks {
		line := fmt.Sprintf("  %s: %s", check.ID, check.State)
		if check.Message != "" {
			line += " - " + check.Message
		}
		if _, err := io.WriteString(w, line+"\n"); err != nil {
			return err
		}
		if check.Remedy != "" {
			if _, err := fmt.Fprintf(w, "    Remedy: %s\n", check.Remedy); err != nil {
				return err
			}
		}
	}
	return printLogTailText(w, value.Log)
}

func printChatsText(w io.Writer, value chatListOutput) error {
	if _, err := fmt.Fprintf(w, "Chats: showing %d of %d, newest first.\n", value.Returned, value.Total); err != nil {
		return err
	}
	if !value.Complete {
		if _, err := fmt.Fprintf(w, "More: imsgcrawl chats --limit %d\nAll: imsgcrawl chats --all\n", nextLimit(value.Limit, value.Total)); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "Open: imsgcrawl messages --chat CHAT_ID\n\n"); err != nil {
		return err
	}
	width := textOutputWidth(w)
	columns := chatTextColumns(width)
	rows := tableRows(len(value.Items))
	for _, item := range value.Items {
		rows = append(rows, []string{
			item.ChatID,
			item.Kind,
			strconv.FormatInt(item.MessageCount, 10),
			formatAppleDate(item.LatestMessageDate),
			chatConversation(item),
		})
	}
	return renderTextTable(w, columns, rows)
}

func printMessagesText(w io.Writer, value messageListOutput) error {
	conversation := "chat " + value.ChatID
	if value.Chat != nil {
		conversation = chatConversation(*value.Chat)
	}
	if _, err := fmt.Fprintf(w, "Messages in %s (chat %s): showing %d of %d, %s.\n", conversation, value.ChatID, value.Returned, value.Total, value.Order); err != nil {
		return err
	}
	if !value.Complete {
		if _, err := fmt.Fprintf(w, "More: imsgcrawl messages --chat %s --limit %d\nAll: imsgcrawl messages --chat %s --all\n", value.ChatID, nextLimit(value.Limit, value.Total), value.ChatID); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "Search: imsgcrawl search QUERY\n\n"); err != nil {
		return err
	}
	width := textOutputWidth(w)
	columns := messageTextColumns(width)
	rows := tableRows(len(value.Items))
	for _, item := range value.Items {
		rows = append(rows, []string{
			formatArchiveTime(item.Time),
			senderName(item.FromMe, item.SenderLabel),
			displayMessageText(item.Text, item.HasAttachments),
		})
	}
	return renderTextTable(w, columns, rows)
}

func printSearchText(w io.Writer, value searchListOutput) error {
	returned := len(value.Results)
	if _, err := fmt.Fprintf(w, "Search %q: showing %d of %d.\n", value.Query, returned, value.TotalMatches); err != nil {
		return err
	}
	if len(value.WhoMatched) > 0 {
		if _, err := fmt.Fprintf(w, "Who matched: %s.\n", strings.Join(value.WhoMatched, ", ")); err != nil {
			return err
		}
	}
	if value.Truncated {
		if value.Limit < maxListLimit {
			if _, err := fmt.Fprintf(w, "More: imsgcrawl search --limit %d", nextSearchLimit(value.Limit, value.TotalMatches)); err != nil {
				return err
			}
			if value.Who != "" {
				if _, err := fmt.Fprintf(w, " --who %s", strconv.Quote(value.Who)); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(w, " %s\n", strconv.Quote(value.Query)); err != nil {
				return err
			}
		} else if _, err := io.WriteString(w, "Narrow the query to see more matches.\n"); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "Open: imsgcrawl open REF\nUse --json when you need refs for follow-up commands.\n\n"); err != nil {
		return err
	}
	width := textOutputWidth(w)
	columns := searchTextColumns(width)
	rows := tableRows(len(value.TextItems))
	for _, item := range value.TextItems {
		rows = append(rows, []string{
			formatArchiveTime(item.Time),
			senderName(item.FromMe, item.SenderLabel),
			searchDisplayRef(item),
			searchConversation(item),
			outputField(searchSnippet(item)),
		})
	}
	return renderTextTable(w, columns, rows)
}

func printOpenText(w io.Writer, value openOutput) error {
	if _, err := fmt.Fprintf(w, "Message %s in %s\n", value.Ref, value.Chat.Name); err != nil {
		return err
	}
	if len(value.Chat.Participants) > 0 {
		if _, err := fmt.Fprintf(w, "Participants: %s\n", strings.Join(value.Chat.Participants, ", ")); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\nTime: %s\nFrom: %s\nText: %s\n\n", formatArchiveTime(value.Message.Time), value.Message.Who, displayMessageText(value.Message.Text, value.Message.HasAttachments)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Context: %d messages around this one.\n\n", len(value.Context)); err != nil {
		return err
	}
	width := textOutputWidth(w)
	columns := openTextColumns(width)
	rows := tableRows(len(value.Context))
	for _, item := range value.Context {
		marker := ""
		if item.Target {
			marker = ">"
		}
		rows = append(rows, []string{
			marker,
			formatArchiveTime(item.Time),
			item.Who,
			displayMessageText(item.Text, item.HasAttachments),
		})
	}
	return renderTextTable(w, columns, rows)
}

func printContactsText(w io.Writer, value control.ContactExport) error {
	for _, contact := range value.Contacts {
		_, err := fmt.Fprintf(w, "%s\t%s\n", contact.DisplayName, strings.Join(contact.PhoneNumbers, ","))
		if err != nil {
			return err
		}
	}
	return nil
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

func nextSearchLimit(limit int, total int64) int {
	next := nextLimit(limit, total)
	if next > maxListLimit {
		return maxListLimit
	}
	return next
}

func formatAppleDate(value int64) string {
	return formatArchiveTime(archive.FormatAppleDateTime(value))
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

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func searchDisplayRef(item archive.SearchResult) string {
	if item.ShortRef != "" {
		return item.ShortRef
	}
	return messageRef(item.MessageID)
}

func printLogTailText(w io.Writer, value *logTailOutput) error {
	if value == nil {
		return nil
	}
	if _, err := io.WriteString(w, "\nLog:\n"); err != nil {
		return err
	}
	if value.LastRun != nil {
		if _, err := fmt.Fprintf(w, "  Last run: %s %s", value.LastRun.Command, value.LastRun.Outcome); err != nil {
			return err
		}
		if value.LastRun.FinishedAt != "" {
			if _, err := fmt.Fprintf(w, " at %s", value.LastRun.FinishedAt); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	if value.MostRecentError != nil {
		if _, err := fmt.Fprintf(w, "  Most recent error: %s %s\n", value.MostRecentError.Command, value.MostRecentError.Event); err != nil {
			return err
		}
	}
	return nil
}
