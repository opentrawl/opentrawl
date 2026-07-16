package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	clawdex "github.com/opentrawl/opentrawl/trawlers/contacts"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
)

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
	input, cleanup, err := writeContactExport(exported)
	if err != nil {
		return fmt.Errorf("stage %s people: %w", sourceHumanName(source), err)
	}
	defer cleanup()
	out, runErr := runTrawlkitCaptured(r.ctx, []string{contacts.ID, clawdex.InternalPeopleReconcileVerb, "--source", source.ID, "--input", input, "--json"}, []trawlkit.Crawler{contacts.Crawler})
	if runErr != nil {
		return fmt.Errorf("update People from %s: %w", sourceHumanName(source), runErr)
	}
	if out.Code != 0 {
		return fmt.Errorf("update People from %s: %w", sourceHumanName(source), crawlerCommandError{command: "People update", err: exitErr{code: out.Code}})
	}
	return nil
}

func writeContactExport(exported *control.ContactExport) (string, func(), error) {
	file, err := os.CreateTemp("", "opentrawl-contact-export-*.json")
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := json.NewEncoder(file).Encode(exported); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
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
