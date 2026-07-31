package trawlkit

import (
	"context"
	"errors"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	sync "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/sync"
)

const internalPeopleReconcileTrawlerCommand = "__people-reconcile"

type executePeopleReconciliationOperation struct {
	peopleSnapshotTrawler *RegisteredTrawlerIdentity
	snapshot              *person.TrawlerPeopleSnapshot
	report                *sync.TrawlerArchiveSyncReport
}

func (operation *executePeopleReconciliationOperation) execute(ctx context.Context, destination Trawler, req *TrawlerCommandExecutionRequest) error {
	reconciler, ok := destination.(PeopleReconciler)
	if !ok {
		return errors.New("destination does not own a People archive")
	}
	report, err := reconciler.ReconcilePeopleSnapshot(
		ctx,
		req,
		operation.peopleSnapshotTrawler,
		operation.snapshot,
	)
	if err == nil && report == nil {
		report = &sync.TrawlerArchiveSyncReport{}
	}
	operation.report = report
	return err
}
