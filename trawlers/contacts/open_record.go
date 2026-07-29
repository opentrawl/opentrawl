package contacts

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
)

type openedPersonValuesLoadedFromContactsArchive struct {
	canonicalPersonRecordReference string
	archivedPerson                 model.Person
}

var _ trawlkit.RecordOpener = (*App)(nil)

func (a *App) OpenRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	ref string,
) (*openv1.OpenRecord, error) {
	openedPersonValues, err := a.loadOpenPerson(ctx, req, ref)
	if err != nil {
		return nil, err
	}
	canonicalOpenedRecordReference := openedPersonValues.canonicalPersonRecordReference
	if canonicalOpenedRecordReference == "" {
		canonicalOpenedRecordReference = archive.PersonRef(openedPersonValues.archivedPerson.ID)
	}
	record := &openv1.OpenRecord{
		RegisteredTrawlerManifestIdentity: a.RegisteredTrawlerDeclaration().RegisteredTrawlerManifestIdentity,
		CanonicalOpenedRecordReference:    canonicalOpenedRecordReference,
		TypedOpenedRecord: &openv1.OpenRecord_PersonRecord{
			PersonRecord: personRecord(openedPersonValues.archivedPerson),
		},
	}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}
