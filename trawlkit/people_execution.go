package trawlkit

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	workerv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/worker/v1"
)

const internalPeopleReconcileVerb = "__people-reconcile"

type typedPeopleReconcile struct {
	source   string
	snapshot *control.PeopleSnapshot
}

func (operation *typedPeopleReconcile) execute(ctx context.Context, destination Crawler, req *Request) error {
	reconciler, ok := destination.(PeopleReconciler)
	if !ok {
		return errors.New("destination does not own a People archive")
	}
	_, err := reconciler.ReconcilePeopleSnapshot(ctx, req, operation.source, operation.snapshot)
	return err
}

func peopleSnapshotToProto(snapshot *control.PeopleSnapshot) *workerv1.PeopleSnapshot {
	result := &workerv1.PeopleSnapshot{Contacts: make([]*workerv1.Contact, 0, len(snapshot.Contacts))}
	for _, contact := range snapshot.Contacts {
		accounts := make(map[string]*workerv1.AccountValues, len(contact.Accounts))
		for provider, values := range contact.Accounts {
			accounts[provider] = &workerv1.AccountValues{Values: append([]string(nil), values...)}
		}
		result.Contacts = append(result.Contacts, &workerv1.Contact{
			SourceId:       contact.SourceID,
			DisplayName:    contact.DisplayName,
			EmailAddresses: append([]string(nil), contact.EmailAddresses...),
			PhoneNumbers:   append([]string(nil), contact.PhoneNumbers...),
			Accounts:       accounts,
		})
	}
	return result
}

func peopleSnapshotFromProto(snapshot *workerv1.PeopleSnapshot) (*control.PeopleSnapshot, error) {
	if snapshot == nil {
		return nil, errors.New("people snapshot is missing")
	}
	result := &control.PeopleSnapshot{Contacts: make([]control.Contact, 0, len(snapshot.GetContacts()))}
	for _, contact := range snapshot.GetContacts() {
		if contact == nil {
			return nil, errors.New("people snapshot contains an empty contact")
		}
		accounts := make(map[string][]string, len(contact.GetAccounts()))
		for provider, values := range contact.GetAccounts() {
			if values == nil {
				return nil, fmt.Errorf("people contact %q contains empty %s accounts", contact.GetDisplayName(), provider)
			}
			accounts[provider] = append([]string(nil), values.GetValues()...)
		}
		result.Contacts = append(result.Contacts, control.Contact{
			SourceID:       contact.GetSourceId(),
			DisplayName:    contact.GetDisplayName(),
			EmailAddresses: append([]string(nil), contact.GetEmailAddresses()...),
			PhoneNumbers:   append([]string(nil), contact.GetPhoneNumbers()...),
			Accounts:       accounts,
		})
	}
	if err := control.ValidatePeopleSnapshot(*result); err != nil {
		return nil, fmt.Errorf("invalid people snapshot: %w", err)
	}
	return result, nil
}
