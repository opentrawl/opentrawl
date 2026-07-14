package federation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

func TestFederatedStatusPreservesFacts(t *testing.T) {
	manifest, status := completeStatusFixture()
	projected, err := ProjectStatus(manifest, status)
	if err != nil {
		t.Fatal(err)
	}
	if projected.Manifest.SourceId != manifest.ID || projected.Manifest.DisplayName != manifest.DisplayName {
		t.Fatalf("manifest identity = %#v", projected.Manifest)
	}
	if !proto.Equal(projected.Manifest.Branding, &federationv1.Branding{
		SymbolName: "envelope", AccentColor: "#3366FF", IconPath: "icons/mail.png", BundleIdentifier: "com.example.Mail",
	}) {
		t.Fatalf("branding = %#v", projected.Manifest.Branding)
	}
	if len(projected.Manifest.Headlines) != 4 || projected.Manifest.Headlines[3] != "labels" {
		t.Fatalf("headlines = %#v", projected.Manifest.Headlines)
	}
	if len(projected.Counts) != 2 || projected.Counts[0].Value != 42 || projected.Counts[1].Value != 7 {
		t.Fatalf("counts = %#v", projected.Counts)
	}
	if projected.Freshness == nil || projected.Freshness.AgeSeconds != 90 || projected.Share == nil || projected.Remote == nil {
		t.Fatalf("optional status facts were lost: %#v", projected)
	}
	if len(projected.Databases) != 1 || len(projected.Databases[0].Counts) != 1 {
		t.Fatalf("databases = %#v", projected.Databases)
	}
	if len(projected.SetupRequirements) != 1 || projected.SetupRequirements[0].Kind != federationv1.SetupKind_SETUP_KIND_ACCOUNT {
		t.Fatalf("setup = %#v", projected.SetupRequirements)
	}
}

func TestProjectStatusPinsCompleteProtobufText(t *testing.T) {
	manifest := manifestFixture("notes", "Notes")
	status := &control.Status{
		SchemaVersion: "trawlkit.control.v1",
		AppID:         "notes",
		GeneratedAt:   "2026-07-12T09:00:00Z",
		State:         "empty",
		Summary:       "The archive is ready but empty.",
		Counts:        []control.Count{control.NewCount("notes", "notes", 0)},
	}
	projected, err := ProjectStatus(manifest, status)
	if err != nil {
		t.Fatal(err)
	}
	want := "" +
		"manifest:  {\n" +
		"  source_id:  \"notes\"\n" +
		"  display_name:  \"Notes\"\n" +
		"  branding:  {}\n" +
		"  headlines:  \"items\"\n" +
		"  capabilities:  \"status\"\n" +
		"  capabilities:  \"search\"\n" +
		"}\n" +
		"app_id:  \"notes\"\n" +
		"schema_version:  \"trawlkit.control.v1\"\n" +
		"generated_rfc3339:  \"2026-07-12T09:00:00Z\"\n" +
		"state:  \"empty\"\n" +
		"summary:  \"The archive is ready but empty.\"\n" +
		"counts:  {\n" +
		"  id:  \"notes\"\n" +
		"  label:  \"notes\"\n" +
		"}\n"
	if got := prototext.Format(projected); got != want {
		t.Fatalf("status protobuf text changed\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestStatusAcceptsFiveStatesAndRejectsUnknown(t *testing.T) {
	manifest, status := completeStatusFixture()
	for _, state := range []string{"ok", "empty", "stale", "missing", "error"} {
		copy := *status
		copy.State = state
		if _, err := ProjectStatus(manifest, &copy); err != nil {
			t.Fatalf("state %q: %v", state, err)
		}
	}
	copy := *status
	copy.State = "unknown"
	if _, err := ProjectStatus(manifest, &copy); err == nil {
		t.Fatal("unknown status state was accepted")
	}
}

func TestStatusPreservesRegistryOrderAndTypedProblems(t *testing.T) {
	readyManifest, readyStatus := completeStatusFixture()
	missingManifest := manifestFixture("photos", "Photos")
	missingStatus := &control.Status{
		SchemaVersion: "trawlkit.control.v1",
		AppID:         "photos",
		GeneratedAt:   "2026-07-12T09:00:00Z",
		State:         "missing",
		Summary:       "Photos access is required.",
		SetupRequirements: []control.SetupRequirement{
			control.NewSetupRequirement("photos", control.SetupKindPhotosPermission, control.SetupStateNeedsAction, "Allow Photos access.", control.SetupActionRequestPhotos, nil),
		},
	}
	response := Status(context.Background(), []StatusSource{
		{Manifest: readyManifest, Run: func(context.Context) (*control.Status, *federationv1.SourceFailure) { return readyStatus, nil }},
		{Manifest: missingManifest, Run: func(context.Context) (*control.Status, *federationv1.SourceFailure) { return missingStatus, nil }},
		{Manifest: manifestFixture("notes", "Notes"), SkipReason: "not selected"},
		{Manifest: manifestFixture("calendar", "Calendar"), Run: func(context.Context) (*control.Status, *federationv1.SourceFailure) {
			return nil, &federationv1.SourceFailure{Code: federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE, Message: "Calendar is unavailable."}
		}},
	})
	if response.Outcome != federationv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL {
		t.Fatalf("outcome = %s", response.Outcome)
	}
	if got := []string{response.Sources[0].AppId, response.Sources[1].AppId}; got[0] != "gmail" || got[1] != "photos" {
		t.Fatalf("sources = %#v", got)
	}
	if len(response.Failures) != 2 || response.Failures[0].Code != federationv1.FailureCode_FAILURE_CODE_PERMISSION || response.Failures[1].SourceId != "calendar" {
		t.Fatalf("failures = %#v", response.Failures)
	}
	if len(response.SkippedSources) != 1 || response.SkippedSources[0].SourceId != "notes" {
		t.Fatalf("skips = %#v", response.SkippedSources)
	}

	onlySkipped := Status(context.Background(), []StatusSource{{Manifest: manifestFixture("notes", "Notes"), SkipReason: "not selected"}})
	if onlySkipped.Outcome != federationv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(onlySkipped.Failures) != 0 {
		t.Fatalf("only skipped = %s, %#v", onlySkipped.Outcome, onlySkipped.Failures)
	}

	allFailed := Status(context.Background(), []StatusSource{{
		Manifest: manifestFixture("calendar", "Calendar"),
		Run: func(context.Context) (*control.Status, *federationv1.SourceFailure) {
			return nil, &federationv1.SourceFailure{Message: "Calendar is unavailable."}
		},
	}})
	if allFailed.Outcome != federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED {
		t.Fatalf("all failed = %s", allFailed.Outcome)
	}
}

func TestStatusPreservesInputOrderWhenCallbacksFinishOutOfOrder(t *testing.T) {
	secondFinished := make(chan struct{})
	firstManifest, firstStatus := completeStatusFixture()
	secondManifest := manifestFixture("notes", "Notes")
	secondStatus := &control.Status{SchemaVersion: "trawlkit.control.v1", AppID: "notes", GeneratedAt: "2026-07-12T09:00:00Z", State: "ok"}
	response := Status(context.Background(), []StatusSource{
		{Manifest: firstManifest, Run: func(context.Context) (*control.Status, *federationv1.SourceFailure) {
			<-secondFinished
			return firstStatus, nil
		}},
		{Manifest: secondManifest, Run: func(context.Context) (*control.Status, *federationv1.SourceFailure) {
			close(secondFinished)
			return secondStatus, nil
		}},
	})
	if got := []string{response.Sources[0].AppId, response.Sources[1].AppId}; got[0] != "gmail" || got[1] != "notes" {
		t.Fatalf("sources = %#v", got)
	}
}

func TestMissingStatusMapsSetupRequirementFailures(t *testing.T) {
	tests := []struct {
		kind control.SetupKind
		want federationv1.FailureCode
	}{
		{control.SetupKindFullDiskAccess, federationv1.FailureCode_FAILURE_CODE_PERMISSION},
		{control.SetupKindPhotosPermission, federationv1.FailureCode_FAILURE_CODE_PERMISSION},
		{control.SetupKindAccount, federationv1.FailureCode_FAILURE_CODE_AUTHENTICATION},
		{control.SetupKindArchiveImport, federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE},
	}
	for _, test := range tests {
		manifest := manifestFixture("source", "Source")
		status := &control.Status{
			SchemaVersion: "trawlkit.control.v1", AppID: "source", GeneratedAt: "2026-07-12T09:00:00Z", State: "missing", Summary: "Setup is required.",
			SetupRequirements: []control.SetupRequirement{control.NewSetupRequirement("setup", test.kind, control.SetupStateNeedsAction, "Set up source.", control.SetupActionRunCommand, []string{"source", "setup"})},
		}
		response := Status(context.Background(), []StatusSource{{Manifest: manifest, Run: func(context.Context) (*control.Status, *federationv1.SourceFailure) { return status, nil }}})
		if response.Failures[0].Code != test.want {
			t.Fatalf("kind %s: got %s, want %s", test.kind, response.Failures[0].Code, test.want)
		}
	}
}

func TestStatusMapsPanicCancellationAndDeadline(t *testing.T) {
	panicResult := Status(context.Background(), []StatusSource{{
		Manifest: manifestFixture("notes", "Notes"),
		Run:      func(context.Context) (*control.Status, *federationv1.SourceFailure) { panic("synthetic panic") },
	}})
	if panicResult.Failures[0].Code != federationv1.FailureCode_FAILURE_CODE_INTERNAL {
		t.Fatalf("panic code = %s", panicResult.Failures[0].Code)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cancelResult := Status(cancelled, []StatusSource{{
		Manifest: manifestFixture("notes", "Notes"),
		Run: func(context.Context) (*control.Status, *federationv1.SourceFailure) {
			return nil, &federationv1.SourceFailure{Message: "stopped"}
		},
	}})
	if cancelResult.Failures[0].Code != federationv1.FailureCode_FAILURE_CODE_CANCELLED {
		t.Fatalf("cancel code = %s", cancelResult.Failures[0].Code)
	}

	deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	deadlineResult := Status(deadline, []StatusSource{{
		Manifest: manifestFixture("notes", "Notes"),
		Run: func(context.Context) (*control.Status, *federationv1.SourceFailure) {
			return nil, &federationv1.SourceFailure{Message: "late"}
		},
	}})
	if deadlineResult.Failures[0].Code != federationv1.FailureCode_FAILURE_CODE_TIMEOUT {
		t.Fatalf("deadline code = %s", deadlineResult.Failures[0].Code)
	}
}

func TestStatusDeadlineKeepsCompletedSources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	readyManifest, readyStatus := completeStatusFixture()
	response := Status(ctx, []StatusSource{
		{Manifest: readyManifest, Run: func(context.Context) (*control.Status, *federationv1.SourceFailure) { return readyStatus, nil }},
		{Manifest: manifestFixture("notes", "Notes"), Run: func(ctx context.Context) (*control.Status, *federationv1.SourceFailure) {
			<-release
			return nil, &federationv1.SourceFailure{Message: "late"}
		}},
	})
	if len(response.Sources) != 1 || response.Sources[0].AppId != "gmail" {
		t.Fatalf("completed sources = %#v", response.Sources)
	}
	if len(response.Failures) != 1 || response.Failures[0].Code != federationv1.FailureCode_FAILURE_CODE_TIMEOUT {
		t.Fatalf("timeout failure = %#v", response.Failures)
	}
}

func TestStatusBoundaryEvidence(t *testing.T) {
	readyManifest, readyStatus := completeStatusFixture()
	missingManifest := manifestFixture("photos", "Photos")
	failedManifest := manifestFixture("calendar", "Calendar")
	skippedManifest := manifestFixture("notes", "Notes")
	missingStatus := &control.Status{
		SchemaVersion: "trawlkit.control.v1", AppID: "photos", GeneratedAt: "2026-07-12T09:02:00Z",
		State: "missing", Summary: "Photos access is required.",
		SetupRequirements: []control.SetupRequirement{
			control.NewSetupRequirement("photos", control.SetupKindPhotosPermission, control.SetupStateNeedsAction, "Allow Photos access.", control.SetupActionRequestPhotos, nil),
		},
	}
	input := struct {
		ReadyManifest   control.Manifest `json:"ready_manifest"`
		ReadyStatus     *control.Status  `json:"ready_status"`
		MissingManifest control.Manifest `json:"missing_manifest"`
		MissingStatus   *control.Status  `json:"missing_status"`
		FailedManifest  control.Manifest `json:"failed_manifest"`
		SkippedManifest control.Manifest `json:"skipped_manifest"`
	}{readyManifest, readyStatus, missingManifest, missingStatus, failedManifest, skippedManifest}
	inputBytes, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	inputBytes = append(inputBytes, '\n')
	readyProjected, err := ProjectStatus(readyManifest, readyStatus)
	if err != nil {
		t.Fatal(err)
	}
	missingProjected, err := ProjectStatus(missingManifest, missingStatus)
	if err != nil {
		t.Fatal(err)
	}
	projected := &federationv1.StatusResponse{Sources: []*federationv1.SourceStatus{readyProjected, missingProjected}}
	response := Status(context.Background(), []StatusSource{
		{Manifest: readyManifest, Run: func(context.Context) (*control.Status, *federationv1.SourceFailure) { return readyStatus, nil }},
		{Manifest: missingManifest, Run: func(context.Context) (*control.Status, *federationv1.SourceFailure) { return missingStatus, nil }},
		{Manifest: failedManifest, Run: func(context.Context) (*control.Status, *federationv1.SourceFailure) {
			return nil, &federationv1.SourceFailure{Code: federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE, Message: "Calendar is unavailable."}
		}},
		{Manifest: skippedManifest, SkipReason: "not selected"},
	})
	writeEvidence(t, "status-input.json", inputBytes)
	writeEvidence(t, "status-projected.pbtxt", []byte(prototext.Format(projected)))
	writeEvidence(t, "status-response.pbtxt", []byte(prototext.Format(response)))
}

func completeStatusFixture() (control.Manifest, *control.Status) {
	manifest := manifestFixture("gmail", "Gmail")
	manifest.Branding = control.Branding{SymbolName: "envelope", AccentColor: "#3366FF", IconPath: "icons/mail.png", BundleIdentifier: "com.example.Mail"}
	manifest.Headlines = []string{"mail", "attachments", "threads", "labels"}
	manifest.Capabilities = []string{"search", "sync", "open", "mail", "attachments"}
	status := &control.Status{
		SchemaVersion: "trawlkit.control.v1", AppID: "gmail", GeneratedAt: "2026-07-12T09:00:00+02:00",
		State: "ok", Summary: "Gmail is ready.", ConfigPath: "/synthetic/config.toml", DatabasePath: "/synthetic/gmail.db",
		DatabaseBytes: 4096, WALBytes: 512, LastSyncAt: "2026-07-12T08:58:30+02:00", LastImportAt: "2026-07-11T12:00:00Z", LastExportAt: "2026-07-10T12:00:00Z",
		Counts:            []control.Count{control.NewCount("messages", "messages", 42), control.NewCount("attachments", "attachments", 7)},
		Freshness:         &control.Freshness{Status: "fresh", AgeSeconds: 90, StaleAfterSeconds: 3600},
		Share:             &control.Share{Enabled: true, RepoPath: "/synthetic/share", Remote: "origin", Branch: "main", NeedsUpdate: true},
		Remote:            &control.Remote{Enabled: true, Mode: "archive", Endpoint: "https://example.com", Archive: "mail", LastIngestAt: "2026-07-12T08:00:00Z", LastSyncAt: "2026-07-12T08:30:00Z", NeedsUpdate: false},
		Databases:         []control.Database{{ID: "mail", Label: "Mail", Kind: "sqlite", Role: "archive", Path: "/synthetic/gmail.db", IsPrimary: true, Bytes: 4096, ModifiedAt: "2026-07-12T08:59:00Z", Counts: []control.Count{control.NewCount("rows", "rows", 49)}}},
		SetupRequirements: []control.SetupRequirement{control.NewSetupRequirement("account", control.SetupKindAccount, control.SetupStateReady, "Account is connected.", control.SetupActionNone, nil)},
		Warnings:          []string{"Synthetic warning."}, Errors: []string{},
	}
	return manifest, status
}

func manifestFixture(id, displayName string) control.Manifest {
	manifest := control.NewManifest(id, displayName, id)
	manifest.Headlines = []string{"items"}
	manifest.Capabilities = []string{"status", "search"}
	return manifest
}

func writeEvidence(t *testing.T, name string, content []byte) {
	t.Helper()
	directory := os.Getenv("OPENTRAWL_EVIDENCE_DIR")
	if directory == "" {
		return
	}
	if len(content) == 0 {
		t.Fatalf("evidence %s is empty", name)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	readBack, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(readBack) != string(content) {
		t.Fatalf("evidence %s changed on write", name)
	}
}
