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

var _ trawlkit.RecordOpener = (*App)(nil)

func (a *App) OpenRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (*open.OpenRecord, error) {
	openedPersonValues, err := a.loadOpenPerson(ctx, req, localShortReference)
	if err != nil {
		return nil, err
	}
	canonicalOpenedRecordReference := openedPersonValues.canonicalPersonRecordReference
	if canonicalOpenedRecordReference == "" {
		canonicalOpenedRecordReference = archive.PersonRef(openedPersonValues.archivedPerson.ID)
	}
	record := &open.OpenRecord{
		RecordTrawler:            a.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(canonicalOpenedRecordReference),
		TypedOpenedRecord: &open.OpenRecord_PersonRecord{
			PersonRecord: personRecord(openedPersonValues.archivedPerson),
		},
	}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}
