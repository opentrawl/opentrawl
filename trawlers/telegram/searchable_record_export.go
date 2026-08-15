package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	searchablerecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/searchable_record"
)

func (c *Crawler) ExportSearchableRecordPage(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	exportRequest *searchablerecord.SearchableRecordExportRequest,
) (*searchablerecord.TrawlerSearchableRecordExportPage, error) {
	recordsAfterSourcePrimaryKey := int64(0)
	recordsAfterCanonicalReference := trawlkit.CanonicalArchiveRecordReferenceText(exportRequest.GetRecordsAfterCanonicalRecordReference())
	if recordsAfterCanonicalReference != "" {
		rawSourcePrimaryKey := strings.TrimPrefix(recordsAfterCanonicalReference, store.MessageRefPrefix)
		if rawSourcePrimaryKey == recordsAfterCanonicalReference {
			return nil, fmt.Errorf("telegram searchable record cursor is invalid")
		}
		var err error
		recordsAfterSourcePrimaryKey, err = strconv.ParseInt(rawSourcePrimaryKey, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("telegram searchable record cursor is invalid")
		}
	}
	archiveStore, err := store.UseExisting(
		ctx,
		req.OpenedTrawlerArchiveStore,
		req.TrawlerArchivePaths.TrawlerArchivePath,
	)
	if err != nil {
		return nil, err
	}
	messages, err := archiveStore.SearchableMessagesAfterSourcePrimaryKey(
		ctx,
		recordsAfterSourcePrimaryKey,
		int(exportRequest.GetMaximumRecordCount())+1,
	)
	if err != nil {
		return nil, err
	}
	searchableArchiveRecords := make([]*searchablerecord.SearchableArchiveRecord, 0, exportRequest.GetMaximumRecordCount()+1)
	for _, archiveMessage := range messages {
		typedRecord := telegramOpenedMessageRecord(archiveMessage)
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
