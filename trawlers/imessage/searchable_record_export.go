package imessage

import (
	"context"
	"fmt"
	"strconv"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	searchablerecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/searchable_record"
)

func (c *Crawler) ExportSearchableRecordPage(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	exportRequest *searchablerecord.SearchableRecordExportRequest,
) (*searchablerecord.TrawlerSearchableRecordExportPage, error) {
	recordsAfterSourceRowIdentifier := int64(0)
	recordsAfterCanonicalReference := trawlkit.CanonicalArchiveRecordReferenceText(exportRequest.GetRecordsAfterCanonicalRecordReference())
	if recordsAfterCanonicalReference != "" {
		messageIdentifier, err := parseMessageRef(recordsAfterCanonicalReference)
		if err != nil {
			return nil, fmt.Errorf("imessage searchable record cursor is invalid")
		}
		recordsAfterSourceRowIdentifier, err = strconv.ParseInt(messageIdentifier, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("imessage searchable record cursor is invalid")
		}
	}
	store, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, err
	}
	messages, err := store.SearchableMessagesAfterSourceRowID(
		ctx,
		recordsAfterSourceRowIdentifier,
		int(exportRequest.GetMaximumRecordCount())+1,
	)
	if err != nil {
		return nil, err
	}
	searchableArchiveRecords := make([]*searchablerecord.SearchableArchiveRecord, 0, len(messages))
	chatSummariesByIdentifier := make(map[string]archive.ChatSummary)
	for _, archiveMessage := range messages {
		chatSummary, found := chatSummariesByIdentifier[archiveMessage.ChatID]
		if !found && archiveMessage.ChatID != "" {
			chatSummary, err = store.Chat(ctx, archiveMessage.ChatID)
			if err != nil {
				return nil, err
			}
			chatSummariesByIdentifier[archiveMessage.ChatID] = chatSummary
		}
		typedRecord := projectMessageRecord(archiveMessage, chatSummary)
		canonicalRecordReference := typedRecord.GetCanonicalRecordReference()
		searchableArchiveRecords = append(searchableArchiveRecords, &searchablerecord.SearchableArchiveRecord{
			CanonicalRecordReference:            canonicalRecordReference,
			CanonicalSearchResultGroupReference: canonicalRecordReference,
			TypedSourceRecord: &searchablerecord.SearchableArchiveRecord_MessageRecord{
				MessageRecord: typedRecord,
			},
		})
	}
	return trawlkit.CompleteSearchableRecordExportPage(searchableArchiveRecords, exportRequest.GetMaximumRecordCount()), nil
}
