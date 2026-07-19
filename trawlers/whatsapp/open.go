package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

type mediaDetails struct {
	Type      string
	Title     string
	SizeBytes int64
}

func (c *Crawler) loadOpenMessage(ctx context.Context, req *trawlkit.Request, ref string) (openValue, error) {
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return openValue{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	messageID, err := c.resolveOpenMessageID(ctx, req, ref)
	if err != nil {
		return openValue{}, err
	}
	target, err := st.MessageByID(ctx, messageID)
	if err != nil {
		if errorsIsNoRows(err) {
			return openValue{}, commandErr(1, "not_found", "message was not found", "run trawl whatsapp search again and pass one of its refs")
		}
		return openValue{}, err
	}
	window, err := st.MessageWindow(ctx, target, openWindowEachSide)
	if err != nil {
		return openValue{}, err
	}
	participants, err := st.GroupParticipants(ctx, target.ChatJID)
	if err != nil {
		return openValue{}, err
	}
	return openValue{target: target, context: window, participants: participants}, nil
}

func (c *Crawler) resolveOpenMessageID(ctx context.Context, req *trawlkit.Request, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		return parseMessageRef(ref)
	}
	fullRefs, err := req.ResolveShortRef(ctx, ref)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", unknownShortRefError()
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
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
		return "", commandErr(1, "foreign_ref", "ref does not belong to whatsapp", "pass a ref returned by trawl whatsapp search")
	}
	messageID := strings.TrimSpace(strings.TrimPrefix(ref, messageRefPrefix))
	if messageID == "" {
		return "", commandErr(1, "invalid_ref", "whatsapp message ref is missing its message id", "pass a complete ref returned by trawl whatsapp search")
	}
	return messageID, nil
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

func messageMedia(message store.Message) *mediaDetails {
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
	return &mediaDetails{Type: kind, Title: title, SizeBytes: message.MediaSize}
}

func errorsIsNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
