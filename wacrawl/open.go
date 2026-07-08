package wacrawl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/crawlkit/shortref"
	"github.com/openclaw/wacrawl/internal/store"
)

type openEnvelope struct {
	Ref     string            `json:"ref"`
	Chat    string            `json:"chat"`
	Message openMessage       `json:"message"`
	Context []openMessage     `json:"context"`
	Window  openWindowSummary `json:"window"`
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
	messageID, err := resolveOpenMessageID(ctx, st, ref)
	if err != nil {
		return err
	}
	target, err := st.MessageByID(ctx, messageID)
	if err != nil {
		if errorsIsNoRows(err) {
			return commandErr(1, "not_found", "message was not found", "run trawl wacrawl search again and pass one of its refs")
		}
		return err
	}
	window, err := st.MessageWindow(ctx, target, openWindowEachSide)
	if err != nil {
		return err
	}
	result := newOpenEnvelope(target, window)
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "open", result)
	}
	return printOpen(req, result)
}

func resolveOpenMessageID(ctx context.Context, st *store.Store, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		return parseMessageRef(ref)
	}
	if !shortref.ValidAlias(ref) {
		return "", unknownShortRefError()
	}
	fullRefs, err := st.ResolveShortRef(ctx, ref)
	if err != nil {
		return "", err
	}
	switch len(fullRefs) {
	case 0:
		return "", unknownShortRefError()
	case 1:
		return parseMessageRef(fullRefs[0])
	default:
		return "", commandErr(1, "ambiguous_short_ref", "short ref matches more than one message", "rerun trawl wacrawl search or use the full ref")
	}
}

func unknownShortRefError() error {
	return commandErr(1, "unknown_short_ref", "short ref was not found", "use a full ref from trawl wacrawl search")
}

func parseMessageRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, messageRefPrefix) {
		return "", commandErr(1, "foreign_ref", "ref does not belong to wacrawl", "pass a ref returned by trawl wacrawl search")
	}
	messageID := strings.TrimSpace(strings.TrimPrefix(ref, messageRefPrefix))
	if messageID == "" {
		return "", commandErr(1, "invalid_ref", "wacrawl message ref is missing its message id", "pass a complete ref returned by trawl wacrawl search")
	}
	return messageID, nil
}

func printOpen(req *crawlkit.Request, result openEnvelope) error {
	if _, err := fmt.Fprintf(req.Out, "chat: %s\nref: %s\n\n", result.Chat, result.Ref); err != nil {
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
		when = parsed.Format("2006-01-02 15:04")
	}
	fixed := fmt.Sprintf("%s  %s  ", marker, when)
	whoWidth := width - render.DisplayWidth(fixed) - render.DisplayWidth(": ") - 1
	if whoWidth < 8 {
		whoWidth = 8
	}
	if whoWidth > 32 {
		whoWidth = 32
	}
	return fixed + render.Truncate(item.Who, whoWidth) + ": "
}

func newOpenEnvelope(target store.Message, context []store.Message) openEnvelope {
	openContext := make([]openMessage, 0, len(context))
	before := 0
	after := 0
	for _, message := range context {
		current := message.SourcePK == target.SourcePK
		if current {
			openContext = append(openContext, newOpenMessage(message, true))
			continue
		}
		if message.Timestamp.Before(target.Timestamp) || (message.Timestamp.Equal(target.Timestamp) && message.SourcePK < target.SourcePK) {
			before++
		} else {
			after++
		}
		openContext = append(openContext, newOpenMessage(message, false))
	}
	return openEnvelope{
		Ref:     messageRef(target),
		Chat:    messageWhere(target),
		Message: newOpenMessage(target, true),
		Context: openContext,
		Window:  openWindowSummary{Before: before, After: after},
	}
}

func newOpenMessage(message store.Message, current bool) openMessage {
	return openMessage{
		Ref:     messageRef(message),
		Time:    formatTime(message.Timestamp),
		Who:     outputField(messageWho(message)),
		Where:   outputField(messageWhere(message)),
		Text:    messageText(message),
		Type:    messageKind(message),
		Media:   messageMedia(message),
		Starred: message.Starred,
		Current: current,
	}
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
