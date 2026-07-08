package wacrawl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/wacrawl/internal/store"
)

type openEnvelope struct {
	Ref          string            `json:"ref"`
	Chat         string            `json:"chat"`
	Participants []string          `json:"participants,omitempty"`
	Message      openMessage       `json:"message"`
	Context      []openMessage     `json:"context"`
	Window       openWindowSummary `json:"window"`
}

type openWindowSummary struct {
	Before int `json:"before"`
	After  int `json:"after"`
}

type openMessage struct {
	Ref     string     `json:"ref"`
	Time    string     `json:"time"`
	Who     string     `json:"who"`
	Where   string     `json:"where"`
	Text    string     `json:"text"`
	Type    string     `json:"type,omitempty"`
	Media   *openMedia `json:"media,omitempty"`
	Starred bool       `json:"starred,omitempty"`
	Current bool       `json:"current,omitempty"`
}

type openMedia struct {
	Type      string `json:"type,omitempty"`
	Title     string `json:"title,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

func (c *Crawler) Open(ctx context.Context, req *crawlkit.Request, ref string) error {
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	messageID, err := c.resolveOpenMessageID(ctx, req, ref)
	if err != nil {
		return err
	}
	target, err := st.MessageByID(ctx, messageID)
	if err != nil {
		if errorsIsNoRows(err) {
			return commandErr(1, "not_found", "message was not found", "run trawl whatsapp search again and pass one of its refs")
		}
		return err
	}
	window, err := st.MessageWindow(ctx, target, openWindowEachSide)
	if err != nil {
		return err
	}
	participants, err := st.GroupParticipants(ctx, target.ChatJID)
	if err != nil {
		return err
	}
	result := newOpenEnvelope(target, window, participants, req.Format)
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "open", result)
	}
	return printOpen(req, result)
}

func (c *Crawler) resolveOpenMessageID(ctx context.Context, req *crawlkit.Request, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		return parseMessageRef(ref)
	}
	fullRefs, err := req.ResolveShortRef(ctx, ref)
	if errors.Is(err, crawlkit.ErrUnknownShortRef) {
		return "", unknownShortRefError()
	}
	if errors.Is(err, crawlkit.ErrAmbiguousShortRef) {
		return "", commandErr(1, "ambiguous_short_ref", "short ref matches more than one message", "rerun trawl whatsapp search or use the full ref")
	}
	if err != nil {
		return "", err
	}
	if len(fullRefs) != 1 {
		return "", unknownShortRefError()
	}
	return parseMessageRef(fullRefs[0])
}

func unknownShortRefError() error {
	return commandErr(1, "unknown_short_ref", "short ref was not found", "use a full ref from trawl whatsapp search")
}

func parseMessageRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, messageRefPrefix) {
		if !strings.HasPrefix(ref, store.LegacyMessageRefPrefix) {
			return "", commandErr(1, "foreign_ref", "ref does not belong to whatsapp", "pass a ref returned by trawl whatsapp search")
		}
		messageID := strings.TrimSpace(strings.TrimPrefix(ref, store.LegacyMessageRefPrefix))
		if messageID == "" {
			return "", commandErr(1, "invalid_ref", "whatsapp message ref is missing its message id", "pass a complete ref returned by trawl whatsapp search")
		}
		return messageID, nil
	}
	messageID := strings.TrimSpace(strings.TrimPrefix(ref, messageRefPrefix))
	if messageID == "" {
		return "", commandErr(1, "invalid_ref", "whatsapp message ref is missing its message id", "pass a complete ref returned by trawl whatsapp search")
	}
	return messageID, nil
}

func printOpen(req *crawlkit.Request, result openEnvelope) error {
	title := result.Chat
	if span := openDateSpan(result.Context); span != "" {
		title += ", " + span
	}
	if err := render.WriteTranscriptHeader(req.Out, render.TranscriptHeader{
		Title:        title,
		Ref:          result.Ref,
		Participants: result.Participants,
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(req.Out, "\nContext: %s messages around this one.\n\n", render.FormatInteger(int64(len(result.Context)))); err != nil {
		return err
	}
	width := render.OutputWidth(req.Out)
	rows := make([]render.TranscriptRow, 0, len(result.Context))
	for _, item := range result.Context {
		rows = append(rows, render.TranscriptRow{
			Time:   parseFormattedTime(item.Time),
			Prefix: openTranscriptPrefix(width, item),
			Text:   item.Text,
		})
	}
	return render.WriteTranscript(req.Out, rows)
}

func openTranscriptPrefix(width int, item openMessage) string {
	marker := " "
	if item.Current {
		marker = ">"
	}
	when := item.Time
	if parsed := parseFormattedTime(item.Time); !parsed.IsZero() {
		when = parsed.Format("15:04")
	}
	fixed := fmt.Sprintf("%s %s  ", marker, when)
	whoWidth := width - render.DisplayWidth(fixed) - render.DisplayWidth(": ") - 1
	if whoWidth < 8 {
		whoWidth = 8
	}
	if whoWidth > 32 {
		whoWidth = 32
	}
	return fixed + render.Truncate(render.HumanIdentity(item.Who), whoWidth) + ": "
}

func openDateSpan(context []openMessage) string {
	var first time.Time
	var last time.Time
	for _, item := range context {
		t := parseFormattedTime(item.Time)
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

func newOpenEnvelope(target store.Message, context []store.Message, participants []string, format output.Format) openEnvelope {
	openContext := make([]openMessage, 0, len(context))
	before := 0
	after := 0
	for _, message := range context {
		current := message.SourcePK == target.SourcePK
		if current {
			openContext = append(openContext, newOpenMessage(message, true, format))
			continue
		}
		if message.Timestamp.Before(target.Timestamp) || (message.Timestamp.Equal(target.Timestamp) && message.SourcePK < target.SourcePK) {
			before++
		} else {
			after++
		}
		openContext = append(openContext, newOpenMessage(message, false, format))
	}
	return openEnvelope{
		Ref:          messageRef(target),
		Chat:         messageWhereForFormat(target, format),
		Participants: participantsForFormat(participants, format),
		Message:      newOpenMessage(target, true, format),
		Context:      openContext,
		Window:       openWindowSummary{Before: before, After: after},
	}
}

func participantsForFormat(participants []string, format output.Format) []string {
	if format == output.JSON {
		return append([]string(nil), participants...)
	}
	out := make([]string, 0, len(participants))
	for _, participant := range participants {
		label := humanParticipantLabel(participant)
		if label == "" {
			continue
		}
		out = append(out, label)
	}
	return out
}

func newOpenMessage(message store.Message, current bool, format output.Format) openMessage {
	return openMessage{
		Ref:     messageRef(message),
		Time:    formatTime(message.Timestamp),
		Who:     outputField(messageWhoForFormat(message, format)),
		Where:   outputField(messageWhereForFormat(message, format)),
		Text:    messageText(message),
		Type:    messageKind(message),
		Media:   messageMedia(message),
		Starred: message.Starred,
		Current: current,
	}
}

func messageWhoForFormat(message store.Message, format output.Format) string {
	if format == output.JSON {
		return messageWhoJSON(message)
	}
	return messageWho(message)
}

func messageWhereForFormat(message store.Message, format output.Format) string {
	if format == output.JSON {
		return messageWhereJSON(message)
	}
	return messageWhere(message)
}

func messageMedia(message store.Message) *openMedia {
	kind := ""
	if messageCarriesMedia(message) {
		kind = messageKind(message)
	} else {
		kind = normalizeMessageKind(message.MediaType)
	}
	title := safeMediaTitle(message)
	if kind == "" && title == "" && message.MediaSize == 0 {
		return nil
	}
	return &openMedia{Type: kind, Title: title, SizeBytes: message.MediaSize}
}

func errorsIsNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
