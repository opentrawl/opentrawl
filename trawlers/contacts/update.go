package contacts

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/apple"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	update "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"google.golang.org/protobuf/proto"
)

func (a *App) Update(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*update.TrawlerArchiveUpdateReport, error) {
	reportContactProgress(req, "Reading Apple Contacts", 0, 0)
	read := a.readApple
	if read == nil {
		read = apple.ReadSystem
	}
	contacts, err := read(ctx)
	if err != nil {
		return nil, apple.ActionableReadError(err)
	}
	return a.reconcileContacts(ctx, req, "apple", apple.ToSourceContacts(contacts, false))
}

// ReconcilePeopleSnapshot lets the root CLI add another crawler's current
// identities to the People archive without creating a second shared import
// protocol. The source remains authoritative for its own snapshot.
func (a *App) ReconcilePeopleSnapshot(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	peopleSnapshotTrawler *trawlkit.RegisteredTrawlerIdentity,
	snapshot *person.TrawlerPeopleSnapshot,
) (*update.TrawlerArchiveUpdateReport, error) {
	source := trawlkit.RegisteredTrawlerIdentityText(peopleSnapshotTrawler)
	if err := trawlkit.ValidateTrawlerPeopleSnapshot(snapshot); err != nil {
		return nil, fmt.Errorf("invalid %s People snapshot: %w", strings.TrimSpace(source), err)
	}
	contacts := make([]model.SourceContact, 0, len(snapshot.GetTrawlerPersonIdentities()))
	for _, personIdentity := range snapshot.GetTrawlerPersonIdentities() {
		emails := make([]model.ContactValue, 0, len(personIdentity.GetPersonEmailAddresses()))
		for _, email := range personIdentity.GetPersonEmailAddresses() {
			emails = append(emails, model.ContactValue{Value: email})
		}
		phones := make([]model.ContactValue, 0, len(personIdentity.GetPersonPhoneNumbers()))
		for _, phone := range personIdentity.GetPersonPhoneNumbers() {
			phones = append(phones, model.ContactValue{Value: phone})
		}
		accounts := make(map[string][]string, len(personIdentity.GetPersonAccountIdentifiersByServiceName()))
		for serviceName, accountIdentifiers := range personIdentity.GetPersonAccountIdentifiersByServiceName() {
			accounts[serviceName] = append([]string(nil), accountIdentifiers.GetPersonAccountIdentifiers()...)
		}
		personIdentifierWithinTrawlerArchive := strings.TrimSpace(
			personIdentity.GetPersonIdentifierWithinTrawlerArchive(),
		)
		if personIdentifierWithinTrawlerArchive != "" {
			accounts[source] = append(accounts[source], personIdentifierWithinTrawlerArchive)
		}
		latestArchiveRecordTimeInvolvingPersonInSourceArchive := time.Time{}
		if latestArchiveRecordTime := personIdentity.GetLatestArchiveRecordTimeInvolvingPersonInTrawlerArchive(); latestArchiveRecordTime != nil && latestArchiveRecordTime.IsValid() {
			latestArchiveRecordTimeInvolvingPersonInSourceArchive = latestArchiveRecordTime.AsTime()
		}
		contacts = append(contacts, model.SourceContact{
			Source:     source,
			ExternalID: personIdentifierWithinTrawlerArchive,
			Name:       personIdentity.GetPersonDisplayName(),
			Emails:     emails,
			Phones:     phones,
			Accounts:   accounts,
			LatestArchiveRecordTimeInvolvingPersonInSourceArchive: latestArchiveRecordTimeInvolvingPersonInSourceArchive,
			MessageCountInvolvingPersonInSourceArchive:            personIdentity.GetMessageCountInvolvingPersonInTrawlerArchive(),
		})
	}
	return a.reconcileContacts(ctx, req, source, contacts)
}

func (a *App) reconcileContacts(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, source string, contacts []model.SourceContact) (*update.TrawlerArchiveUpdateReport, error) {
	reportContactProgress(req, "Updating People", 0, int64(len(contacts)))
	st, err := archive.Use(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open Contacts archive: %w", err))
	}
	stats, err := st.UpdateContactSnapshot(ctx, source, contacts, time.Now())
	if err != nil {
		return nil, fmt.Errorf("update People from %s: %w", strings.TrimSpace(source), err)
	}
	reportContactProgress(req, "People updated", int64(len(contacts)), int64(len(contacts)))
	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info("contacts_update_complete", strings.Join([]string{
			"source=" + strconv.Quote(strings.TrimSpace(source)),
			"contacts=" + strconv.Itoa(len(contacts)),
			"added=" + strconv.Itoa(stats.Added),
			"updated=" + strconv.Itoa(stats.Updated),
			"removed=" + strconv.Itoa(stats.Removed),
		}, " "))
	}
	return &update.TrawlerArchiveUpdateReport{
		ArchiveRecordCountAddedByThisUpdate:   proto.Uint64(uint64(stats.Added)),
		ArchiveRecordCountUpdatedByThisUpdate: proto.Uint64(uint64(stats.Updated)),
		ArchiveRecordCountRemovedByThisUpdate: proto.Uint64(uint64(stats.Removed)),
	}, nil
}

func reportContactProgress(req *trawlkit.TrawlerCommandExecutionRequest, message string, done, total int64) {
	if req != nil && req.ReportTrawlerCommandProgress != nil {
		req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: "people", Done: done, Total: total, Message: message})
	}
}
