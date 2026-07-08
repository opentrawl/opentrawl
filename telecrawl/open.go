package telecrawl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit"
	ckoutput "github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/telecrawl/internal/store"
)

const (
	openTranscriptMinWhoWidth = 8
	openTranscriptMaxWhoWidth = 32
)

func (c *Crawler) Open(ctx context.Context, req *crawlkit.Request, ref string) error {
	r := c.handler(ctx, req)
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	sourcePK, err := r.resolveOpenMessageRef(st, ref)
	if err != nil {
		return err
	}
	window, err := st.OpenMessageWindow(ctx, sourcePK, openContextRadius)
	if errors.Is(err, store.ErrMessageNotFound) {
		return r.contractError("not_found", "message was not found in this archive", "Run telecrawl search --json again and use one of the returned refs.")
	}
	if err != nil {
		return err
	}
	envelope := newOpenEnvelope(window)
	if req.Format == ckoutput.JSON {
		return ckoutput.Write(req.Out, req.Format, "open", envelope)
	}
	return r.printOpen(envelope)
}

func (r *runtime) resolveOpenMessageRef(st *store.Store, ref string) (int64, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		sourcePK, err := parseMessageRef(ref)
		if err != nil {
			return 0, r.contractError("invalid_ref", "ref is not a telecrawl message ref", "Use a ref returned by telecrawl search --json, such as telecrawl:msg/<id>.")
		}
		return sourcePK, nil
	}
	fullRefs, err := st.ResolveShortRef(r.ctx, ref)
	if errors.Is(err, store.ErrUnknownShortRef) {
		return 0, r.contractError("unknown_short_ref", "short ref was not found in this archive", "Run telecrawl search and copy the displayed short ref, or use a full ref from telecrawl search --json.")
	}
	if errors.Is(err, store.ErrAmbiguousShortRef) {
		return 0, r.contractError("ambiguous_short_ref", "short ref matches more than one archived message", "Run telecrawl search again and use the longer displayed ref or the full ref from telecrawl search --json.")
	}
	if err != nil {
		return 0, err
	}
	if len(fullRefs) != 1 {
		return 0, r.contractError("unknown_short_ref", "short ref was not found in this archive", "Run telecrawl search and copy the displayed short ref, or use a full ref from telecrawl search --json.")
	}
	sourcePK, err := parseMessageRef(fullRefs[0])
	if err != nil {
		return 0, err
	}
	return sourcePK, nil
}

func parseMessageRef(ref string) (int64, error) {
	if !strings.HasPrefix(ref, store.MessageRefPrefix) {
		return 0, errors.New("invalid message ref")
	}
	rawID := strings.TrimPrefix(ref, store.MessageRefPrefix)
	if rawID == "" {
		return 0, errors.New("invalid message ref")
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != rawID {
		return 0, errors.New("invalid message ref")
	}
	return id, nil
}

func (r *runtime) printOpen(value openEnvelope) error {
	if _, err := fmt.Fprintf(r.stdout, "chat: %s (%s)\n", value.Chat.Name, value.Chat.Ref); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "ref: %s\n", value.Ref); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "target: %s %s\n", shortLocalTime(parseRenderTime(value.Message.Time)), value.Message.Sender.DisplayName); err != nil {
		return err
	}
	if strings.TrimSpace(value.Message.Text) != "" {
		if _, err := fmt.Fprintf(r.stdout, "text: %s\n", value.Message.Text); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(r.stdout, "Showing %s and %s.\n", contextCountPhrase(value.ContextWindow.Before, "earlier message", "earlier messages"), contextCountPhrase(value.ContextWindow.After, "message after", "messages after")); err != nil {
		return err
	}
	if value.ContextWindow.BeforeTruncated || value.ContextWindow.AfterTruncated {
		chatID := value.Chat.Ref[strings.LastIndex(value.Chat.Ref, "/")+1:]
		if _, err := fmt.Fprintf(r.stdout, "More: telecrawl messages --chat %s\n", chatID); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(r.stdout, "\n"); err != nil {
		return err
	}
	rows := make([]render.TranscriptRow, 0, len(value.Context))
	for _, message := range value.Context {
		text := strings.TrimSpace(message.Text)
		if text == "" {
			text = mediaSummary(message)
		}
		prefix := openTranscriptPrefix(render.OutputWidth(r.stdout), message)
		rows = append(rows, render.TranscriptRow{
			Time:   parseRenderTime(message.Time),
			Prefix: prefix,
			Text:   text,
		})
	}
	return render.WriteTranscript(r.stdout, rows)
}

func openTranscriptPrefix(width int, message openMessage) string {
	marker := " "
	if message.IsTarget {
		marker = ">"
	}
	when := "--:--"
	if parsed := parseRenderTime(message.Time); !parsed.IsZero() {
		when = parsed.Local().Format("15:04")
	}
	fixed := fmt.Sprintf("%s %s  ", marker, when)
	whoWidth := width - render.DisplayWidth(fixed) - render.DisplayWidth(": ") - 1
	if whoWidth < openTranscriptMinWhoWidth {
		whoWidth = openTranscriptMinWhoWidth
	}
	if whoWidth > openTranscriptMaxWhoWidth {
		whoWidth = openTranscriptMaxWhoWidth
	}
	return fixed + render.Truncate(message.Sender.DisplayName, whoWidth) + ": "
}

func contextCountPhrase(count int, singular, plural string) string {
	if count == 0 {
		switch singular {
		case "message after":
			return "none after"
		default:
			return "no " + plural
		}
	}
	if count == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(count) + " " + plural
}

func mediaSummary(message openMessage) string {
	switch {
	case message.MediaTitle != "":
		return "[" + message.MediaTitle + "]"
	case message.MediaType != "":
		return "[" + message.MediaType + "]"
	default:
		return "[empty message]"
	}
}
