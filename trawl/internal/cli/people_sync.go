package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
)

func (r *Runtime) reconcileSourcePeopleContext(ctx context.Context, source Source, sources []Source) error {
	if source.ID == "contacts" {
		return nil
	}
	if _, ok := source.Crawler.(trawlkit.PeopleSnapshotProvider); !ok {
		return nil
	}
	contacts, found := findSource(sources, "contacts")
	if !found || contacts.Crawler == nil {
		return fmt.Errorf("contacts is not installed")
	}
	if _, ok := contacts.Crawler.(trawlkit.PeopleReconciler); !ok {
		return fmt.Errorf("contacts cannot update the People archive")
	}
	snapshot, err := r.sourceExecutor().PeopleSnapshot(ctx, source.Crawler)
	err = sourceExecutionError("people", err)
	if err != nil {
		return fmt.Errorf("read %s people: %w", sourceHumanName(source), err)
	}
	if snapshot == nil {
		return fmt.Errorf("read %s people: source returned no People snapshot", sourceHumanName(source))
	}
	if err := r.sourceExecutor().ReconcilePeople(ctx, contacts.Crawler, source.ID, snapshot); err != nil {
		return fmt.Errorf("update People from %s: %w", sourceHumanName(source), err)
	}
	return nil
}

func withPeopleSyncFailure(result SyncResult, err error) SyncResult {
	if err == nil {
		return result
	}
	message := "People update failed: " + strings.TrimSpace(err.Error())
	if result.State == "ok" {
		result.State = "partial"
	}
	if result.Message == "" {
		result.Message = message
	} else {
		result.Message += " · " + message
	}
	result.Error = &ErrorBody{
		Code:    "people_sync_failed",
		Message: message,
		Remedy:  "Review OpenTrawl's logs for this source, then sync again.",
	}
	return result
}
