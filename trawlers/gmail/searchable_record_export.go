package gmail

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/gmail/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	searchablerecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/searchable_record"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) ExportSearchableRecordPage(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	exportRequest *searchablerecord.SearchableRecordExportRequest,
) (*searchablerecord.TrawlerSearchableRecordExportPage, error) {
	recordsAfterCanonicalReference := trawlkit.CanonicalArchiveRecordReferenceText(exportRequest.GetRecordsAfterCanonicalRecordReference())
	recordsAfterMessageIdentifier := strings.TrimPrefix(recordsAfterCanonicalReference, archive.RefPrefix)
	if recordsAfterCanonicalReference != "" && recordsAfterMessageIdentifier == recordsAfterCanonicalReference {
		return nil, fmt.Errorf("gmail searchable record cursor is invalid")
	}
	gmailArchiveStore, err := archive.UseExisting(
		ctx,
		req.OpenedTrawlerArchiveStore,
		req.TrawlerArchivePaths.TrawlerArchivePath,
	)
	if err != nil {
		return nil, err
	}
	openedMessages, err := gmailArchiveStore.OpenMessagesAfterIdentifier(
		ctx,
		recordsAfterMessageIdentifier,
		exportRequest.GetMaximumRecordCount()+1,
	)
	if err != nil {
		return nil, err
	}
	searchableArchiveRecords := make([]*searchablerecord.SearchableArchiveRecord, 0, exportRequest.GetMaximumRecordCount()+1)
	for _, openedMessage := range openedMessages {
		typedOpenedRecord, err := projectOpenRecord(openedMessage)
		if err != nil {
			return nil, err
		}
		associatedTime, err := parseContractTime(openedMessage.Time)
		if err != nil {
			return nil, err
		}
		trawlerSpecificRecord := &searchablerecord.TrawlerSpecificSearchableRecord{
			DetailPresentation: projectOpenDetailPresentation(typedOpenedRecord),
		}
		if !associatedTime.IsZero() {
			trawlerSpecificRecord.ArchiveRecordAssociatedTime = timestamppb.New(associatedTime)
		}
		canonicalRecordReference := typedOpenedRecord.GetCanonicalGmailMessageRecordReference()
		searchableArchiveRecords = append(searchableArchiveRecords, &searchablerecord.SearchableArchiveRecord{
			CanonicalRecordReference:            canonicalRecordReference,
			CanonicalSearchResultGroupReference: canonicalRecordReference,
			TypedSourceRecord: &searchablerecord.SearchableArchiveRecord_TrawlerSpecificRecord{
				TrawlerSpecificRecord: trawlerSpecificRecord,
			},
		})
	}
	return trawlkit.CompleteSearchableRecordExportPage(searchableArchiveRecords, exportRequest.GetMaximumRecordCount()), nil
}
