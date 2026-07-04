package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/control"
	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/crawlkit/render"
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
	case archive.WhoResolution:
		return printWhoText(r.stdout, value)
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
	fields := []render.CardField{
		{Label: "ID", Value: value.ID},
		{Label: "Version", Value: value.Version},
		{Label: "Database", Value: value.Paths.DefaultDatabase},
		{Label: "Logs", Value: value.Paths.DefaultLogs},
	}
	if len(value.Capabilities) > 0 {
		fields = append(fields, render.CardField{Label: "Capabilities", Value: strings.Join(value.Capabilities, ", ")})
	}
	return render.WriteCard(w, render.Card{
		Title:  value.DisplayName + " (" + value.ID + ")",
		Fields: fields,
		Body:   value.Description,
		Hints:  []string{"JSON: imsgcrawl metadata --json"},
	})
}

func printSyncText(w io.Writer, value archive.SyncResult) error {
	_, err := fmt.Fprintf(w, "Sync complete\n\nMessages source:\n  Database: %s\n  Modified: %s\n  Size: %d bytes\n\nLocal archive:\n  Database: %s\n  Synced: %s\n\nArchived rows:\n  Handles: %d\n  Chats: %d\n  Participants: %d\n  Chat-message links: %d\n  Messages: %d\n",
		value.SourcePath, emptyDash(value.SourceModifiedAt), value.SourceBytes, value.ArchivePath, value.SyncedAt, value.Handles, value.Chats, value.Participants, value.ChatMessages, value.Messages)
	return err
}

func printStatusText(w io.Writer, value statusOutput) error {
	return render.WriteStatus(w, render.Status{
		State:     render.StatusState(value.State),
		Summary:   value.Summary,
		Sections:  statusRenderSections(value),
		Freshness: statusRenderFreshness(value.Freshness),
		Log:       renderLogTail(value.Log),
		Warnings:  value.Warnings,
		Errors:    value.Errors,
	})
}

func statusRenderSections(value statusOutput) []render.Section {
	var sections []render.Section
	if value.Source != nil {
		sections = append(sections, render.Section{
			Title: "Messages source",
			Fields: []render.Field{
				{Label: "Database", Value: value.Source.DatabasePath},
				{Label: "Handles", Value: strconv.FormatInt(value.Source.Handles, 10)},
				{Label: "Chats", Value: strconv.FormatInt(value.Source.Chats, 10)},
				{Label: "Messages", Value: strconv.FormatInt(value.Source.Messages, 10)},
			},
		})
	}
	if value.Archive != nil {
		sections = append(sections, render.Section{
			Title: "Local archive",
			Fields: []render.Field{
				{Label: "Database", Value: value.Archive.ArchivePath},
				{Label: "Last sync", Value: value.Archive.LastSyncAt},
				{Label: "Handles", Value: strconv.FormatInt(value.Archive.Handles, 10)},
				{Label: "Named contacts", Value: strconv.FormatInt(value.Archive.NamedContacts, 10)},
				{Label: "Chats", Value: strconv.FormatInt(value.Archive.Chats, 10)},
				{Label: "Participants", Value: strconv.FormatInt(value.Archive.Participants, 10)},
				{Label: "Chat-message links", Value: strconv.FormatInt(value.Archive.ChatMessages, 10)},
				{Label: "Messages", Value: strconv.FormatInt(value.Archive.Messages, 10)},
			},
		})
	}
	return sections
}

func statusRenderFreshness(value *statusFreshness) *render.Freshness {
	if value == nil {
		return nil
	}
	return &render.Freshness{LastSync: value.LastSync}
}

func renderLogTail(value *logTailOutput) render.LogTail {
	if value == nil {
		return render.LogTail{}
	}
	out := render.LogTail{}
	if value.LastRun != nil {
		out.LastRun = &cklog.RunSummary{
			RunID:      value.LastRun.RunID,
			Command:    value.LastRun.Command,
			StartedAt:  parseLogTime(value.LastRun.StartedAt),
			FinishedAt: parseLogTime(value.LastRun.FinishedAt),
			Outcome:    value.LastRun.Outcome,
			LastEvent:  value.LastRun.LastEvent,
		}
	}
	if value.MostRecentError != nil {
		out.MostRecentError = &cklog.Line{
			RunID:     value.MostRecentError.RunID,
			Command:   value.MostRecentError.Command,
			Event:     value.MostRecentError.Event,
			Message:   value.MostRecentError.Message,
			Timestamp: parseLogTime(value.MostRecentError.Timestamp),
		}
	}
	return out
}

func parseLogTime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

func printDoctorText(w io.Writer, value doctorOutput) error {
	checks := make([]render.Check, 0, len(value.Checks))
	for _, check := range value.Checks {
		checks = append(checks, render.Check{
			Name:    check.ID,
			State:   render.CheckState(check.State),
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return render.WriteDoctor(w, checks, value.logTail)
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
	if len(value.Items) == 0 {
		_, err := io.WriteString(w, "No chats yet. Run imsgcrawl sync.\n")
		return err
	}
	rows := make([][]string, 0, len(value.Items))
	for _, item := range value.Items {
		rows = append(rows, []string{
			item.ChatID,
			item.Kind,
			strconv.FormatInt(item.MessageCount, 10),
			shortArchiveTime(archive.FormatAppleDateTime(item.LatestMessageDate)),
			chatConversation(item),
		})
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "chat"},
		{Header: "kind"},
		{Header: "msgs", AlignRight: true},
		{Header: "latest"},
		{Header: "conversation", Wrap: true},
	}, rows)
}

func printMessagesText(w io.Writer, value messageListOutput) error {
	conversation := "chat " + value.ChatID
	if value.Chat != nil {
		conversation = chatConversation(*value.Chat)
	}
	heading := fmt.Sprintf("Messages in %s (chat %s): showing %d of %d, %s.", conversation, value.ChatID, value.Returned, value.Total, value.Order)
	var hints []string
	if !value.Complete {
		hints = append(hints,
			fmt.Sprintf("More: imsgcrawl messages --chat %s --limit %d", value.ChatID, nextLimit(value.Limit, value.Total)),
			fmt.Sprintf("All: imsgcrawl messages --chat %s --all", value.ChatID),
		)
	}
	hints = append(hints, "Search: imsgcrawl search QUERY")
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

func printOpenText(w io.Writer, value openOutput) error {
	span := openDateSpan(value.Context)
	title := value.Chat.Name
	if span != "" {
		title += ", " + span
	}
	for _, line := range render.WrapWithIndent("Transcript: ", title, render.OutputWidth(w), "") {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "Ref: %s\n", value.Ref); err != nil {
		return err
	}
	if len(value.Chat.Participants) > 0 {
		if _, err := fmt.Fprintf(w, "Participants: %s\n", strings.Join(value.Chat.Participants, ", ")); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\nTime: %s\nFrom: %s\n", formatArchiveTime(value.Message.Time), value.Message.Who); err != nil {
		return err
	}
	if err := render.WriteWrappedField(w, "Text", displayMessageText(value.Message.Text, value.Message.HasAttachments)); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Context: %d messages around this one.\n\n", len(value.Context)); err != nil {
		return err
	}
	return printOpenTranscript(w, value.Context)
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

func parseArchiveTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return t
}

// shortArchiveTime formats an archived RFC3339 timestamp as short local time
// for table cells; empty or unparseable values render as an empty cell.
func shortArchiveTime(value string) string {
	return render.ShortLocalTime(parseArchiveTime(value))
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
