package cli

import (
	"context"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlkit"
)

func (r *Runtime) reconcileTrawlerPeopleContext(ctx context.Context, trawler InstalledTrawler, installedTrawlers []InstalledTrawler) error {
	if trawler.RegisteredTrawlerManifestIdentity == "contacts" {
		return nil
	}
	if _, ok := trawler.Trawler.(trawlkit.PeopleSnapshotProvider); !ok {
		return nil
	}
	contacts, found := findInstalledTrawler(installedTrawlers, "contacts")
	if !found || contacts.Trawler == nil {
		return fmt.Errorf("contacts is not installed")
	}
	if _, ok := contacts.Trawler.(trawlkit.PeopleReconciler); !ok {
		return fmt.Errorf("contacts cannot update the People archive")
	}
	snapshot, err := r.trawlerExecutor().PeopleSnapshot(ctx, trawler.Trawler)
	err = trawlerExecutionError("people", err)
	if err != nil {
		return fmt.Errorf("read %s people: %w", trawlerHumanName(trawler), err)
	}
	if snapshot == nil {
		return fmt.Errorf("read %s people: trawler returned no People snapshot", trawlerHumanName(trawler))
	}
	if err := r.trawlerExecutor().ReconcilePeople(ctx, contacts.Trawler, trawler.RegisteredTrawlerManifestIdentity, snapshot); err != nil {
		return fmt.Errorf("update People from %s: %w", trawlerHumanName(trawler), err)
	}
	return nil
}
