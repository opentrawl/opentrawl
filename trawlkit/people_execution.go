package trawlkit

import (
	"context"
	"errors"

	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	syncv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/sync/v1"
)

const internalPeopleReconcileTrawlerCommand = "__people-reconcile"

type executePeopleReconciliationOperation struct {
	peopleSnapshotTrawler *RegisteredTrawlerIdentity
	snapshot              *personv1.TrawlerPeopleSnapshot
	report                *syncv1.TrawlerArchiveSyncReport
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
		report = &syncv1.TrawlerArchiveSyncReport{}
	}
	operation.report = report
	return err
}
