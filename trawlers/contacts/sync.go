package clawdex

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
	"github.com/opentrawl/opentrawl/trawlkit/control"
)

func (a *App) Sync(ctx context.Context, req *trawlkit.Request) (*trawlkit.SyncReport, error) {
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
func (a *App) ReconcilePeopleSnapshot(ctx context.Context, req *trawlkit.Request, source string, snapshot *control.PeopleSnapshot) (*trawlkit.SyncReport, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("%s People snapshot is missing", strings.TrimSpace(source))
	}
	if err := control.ValidatePeopleSnapshot(*snapshot); err != nil {
		return nil, fmt.Errorf("invalid %s People snapshot: %w", strings.TrimSpace(source), err)
	}
	contacts := make([]model.SourceContact, 0, len(snapshot.Contacts))
	for _, contact := range snapshot.Contacts {
		emails := make([]model.ContactValue, 0, len(contact.EmailAddresses))
		for _, email := range contact.EmailAddresses {
			emails = append(emails, model.ContactValue{Value: email})
		}
		phones := make([]model.ContactValue, 0, len(contact.PhoneNumbers))
		for _, phone := range contact.PhoneNumbers {
			phones = append(phones, model.ContactValue{Value: phone})
		}
		contacts = append(contacts, model.SourceContact{
			Source:   source,
			Name:     contact.DisplayName,
			Emails:   emails,
			Phones:   phones,
			Accounts: contact.Accounts,
		})
	}
	return a.reconcileContacts(ctx, req, source, contacts)
}

func (a *App) reconcileContacts(ctx context.Context, req *trawlkit.Request, source string, contacts []model.SourceContact) (*trawlkit.SyncReport, error) {
	reportContactProgress(req, "Updating People", 0, int64(len(contacts)))
	st, err := archive.Use(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open Contacts archive: %w", err))
	}
	stats, err := st.SyncContactSnapshot(ctx, source, contacts, time.Now())
	if err != nil {
		return nil, fmt.Errorf("update People from %s: %w", strings.TrimSpace(source), err)
	}
	reportContactProgress(req, "People updated", int64(len(contacts)), int64(len(contacts)))
	if req.Log != nil {
		_ = req.Log.Info("contacts_sync_complete", strings.Join([]string{
			"source=" + strconv.Quote(strings.TrimSpace(source)),
			"contacts=" + strconv.Itoa(len(contacts)),
			"added=" + strconv.Itoa(stats.Added),
			"updated=" + strconv.Itoa(stats.Updated),
			"removed=" + strconv.Itoa(stats.Removed),
		}, " "))
	}
	return &trawlkit.SyncReport{Added: int64(stats.Added), Updated: int64(stats.Updated), Removed: int64(stats.Removed)}, nil
}

func reportContactProgress(req *trawlkit.Request, message string, done, total int64) {
	if req != nil && req.Progress != nil {
		req.Progress(trawlkit.Progress{Phase: "people", Done: done, Total: total, Message: message})
	}
}
