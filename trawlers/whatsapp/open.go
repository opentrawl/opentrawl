package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func (c *Crawler) loadOpenMessage(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	canonicalRecordReference *trawlkit.CanonicalArchiveRecordReference,
) (openValue, error) {
	st, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return openValue{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	messageID, err := parseMessageRef(trawlkit.CanonicalArchiveRecordReferenceText(canonicalRecordReference))
	if err != nil {
		return openValue{}, err
	}
	target, err := st.MessageByID(ctx, messageID)
	if err != nil {
		if errorsIsNoRows(err) {
			return openValue{}, commandErr(1, "not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounMessage))
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

func parseMessageRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, messageRefPrefix) {
		return "", commandErr(1, "foreign_ref", render.LinkIsForAnotherTrawler())
	}
	messageID := strings.TrimSpace(strings.TrimPrefix(ref, messageRefPrefix))
	if messageID == "" {
		return "", commandErr(1, "invalid_ref", render.LinkIsNotValid())
	}
	return messageID, nil
}

func errorsIsNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
