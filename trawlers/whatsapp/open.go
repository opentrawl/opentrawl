package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
)

type mediaDetails struct {
	Type      string
	Title     string
	SizeBytes int64
}

func (c *Crawler) loadOpenMessage(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (openValue, error) {
	st, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return openValue{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	messageID, err := c.resolveOpenMessageID(ctx, req, localShortReference)
	if err != nil {
		return openValue{}, err
	}
	target, err := st.MessageByID(ctx, messageID)
	if err != nil {
		if errorsIsNoRows(err) {
			return openValue{}, commandErr(1, "not_found", "No message has that link.")
		}
		return openValue{}, err
	}
	window, err := st.MessageWindow(ctx, target, openWindowEachSide)
	if err != nil {
		return openValue{}, err
	}
	participantIdentities, err := st.ConversationParticipantIdentitiesObservedByTrawlerArchive(
		ctx,
		target.ChatJID,
	)
	if err != nil {
		return openValue{}, err
	}
	return openValue{
		target:  target,
		context: window.Messages,
		participants: conversationParticipantDisplayNamesFromIdentitiesObservedByTrawlerArchive(
			participantIdentities,
		),
		beforeTruncated: window.BeforeTruncated,
		afterTruncated:  window.AfterTruncated,
	}, nil
}

func (c *Crawler) resolveOpenMessageID(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (string, error) {
	fullRefs, err := req.ResolveShortReference(ctx, localShortReference)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", unknownShortRefError()
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandErr(1, "ambiguous_short_ref", "More than one message has that link.")
	}
	if err != nil {
		return "", err
	}
	if len(fullRefs) != 1 {
		return "", unknownShortRefError()
	}
	return parseMessageRef(trawlkit.CanonicalArchiveRecordReferenceText(fullRefs[0]))
}

func unknownShortRefError() error {
	return commandErr(1, "unknown_short_ref", "No message has that link.")
}

func parseMessageRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, messageRefPrefix) {
		return "", commandErr(1, "foreign_ref", "The link is not for WhatsApp.")
	}
	messageID := strings.TrimSpace(strings.TrimPrefix(ref, messageRefPrefix))
	if messageID == "" {
		return "", commandErr(1, "invalid_ref", "The WhatsApp message link is not valid.")
	}
	return messageID, nil
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
