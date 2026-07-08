package telecrawl

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

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
	sourcePK, err := r.resolveOpenMessageRef(ref)
	if err != nil {
		return err
	}
	window, err := st.OpenMessageWindow(ctx, sourcePK, openContextRadius)
	if errors.Is(err, store.ErrMessageNotFound) {
		return r.contractError("not_found", "message was not found in this archive", "run trawl telegram search --json again and use one of the returned refs.")
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

func (r *runtime) resolveOpenMessageRef(ref string) (int64, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		sourcePK, err := parseMessageRef(ref)
		if err != nil {
			return 0, r.contractError("invalid_ref", "ref is not a telegram message ref", "use a ref returned by trawl telegram search --json, such as telegram:msg/<id>.")
		}
		return sourcePK, nil
	}
	fullRefs, err := r.req.ResolveShortRef(r.ctx, ref)
	if errors.Is(err, crawlkit.ErrUnknownShortRef) {
		return 0, r.contractError("unknown_short_ref", "short ref was not found in this archive", "run trawl telegram search and copy the displayed short ref, or use a full ref from trawl telegram search --json.")
	}
	if errors.Is(err, crawlkit.ErrAmbiguousShortRef) {
		return 0, r.contractError("ambiguous_short_ref", "short ref matches more than one archived message", "run trawl telegram search again and use the longer displayed ref or the full ref from trawl telegram search --json.")
	}
	if err != nil {
		return 0, err
	}
	if len(fullRefs) != 1 {
		return 0, r.contractError("unknown_short_ref", "short ref was not found in this archive", "run trawl telegram search and copy the displayed short ref, or use a full ref from trawl telegram search --json.")
	}
	sourcePK, err := parseMessageRef(fullRefs[0])
	if err != nil {
		return 0, err
	}
	return sourcePK, nil
}

func parseMessageRef(ref string) (int64, error) {
	prefix := store.MessageRefPrefix
	if !strings.HasPrefix(ref, prefix) {
		if !strings.HasPrefix(ref, store.LegacyMessageRefPrefix) {
			return 0, errors.New("invalid message ref")
		}
		prefix = store.LegacyMessageRefPrefix
	}
	rawID := strings.TrimPrefix(ref, prefix)
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
	title := value.Chat.Name
	if span := openDateSpan(value.Context); span != "" {
		title += ", " + span
	}
	if err := render.WriteTranscriptHeader(r.stdout, render.TranscriptHeader{
		Title:        title,
		Ref:          value.Ref,
		Participants: value.Participants,
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "\nTime: %s\nFrom: %s\n", shortLocalTime(parseRenderTime(value.Message.Time)), render.HumanIdentity(value.Message.Sender.DisplayName)); err != nil {
		return err
	}
	if strings.TrimSpace(value.Message.Text) != "" {
		if err := render.WriteWrappedField(r.stdout, "Text", value.Message.Text); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(r.stdout); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "Context: %s messages around this one.\n", render.FormatInteger(int64(len(value.Context)))); err != nil {
		return err
	}
	if value.ContextWindow.BeforeTruncated || value.ContextWindow.AfterTruncated {
		chatID := value.Chat.Ref[strings.LastIndex(value.Chat.Ref, "/")+1:]
		if _, err := fmt.Fprintf(r.stdout, "More: trawl telegram messages --chat %s\n", chatID); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(r.stdout); err != nil {
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
	return fixed + render.Truncate(render.HumanIdentity(message.Sender.DisplayName), whoWidth) + ": "
}

func openDateSpan(context []openMessage) string {
	var first time.Time
	var last time.Time
	for _, item := range context {
		t := parseRenderTime(item.Time)
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
