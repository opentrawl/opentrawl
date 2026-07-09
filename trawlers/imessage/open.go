package imsgcrawl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

const (
	defaultOpenWindow = 10
	messageRefPrefix  = archive.MessageRefPrefix
)

var (
	errForeignRef = errors.New("ref is not from imessage")
	errInvalidRef = errors.New("ref is not an imessage message ref")
)

func (c *Crawler) Open(ctx context.Context, req *trawlkit.Request, ref string) error {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	messageID, err := c.resolveOpenRef(ctx, req, ref)
	if err != nil {
		return err
	}
	result, err := st.OpenMessage(ctx, messageID, defaultOpenWindow)
	if errors.Is(err, archive.ErrMessageNotFound) {
		return commandErr(1, "not_found", errors.New("message ref was not found"), "run trawl imessage search --json again and use a current ref")
	}
	if err != nil {
		return err
	}
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "open", newOpenOutput(result))
	}
	return printOpenText(req.Out, newOpenOutput(result))
}

func (c *Crawler) resolveOpenRef(ctx context.Context, req *trawlkit.Request, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.Contains(ref, ":") {
		return c.resolveShortRef(ctx, req, ref)
	}
	messageID, err := parseMessageRef(ref)
	if err != nil {
		if errors.Is(err, errForeignRef) {
			return "", commandErr(1, "foreign_ref", err, "use a ref returned by trawl imessage search --json")
		}
		return "", commandErr(1, "invalid_ref", err, "use a ref in the form imessage:msg/ID")
	}
	return messageID, nil
}

func (c *Crawler) resolveShortRef(ctx context.Context, req *trawlkit.Request, alias string) (string, error) {
	if !trawlkit.ValidShortRef(alias) {
		return "", commandErr(1, "invalid_ref", errInvalidRef, "use a ref in the form imessage:msg/ID or a short ref from search")
	}
	resolved, err := req.ResolveShortRef(ctx, alias)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", commandErr(1, "unknown_short_ref", errors.New("short ref was not found"), "rerun search or use the full ref")
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandErr(1, "ambiguous_short_ref", errors.New("short ref matches more than one message"), "rerun search or use the full ref")
	}
	if err != nil {
		return "", err
	}
	messageID, err := parseMessageRef(resolved[0])
	if err != nil {
		return "", err
	}
	return messageID, nil
}

func parseMessageRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	prefix := messageRefPrefix
	if !strings.HasPrefix(ref, prefix) {
		if !strings.HasPrefix(ref, archive.LegacyMessageRefPrefix) {
			return "", errForeignRef
		}
		prefix = archive.LegacyMessageRefPrefix
	}
	messageID := strings.TrimPrefix(ref, prefix)
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
	if err := render.WriteTranscriptHeader(w, render.TranscriptHeader{
		Title:        title,
		Ref:          value.Ref,
		Participants: value.Chat.Participants,
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "\nTime: %s\nFrom: %s\n", formatArchiveTime(value.Message.Time), render.HumanIdentity(value.Message.Who)); err != nil {
		return err
	}
	if err := render.WriteWrappedField(w, "Text", displayMessageText(value.Message.Text, value.Message.HasAttachments)); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Context: %s messages around this one.\n\n", render.FormatInteger(int64(len(value.Context)))); err != nil {
		return err
	}
	return printOpenTranscript(w, value.Context)
}
