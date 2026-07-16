package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
)

type contactReconciler interface {
	ReconcileContactExport(context.Context, *trawlkit.Request, string, *control.ContactExport) (*trawlkit.SyncReport, error)
}

func (r *Runtime) reconcileSourcePeople(source Source, sources []Source) error {
	if source.ID == "contacts" {
		return nil
	}
	exporter, ok := source.Crawler.(trawlkit.ContactExporter)
	if !ok {
		return nil
	}
	contacts, found := findSource(sources, "contacts")
	if !found || contacts.Crawler == nil {
		return fmt.Errorf("Contacts is not installed")
	}
	reconciler, ok := contacts.Crawler.(contactReconciler)
	if !ok {
		return fmt.Errorf("Contacts cannot update People from %s", sourceHumanName(source))
	}
	var exported *control.ContactExport
	if err := r.withSourceRequest(source, "contacts", sourceStoreRead, outputFormat(true), io.Discard, func(ctx context.Context, req *trawlkit.Request) error {
		var exportErr error
		exported, exportErr = exporter.ContactExport(ctx, req)
		return exportErr
	}); err != nil {
		return fmt.Errorf("read %s people: %w", sourceHumanName(source), err)
	}
	if exported == nil {
		return fmt.Errorf("read %s people: source returned no contact snapshot", sourceHumanName(source))
	}
	if err := r.withSourceRequest(contacts, "sync", sourceStoreWrite, outputFormat(true), io.Discard, func(ctx context.Context, req *trawlkit.Request) error {
		_, reconcileErr := reconciler.ReconcileContactExport(ctx, req, source.ID, exported)
		return reconcileErr
	}); err != nil {
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
