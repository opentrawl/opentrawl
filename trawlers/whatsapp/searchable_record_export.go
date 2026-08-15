package whatsapp

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	searchablerecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/searchable_record"
)

func (c *Crawler) ExportSearchableRecordPage(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	exportRequest *searchablerecord.SearchableRecordExportRequest,
) (*searchablerecord.TrawlerSearchableRecordExportPage, error) {
	recordsAfterCanonicalReference := trawlkit.CanonicalArchiveRecordReferenceText(exportRequest.GetRecordsAfterCanonicalRecordReference())
	recordsAfterMessageIdentifier := strings.TrimPrefix(recordsAfterCanonicalReference, store.MessageRefPrefix)
	if recordsAfterCanonicalReference != "" && recordsAfterMessageIdentifier == recordsAfterCanonicalReference {
		return nil, fmt.Errorf("whatsapp searchable record cursor is invalid")
	}
	archiveStore, err := store.UseExisting(
		ctx,
		req.OpenedTrawlerArchiveStore,
		req.TrawlerArchivePaths.TrawlerArchivePath,
	)
	if err != nil {
		return nil, err
	}
	messages, err := archiveStore.SearchableMessagesAfterIdentifier(
		ctx,
		recordsAfterMessageIdentifier,
		int(exportRequest.GetMaximumRecordCount())+1,
	)
	if err != nil {
		return nil, err
	}
	searchableArchiveRecords := make([]*searchablerecord.SearchableArchiveRecord, 0, exportRequest.GetMaximumRecordCount()+1)
	for _, archiveMessage := range messages {
		typedRecord := projectMessageRecord(archiveMessage)
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
