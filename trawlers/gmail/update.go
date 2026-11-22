package gmail

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/gmail/internal/archive"
	"github.com/opentrawl/opentrawl/gmail/internal/auth"
	"github.com/opentrawl/opentrawl/gmail/internal/gmailapi"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/mailparse"
	updatecontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"google.golang.org/protobuf/proto"
)

const (
	gmailMessagesPerFetchBatch     = 100
	reparsedMessagesPerTransaction = 500
)

type gmailUpdate struct {
	req                   *trawlkit.TrawlerCommandExecutionRequest
	archiveStore          *archive.Store
	gmailAPI              *gmailapi.GmailAPIClient
	rawEmailOriginals     mailparse.RawEmailOriginalStore
	gmailLabels           []gmailapi.GmailLabel
	deletedFromSourceTime time.Time

	messagesFetched          int
	messagesAdded            int
	messagesLabelsRefreshed  int
	messagesDeletedFromGmail int
}

// Update lists the whole mailbox the first time, and afterwards reads only
// Gmail's history since the stored cursor. Raw messages are fetched once
// and kept as write-once originals; a message that leaves Gmail stays in the
// archive, marked deleted from Gmail.
func (c *Crawler) Update(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*updatecontract.TrawlerArchiveUpdateReport, error) {
	updateStartedTime := time.Now()
	accessTokens, err := auth.NewGmailAccessTokenSource(googleOAuthClient())
	if err != nil {
		return nil, gmailConnectionCommandFailure(req, err)
	}
	archiveStore, err := archive.Use(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, commandErr("archive_open_failed", "cannot open the archive database", err)
	}
	if err := archiveStore.MarkUpdateStarted(ctx, updateStartedTime); err != nil {
		return nil, err
	}
	rawEmailOriginals := mailparse.NewRawEmailOriginalStore(filepath.Dir(req.TrawlerArchivePaths.TrawlerArchivePath))
	reparsed, err := reparseStoredOriginalsWithOlderParse(ctx, archiveStore, rawEmailOriginals)
	if err != nil {
		return nil, err
	}
	update := &gmailUpdate{
		req:                   req,
		archiveStore:          archiveStore,
		gmailAPI:              gmailapi.NewGmailAPIClient(accessTokens),
		rawEmailOriginals:     rawEmailOriginals,
		deletedFromSourceTime: updateStartedTime,
	}
	if err := update.run(ctx); err != nil {
		return nil, gmailConnectionCommandFailure(req, err)
	}
	if err := archiveStore.MarkUpdateCompleted(ctx, time.Now()); err != nil {
		return nil, err
	}
	logGmailEvent(req, "update_complete", strings.Join([]string{
		"fetched=" + strconv.Itoa(update.messagesFetched),
		"added=" + strconv.Itoa(update.messagesAdded),
		"labels_refreshed=" + strconv.Itoa(update.messagesLabelsRefreshed),
		"deleted_from_gmail=" + strconv.Itoa(update.messagesDeletedFromGmail),
		"reparsed_from_originals=" + strconv.Itoa(reparsed),
		"elapsed_ms=" + strconv.FormatInt(time.Since(updateStartedTime).Milliseconds(), 10),
	}, " "))
	return &updatecontract.TrawlerArchiveUpdateReport{ArchiveRecordCountAddedByThisUpdate: proto.Uint64(uint64(update.messagesAdded))}, nil
}

func (u *gmailUpdate) run(ctx context.Context) error {
	profile, err := u.gmailAPI.Profile(ctx)
	if err != nil {
		return err
	}
	if err := u.archiveStore.BindGoogleAccountEmailAddress(ctx, profile.GoogleAccountEmailAddress); err != nil {
		return err
	}
	labels, err := u.gmailAPI.ListGmailLabels(ctx)
	if err != nil {
		return err
	}
	if err := u.archiveStore.WriteGmailLabels(ctx, labels); err != nil {
		return err
	}
	u.gmailLabels = labels
	syncState, err := u.archiveStore.GmailMailboxSyncState(ctx)
	if err != nil {
		return err
	}
	switch syncState.Phase {
	case archive.GmailMailboxSyncPhaseFullMailboxListingInProgress:
		return u.completeFullMailboxListing(ctx, syncState.HistoryCursor)
	case archive.GmailMailboxSyncPhaseNeverSynced:
		return u.startAndCompleteFullMailboxListing(ctx, profile.CurrentGmailHistoryIdentifier)
	case archive.GmailMailboxSyncPhaseIncrementalHistorySynced:
		err = u.applyMailboxChangesSince(ctx, syncState.HistoryCursor, false)
		if errors.Is(err, gmailapi.ErrGmailHistoryCursorExpired) {
			logGmailEvent(u.req, "history_expired", "listing the whole mailbox again")
			return u.startAndCompleteFullMailboxListing(ctx, profile.CurrentGmailHistoryIdentifier)
		}
		return err
	default:
		return errors.New("unknown Gmail mailbox sync phase")
	}
}

// startAndCompleteFullMailboxListing records the mailbox's history cursor
// before listing every message, then finishes fetching and reconciling.
func (u *gmailUpdate) startAndCompleteFullMailboxListing(ctx context.Context, cursor gmailapi.GmailHistoryIdentifier) error {
	listedIdentifiers, err := u.gmailAPI.ListGmailMessageIdentifiers(ctx, "")
	if err != nil {
		return err
	}
	if err := u.archiveStore.StartFullMailboxListing(ctx, cursor, listedIdentifiers); err != nil {
		return err
	}
	logGmailEvent(u.req, "full_listing_started", "listed="+strconv.Itoa(len(listedIdentifiers)))
	return u.completeFullMailboxListing(ctx, cursor)
}

// completeFullMailboxListing fetches only messages the archive lacks,
// reconciles labels and deletions, then replays history since cursor.
func (u *gmailUpdate) completeFullMailboxListing(ctx context.Context, cursor gmailapi.GmailHistoryIdentifier) error {
	for {
		pending, err := u.archiveStore.GmailMessagesNotYetFetchedInFullMailboxListing(ctx)
		if err != nil {
			return err
		}
		for start := 0; start < len(pending); start += gmailMessagesPerFetchBatch {
			batch := pending[start:min(start+gmailMessagesPerFetchBatch, len(pending))]
			fetched, excluded, err := u.gmailAPI.FetchRawGmailMessages(ctx, batch)
			if err != nil {
				return err
			}
			messages, err := u.archivedGmailMessages(fetched)
			if err != nil {
				return err
			}
			added, err := u.archiveStore.WriteFullMailboxListingBatch(ctx, messages, excluded)
			if err != nil {
				return err
			}
			u.messagesFetched += len(fetched)
			u.messagesAdded += added
			u.reportProgress(start+len(batch), len(pending), "downloading Gmail messages")
		}
		labelsByMessage, err := u.gmailLabelMembership(ctx)
		if err != nil {
			return err
		}
		deleted, err := u.archiveStore.ReconcileFullMailboxListing(ctx, labelsByMessage, u.deletedFromSourceTime)
		if err != nil {
			return err
		}
		u.messagesDeletedFromGmail += deleted
		err = u.applyMailboxChangesSince(ctx, cursor, true)
		if errors.Is(err, gmailapi.ErrGmailHistoryCursorExpired) {
			logGmailEvent(u.req, "history_expired", "listing the whole mailbox again")
			profile, err := u.gmailAPI.Profile(ctx)
			if err != nil {
				return err
			}
			if err := u.archiveStore.BindGoogleAccountEmailAddress(ctx, profile.GoogleAccountEmailAddress); err != nil {
				return err
			}
			if err := u.archiveStore.ClearFullMailboxListing(ctx); err != nil {
				return err
			}
			listedIdentifiers, err := u.gmailAPI.ListGmailMessageIdentifiers(ctx, "")
			if err != nil {
				return err
			}
			if err := u.archiveStore.StartFullMailboxListing(ctx, profile.CurrentGmailHistoryIdentifier, listedIdentifiers); err != nil {
				return err
			}
			cursor = profile.CurrentGmailHistoryIdentifier
			logGmailEvent(u.req, "full_listing_started", "listed="+strconv.Itoa(len(listedIdentifiers)))
			continue
		}
		return err
	}
}

// gmailLabelMembership reads which messages carry each label from Gmail's
// per-label listings, so labels of archived messages are refreshed without
// downloading the messages again.
func (u *gmailUpdate) gmailLabelMembership(ctx context.Context) (map[gmailapi.GmailMessageIdentifier][]gmailapi.GmailLabelIdentifier, error) {
	labelsByMessage := map[gmailapi.GmailMessageIdentifier][]gmailapi.GmailLabelIdentifier{}
	for labelIndex, label := range u.gmailLabels {
		if label.GmailLabelIdentifier == gmailapi.GmailSpamLabelIdentifier || label.GmailLabelIdentifier == gmailapi.GmailTrashLabelIdentifier {
			continue
		}
		identifiers, err := u.gmailAPI.ListGmailMessageIdentifiers(ctx, label.GmailLabelIdentifier)
		if err != nil {
			return nil, err
		}
		for _, identifier := range identifiers {
			labelsByMessage[identifier] = append(labelsByMessage[identifier], label.GmailLabelIdentifier)
		}
		u.reportProgress(labelIndex+1, len(u.gmailLabels), "reconciling Gmail labels")
	}
	return labelsByMessage, nil
}

// applyMailboxChangesSince downloads only messages the archive lacks, asks
// Gmail only for the labels of messages it holds, and writes label changes,
// deletions and the next cursor together.
func (u *gmailUpdate) applyMailboxChangesSince(ctx context.Context, cursor gmailapi.GmailHistoryIdentifier, finishesFullMailboxListing bool) error {
	changes, err := u.gmailAPI.ListGmailMailboxChanges(ctx, cursor)
	if err != nil {
		return err
	}
	archived, err := u.archiveStore.ArchivedGmailMessageIdentifiers(ctx, changes.ChangedGmailMessageIdentifiers)
	if err != nil {
		return err
	}
	var messagesToDownload, messagesToRefreshLabels []gmailapi.GmailMessageIdentifier
	for _, identifier := range changes.ChangedGmailMessageIdentifiers {
		if _, isArchived := archived[identifier]; isArchived {
			messagesToRefreshLabels = append(messagesToRefreshLabels, identifier)
		} else {
			messagesToDownload = append(messagesToDownload, identifier)
		}
	}
	toApply := archive.GmailMailboxChangesToApply{
		GmailMessagesDeletedFromSource: append([]gmailapi.GmailMessageIdentifier(nil), changes.DeletedGmailMessageIdentifiers...),
		NextGmailHistoryCursor:         changes.NextGmailHistoryCursor,
		FinishesFullMailboxListing:     finishesFullMailboxListing,
		DeletedFromSourceTime:          u.deletedFromSourceTime,
	}
	for start := 0; start < len(messagesToDownload); start += gmailMessagesPerFetchBatch {
		batch := messagesToDownload[start:min(start+gmailMessagesPerFetchBatch, len(messagesToDownload))]
		fetched, excluded, err := u.gmailAPI.FetchRawGmailMessages(ctx, batch)
		if err != nil {
			return err
		}
		messages, err := u.archivedGmailMessages(fetched)
		if err != nil {
			return err
		}
		added, err := u.archiveStore.WriteArchivedGmailMessages(ctx, messages)
		if err != nil {
			return err
		}
		u.messagesFetched += len(fetched)
		u.messagesAdded += added
		toApply.GmailMessagesDeletedFromSource = append(toApply.GmailMessagesDeletedFromSource, excluded...)
		u.reportProgress(start+len(batch), len(messagesToDownload), "downloading new Gmail messages")
	}
	for start := 0; start < len(messagesToRefreshLabels); start += gmailMessagesPerFetchBatch {
		batch := messagesToRefreshLabels[start:min(start+gmailMessagesPerFetchBatch, len(messagesToRefreshLabels))]
		labelStates, excluded, err := u.gmailAPI.FetchGmailMessageLabelStates(ctx, batch)
		if err != nil {
			return err
		}
		toApply.GmailMessageLabelStates = append(toApply.GmailMessageLabelStates, labelStates...)
		toApply.GmailMessagesDeletedFromSource = append(toApply.GmailMessagesDeletedFromSource, excluded...)
		u.reportProgress(start+len(batch), len(messagesToRefreshLabels), "refreshing Gmail labels")
	}
	deleted, err := u.archiveStore.ApplyGmailMailboxChanges(ctx, toApply)
	if err != nil {
		return err
	}
	u.messagesLabelsRefreshed += len(toApply.GmailMessageLabelStates)
	u.messagesDeletedFromGmail += deleted
	return nil
}

// archivedGmailMessages stores each raw message once and parses it. The
// message time is Gmail's internalDate, else the Date header.
func (u *gmailUpdate) archivedGmailMessages(fetched []gmailapi.FetchedRawGmailMessage) ([]archive.ArchivedGmailMessage, error) {
	messages := make([]archive.ArchivedGmailMessage, 0, len(fetched))
	for _, message := range fetched {
		originalSHA256, err := u.rawEmailOriginals.StoreRawEmailOriginalOnce(message.RawRFC5322EmailMessage)
		if err != nil {
			return nil, err
		}
		parsed := mailparse.ParseRawEmailMessage(message.RawRFC5322EmailMessage)
		internalDateMilliseconds := message.GmailInternalDateMilliseconds
		if (!message.GmailInternalDateWasReported || internalDateMilliseconds <= 0) && !parsed.DateHeaderTime.IsZero() {
			internalDateMilliseconds = parsed.DateHeaderTime.UnixMilli()
		}
		messages = append(messages, archive.ArchivedGmailMessage{
			GmailMessageIdentifier:        message.GmailMessageIdentifier,
			GmailThreadIdentifier:         message.GmailThreadIdentifier,
			GmailHistoryIdentifier:        message.GmailHistoryIdentifier,
			GmailInternalDateMilliseconds: internalDateMilliseconds,
			GmailLabelIdentifiers:         message.GmailLabelIdentifiers,
			RawEmailOriginalSHA256:        originalSHA256,
			EmailParserRevision:           mailparse.EmailParserRevision,
			ParsedEmailMessage:            parsed,
		})
	}
	return messages, nil
}

// reparseStoredOriginalsWithOlderParse parses stored originals again when
// the parser changes. Gmail is never asked for them again.
func reparseStoredOriginalsWithOlderParse(ctx context.Context, archiveStore *archive.Store, rawEmailOriginals mailparse.RawEmailOriginalStore) (int, error) {
	olderParses, err := archiveStore.ArchivedGmailMessagesWithOlderParse(ctx, mailparse.EmailParserRevision)
	if err != nil {
		return 0, err
	}
	for start := 0; start < len(olderParses); start += reparsedMessagesPerTransaction {
		batch := olderParses[start:min(start+reparsedMessagesPerTransaction, len(olderParses))]
		reparsed := make([]archive.ReparsedGmailMessage, 0, len(batch))
		for _, message := range batch {
			rawEmailMessage, err := rawEmailOriginals.ReadRawEmailOriginal(message.RawEmailOriginalSHA256)
			if err != nil {
				return 0, err
			}
			reparsed = append(reparsed, archive.ReparsedGmailMessage{
				GmailMessageIdentifier: message.GmailMessageIdentifier,
				EmailParserRevision:    mailparse.EmailParserRevision,
				ParsedEmailMessage:     mailparse.ParseRawEmailMessage(rawEmailMessage),
			})
		}
		if err := archiveStore.RewriteReparsedGmailMessages(ctx, reparsed); err != nil {
			return 0, err
		}
	}
	return len(olderParses), nil
}

func (u *gmailUpdate) reportProgress(messagesDone, messagesTotal int, message string) {
	if u.req.ReportTrawlerCommandProgress != nil {
		u.req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: "update", Done: int64(messagesDone), Total: int64(messagesTotal), Message: message})
	}
}
