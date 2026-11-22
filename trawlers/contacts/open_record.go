package contacts

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
)

type openedPersonValuesLoadedFromContactsArchive struct {
	canonicalPersonRecordReference string
	archivedPerson                 model.Person
}

func (a *App) OpenCanonicalRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	canonicalRecordReference *trawlkit.CanonicalArchiveRecordReference,
) (*open.OpenRecord, error) {
	openedPersonValues, err := a.loadOpenPerson(ctx, req, canonicalRecordReference)
	if err != nil {
		return nil, err
	}
	canonicalOpenedRecordReference := openedPersonValues.canonicalPersonRecordReference
	if canonicalOpenedRecordReference == "" {
		canonicalOpenedRecordReference = archive.PersonRef(openedPersonValues.archivedPerson.ID)
	}
	personRecordForProduct, err := personRecord(openedPersonValues.archivedPerson)
	if err != nil {
		return nil, err
	}
	record := &open.OpenRecord{
		RecordTrawler:            a.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(canonicalOpenedRecordReference),
		TypedOpenedRecord: &open.OpenRecord_PersonRecord{
			PersonRecord: personRecordForProduct,
		},
	}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}
