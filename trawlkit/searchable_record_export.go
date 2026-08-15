package trawlkit

import searchablerecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/searchable_record"

func CompleteSearchableRecordExportPage(
	searchableArchiveRecords []*searchablerecord.SearchableArchiveRecord,
	maximumRecordCount uint32,
) *searchablerecord.TrawlerSearchableRecordExportPage {
	allSearchableRecordsExported := len(searchableArchiveRecords) <= int(maximumRecordCount)
	if !allSearchableRecordsExported {
		searchableArchiveRecords = searchableArchiveRecords[:maximumRecordCount]
	}
	response := &searchablerecord.TrawlerSearchableRecordExportPage{
		SearchableArchiveRecordsInCanonicalReferenceOrder: searchableArchiveRecords,
		AllSearchableRecordsExported:                      allSearchableRecordsExported,
	}
	if len(searchableArchiveRecords) > 0 {
		response.NextPageStartsAfterCanonicalRecordReference = searchableArchiveRecords[len(searchableArchiveRecords)-1].GetCanonicalRecordReference()
	}
	return response
}
