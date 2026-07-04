package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/openclaw/telecrawl/internal/store"
)

func (r *runtime) runOpen(args []string) error {
	if len(args) != 1 {
		return usageErr(errors.New("open takes exactly one ref"))
	}
	return r.withStore(func(st *store.Store) error {
		sourcePK, err := r.resolveOpenMessageRef(st, args[0])
		if err != nil {
			return err
		}
		window, err := st.OpenMessageWindow(r.ctx, sourcePK, openContextRadius)
		if errors.Is(err, store.ErrMessageNotFound) {
			return r.contractError("not_found", "message was not found in this archive", "Run telecrawl search --json again and use one of the returned refs.")
		}
		if err != nil {
			return err
		}
		return r.print(newOpenEnvelope(window))
	})
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
	if _, err := fmt.Fprintf(r.stdout, "target: %s %s\n", value.Message.Time, value.Message.Sender.DisplayName); err != nil {
		return err
	}
	if strings.TrimSpace(value.Message.Text) != "" {
		if _, err := fmt.Fprintf(r.stdout, "text: %s\n", value.Message.Text); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(r.stdout, "context: %d before, %d after", value.ContextWindow.Before, value.ContextWindow.After); err != nil {
		return err
	}
	if value.ContextWindow.BeforeTruncated || value.ContextWindow.AfterTruncated {
		if _, err := io.WriteString(r.stdout, " (bounded; more messages omitted)"); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(r.stdout, "\n"); err != nil {
		return err
	}
	for _, message := range value.Context {
		marker := " "
		if message.IsTarget {
			marker = ">"
		}
		text := strings.TrimSpace(message.Text)
		if text == "" {
			text = mediaSummary(message)
		}
		if _, err := fmt.Fprintf(r.stdout, "%s %s  %s: %s\n", marker, message.Time, message.Sender.DisplayName, text); err != nil {
			return err
		}
	}
	return nil
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
