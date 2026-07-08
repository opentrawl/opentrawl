package imsgcrawl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/imsgcrawl/internal/archive"
)

const (
	defaultOpenWindow = 10
	messageRefPrefix  = archive.MessageRefPrefix
)

var (
	errForeignRef = errors.New("ref is not from imsgcrawl")
	errInvalidRef = errors.New("ref is not an imsgcrawl message ref")
)

func (c *Crawler) Open(ctx context.Context, req *crawlkit.Request, ref string) error {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	messageID, err := resolveOpenRef(ctx, st, ref)
	if err != nil {
		return err
	}
	result, err := st.OpenMessage(ctx, messageID, defaultOpenWindow)
	if errors.Is(err, archive.ErrMessageNotFound) {
		return commandErr(1, "not_found", errors.New("message ref was not found"), "run trawl imsgcrawl search --json again and use a current ref")
	}
	if err != nil {
		return err
	}
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "open", newOpenOutput(result))
	}
	return printOpenText(req.Out, newOpenOutput(result))
}

func resolveOpenRef(ctx context.Context, st *archive.Store, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.Contains(ref, ":") {
		return resolveShortRef(ctx, st, ref)
	}
	messageID, err := parseMessageRef(ref)
	if err != nil {
		if errors.Is(err, errForeignRef) {
			return "", commandErr(1, "foreign_ref", err, "use a ref returned by trawl imsgcrawl search --json")
		}
		return "", commandErr(1, "invalid_ref", err, "use a ref in the form imsgcrawl:msg/ID")
	}
	return messageID, nil
}

func resolveShortRef(ctx context.Context, st *archive.Store, alias string) (string, error) {
	if !archive.ValidShortRef(alias) {
		return "", commandErr(1, "invalid_ref", errInvalidRef, "use a ref in the form imsgcrawl:msg/ID or a short ref from search")
	}
	resolved, err := st.ResolveShortRef(ctx, alias)
	if err != nil {
		return "", err
	}
	switch len(resolved.FullRefs) {
	case 0:
		return "", commandErr(1, "unknown_short_ref", errors.New("short ref was not found"), "rerun search or use the full ref")
	case 1:
		messageID, err := parseMessageRef(resolved.FullRefs[0])
		if err != nil {
			return "", err
		}
		return messageID, nil
	default:
		return "", commandErr(1, "ambiguous_short_ref", errors.New("short ref matches more than one message"), "rerun search or use the full ref")
	}
}

func parseMessageRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, messageRefPrefix) {
		return "", errForeignRef
	}
	messageID := strings.TrimPrefix(ref, messageRefPrefix)
	if messageID == "" || strings.TrimSpace(messageID) != messageID {
		return "", errInvalidRef
	}
	id, err := strconv.ParseInt(messageID, 10, 64)
	if err != nil || id <= 0 {
		return "", errInvalidRef
	}
	return messageID, nil
}

func newOpenOutput(value archive.MessageContext) openOutput {
	where := chatDisplayName(value.Chat)
	if where == "" {
		where = "unknown chat"
	}
	where = outputField(where)
	out := openOutput{
		Ref: archive.MessageRef(value.Message.MessageID),
		Chat: openChatOutput{
			Name:         where,
			Participants: value.Chat.ParticipantHandles,
		},
		Message: openMessageItem(value.Message, where, false),
	}
	out.Context = make([]openMessageOutput, 0, len(value.Before)+1+len(value.After))
	for _, item := range value.Before {
		out.Context = append(out.Context, openMessageItem(item, where, false))
	}
	out.Context = append(out.Context, openMessageItem(value.Message, where, true))
	for _, item := range value.After {
		out.Context = append(out.Context, openMessageItem(item, where, false))
	}
	return out
}

func openMessageItem(item archive.MessageRow, where string, target bool) openMessageOutput {
	return openMessageOutput{
		Ref:            archive.MessageRef(item.MessageID),
		Time:           item.Time,
		Who:            outputField(senderName(item.FromMe, item.SenderLabel)),
		Where:          outputField(where),
		Text:           item.Text,
		FromMe:         item.FromMe,
		HasAttachments: item.HasAttachments,
		Target:         target,
	}
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
