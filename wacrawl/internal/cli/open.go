package cli

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

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

func (a *app) runOpen(ctx context.Context, args []string) error {
	if commandWantsHelp(args) {
		printCommandUsage(a.stdout, "open")
		return nil
	}
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "open")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 1 {
		return usageErr(errors.New("open requires exactly one ref"))
	}
	ref := strings.TrimSpace(fs.Arg(0))
	if strings.Contains(ref, ":") {
		messageID, contractErr := parseMessageRef(ref)
		if contractErr != nil {
			return a.failContract(*contractErr)
		}
		return a.withReadStore(ctx, func(st *store.Store) error {
			return a.openMessageID(ctx, st, messageID)
		})
	}
	if !shortref.ValidAlias(ref) {
		return a.failContract(unknownShortRefError())
	}
	return a.withExistingStore(ctx, func(st *store.Store) error {
		if err := st.EnsureShortRefs(ctx); err != nil {
			return err
		}
		fullRefs, err := st.ResolveShortRef(ctx, ref)
		if err != nil {
			return err
		}
		switch len(fullRefs) {
		case 0:
			return a.failContract(unknownShortRefError())
		case 1:
			messageID, contractErr := parseMessageRef(fullRefs[0])
			if contractErr != nil {
				return a.failContract(*contractErr)
			}
			return a.openMessageID(ctx, st, messageID)
		default:
			return a.failContract(contractError{
				Code:    "ambiguous_short_ref",
				Message: "short ref matches more than one message",
				Remedy:  "rerun wacrawl search or use the full ref",
			})
		}
	})
}

func (a *app) openMessageID(ctx context.Context, st *store.Store, messageID string) error {
	target, err := st.MessageByID(ctx, messageID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return a.failContract(contractError{
				Code:    "not_found",
				Message: "message was not found",
				Remedy:  "run wacrawl search again and pass one of its refs",
			})
		}
		return err
	}
	window, err := st.MessageWindow(ctx, target, openWindowEachSide)
	if err != nil {
		return err
	}
	return a.print(newOpenEnvelope(target, window))
}

func unknownShortRefError() contractError {
	return contractError{
		Code:    "unknown_short_ref",
		Message: "short ref was not found",
		Remedy:  "use a full ref from wacrawl search",
	}
}

func parseMessageRef(ref string) (string, *contractError) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, messageRefPrefix) {
		return "", &contractError{
			Code:    "foreign_ref",
			Message: "ref does not belong to wacrawl",
			Remedy:  "pass a ref returned by wacrawl search",
		}
	}
	messageID := strings.TrimSpace(strings.TrimPrefix(ref, messageRefPrefix))
	if messageID == "" {
		return "", &contractError{
			Code:    "invalid_ref",
			Message: "wacrawl message ref is missing its message id",
			Remedy:  "pass a complete ref returned by wacrawl search",
		}
	}
	return messageID, nil
}

func (a *app) printOpen(result openEnvelope) error {
	if _, err := fmt.Fprintf(a.stdout, "chat: %s\nref: %s\n\n", result.Chat, result.Ref); err != nil {
		return err
	}
	width := render.OutputWidth(a.stdout)
	rows := make([]render.TranscriptRow, 0, len(result.Context))
	for _, item := range result.Context {
		prefix := openTranscriptPrefix(width, item)
		rows = append(rows, render.TranscriptRow{
			Time:   parseFormattedTime(item.Time),
			Prefix: prefix,
			Text:   item.Text,
		})
	}
	return render.WriteTranscript(a.stdout, rows)
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
	media := messageMedia(message)
	return openMessage{
		Ref:     messageRef(message),
		Time:    formatTime(message.Timestamp),
		Who:     outputField(messageWho(message)),
		Where:   outputField(messageWhere(message)),
		Text:    messageText(message),
		Type:    messageKind(message),
		Media:   media,
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
