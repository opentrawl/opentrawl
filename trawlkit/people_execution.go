package trawlkit

import (
	"context"
	"errors"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	update "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
)

const internalPeopleReconcileTrawlerCommand = "__people-reconcile"

type executePeopleReconciliationOperation struct {
	peopleSnapshotTrawler *RegisteredTrawlerIdentity
	snapshot              *person.TrawlerPeopleSnapshot
	report                *update.TrawlerArchiveUpdateReport
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
		report = &update.TrawlerArchiveUpdateReport{}
	}
	operation.report = report
	return err
}
