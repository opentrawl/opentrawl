package federation

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
)

type statusRunResult struct {
	status  *federationv1.SourceStatus
	failure *federationv1.SourceFailure
	skip    *federationv1.SkippedSource
}

func Status(ctx context.Context, sources []StatusSource) *federationv1.StatusResponse {
	results := make([]statusRunResult, len(sources))
	type completedStatus struct {
		index  int
		result statusRunResult
	}
	completed := make(chan completedStatus, len(sources))
	for index := range sources {
		go func(index int) {
			completed <- completedStatus{index: index, result: runStatusSource(ctx, sources[index])}
		}(index)
	}
	remaining := len(sources)
	for remaining > 0 {
		select {
		case completed := <-completed:
			results[completed.index] = completed.result
			remaining--
		case <-ctx.Done():
		drainCompleted:
			for {
				select {
				case completed := <-completed:
					results[completed.index] = completed.result
					remaining--
				default:
					break drainCompleted
				}
			}
			code := federationv1.FailureCode_FAILURE_CODE_CANCELLED
			if ctx.Err() == context.DeadlineExceeded {
				code = federationv1.FailureCode_FAILURE_CODE_TIMEOUT
			}
			for index := range sources {
				if results[index].status == nil && results[index].failure == nil && results[index].skip == nil {
					results[index].failure = operationFailure(sources[index].Manifest, "status", ctx.Err().Error(), code)
				}
			}
			remaining = 0
		}
	}

	response := &federationv1.StatusResponse{}
	successes := 0
	for _, result := range results {
		if result.skip != nil {
			response.SkippedSources = append(response.SkippedSources, result.skip)
			continue
		}
		if result.status != nil {
			response.Sources = append(response.Sources, result.status)
		}
		if result.failure != nil {
			response.Failures = append(response.Failures, result.failure)
			continue
		}
		successes++
	}
	response.Outcome = aggregateOutcome(successes, len(response.Failures), len(response.SkippedSources))
	return response
}

func runStatusSource(ctx context.Context, source StatusSource) (result statusRunResult) {
	if strings.TrimSpace(source.SkipReason) != "" {
		result.skip = skippedSource(source.Manifest, source.SkipReason)
		return result
	}
	if source.Run == nil {
		result.failure = operationFailure(source.Manifest, "status", "callback is nil", federationv1.FailureCode_FAILURE_CODE_INTERNAL)
		return result
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			result = statusRunResult{failure: panicFailure(source.Manifest, "status", recovered)}
		}
	}()
	status, failure := source.Run(ctx)
	if failure != nil {
		result.failure = callbackFailure(ctx, source.Manifest, failure)
		return result
	}
	if ctx.Err() != nil {
		result.failure = callbackFailure(ctx, source.Manifest, &federationv1.SourceFailure{Message: ctx.Err().Error()})
		return result
	}
	if status == nil {
		code := federationv1.FailureCode_FAILURE_CODE_INTERNAL
		if ctx.Err() == context.Canceled {
			code = federationv1.FailureCode_FAILURE_CODE_CANCELLED
		} else if ctx.Err() == context.DeadlineExceeded {
			code = federationv1.FailureCode_FAILURE_CODE_TIMEOUT
		}
		result.failure = operationFailure(source.Manifest, "status", "source returned no status", code)
		return result
	}
	projected, err := ProjectStatus(source.Manifest, status)
	if err != nil {
		result.failure = projectionFailure(source.Manifest, "status", err)
		return result
	}
	result.status = projected
	if projected.State == "missing" || projected.State == "error" {
		result.failure = statusFailure(projected)
	}
	return result
}

func ProjectStatus(manifest control.Manifest, status *control.Status) (*federationv1.SourceStatus, error) {
	if status == nil {
		return nil, fmt.Errorf("source returned no status")
	}
	if strings.TrimSpace(manifest.ID) == "" {
		return nil, fmt.Errorf("manifest source id is empty")
	}
	if status.AppID != manifest.ID {
		return nil, fmt.Errorf("status app id %q does not match manifest id %q", status.AppID, manifest.ID)
	}
	switch status.State {
	case "ok", "empty", "stale", "missing", "error":
	default:
		return nil, fmt.Errorf("status state %q is invalid", status.State)
	}
	out := &federationv1.SourceStatus{
		Manifest: &federationv1.SourceManifest{
			SourceId: manifest.ID,
			Surface:  manifest.DisplayName,
			Branding: &federationv1.Branding{
				SymbolName:       manifest.Branding.SymbolName,
				AccentColor:      manifest.Branding.AccentColor,
				IconPath:         manifest.Branding.IconPath,
				BundleIdentifier: manifest.Branding.BundleIdentifier,
			},
			Headlines:    append([]string(nil), manifest.Headlines...),
			Capabilities: append([]string(nil), manifest.Capabilities...),
		},
		AppId:             status.AppID,
		SchemaVersion:     status.SchemaVersion,
		GeneratedRfc3339:  status.GeneratedAt,
		State:             status.State,
		Summary:           status.Summary,
		ConfigPath:        status.ConfigPath,
		DatabasePath:      status.DatabasePath,
		DatabaseBytes:     status.DatabaseBytes,
		WalBytes:          status.WALBytes,
		LastSyncRfc3339:   status.LastSyncAt,
		LastImportRfc3339: status.LastImportAt,
		LastExportRfc3339: status.LastExportAt,
		Counts:            projectCounts(status.Counts),
		Databases:         projectDatabases(status.Databases),
		SetupRequirements: projectSetupRequirements(status.SetupRequirements),
		Warnings:          append([]string(nil), status.Warnings...),
		Errors:            append([]string(nil), status.Errors...),
	}
	if status.Freshness != nil {
		out.Freshness = &federationv1.Freshness{
			Status:            status.Freshness.Status,
			AgeSeconds:        status.Freshness.AgeSeconds,
			StaleAfterSeconds: status.Freshness.StaleAfterSeconds,
		}
	}
	if status.Share != nil {
		out.Share = &federationv1.Share{
			Enabled:     status.Share.Enabled,
			RepoPath:    status.Share.RepoPath,
			Remote:      status.Share.Remote,
			Branch:      status.Share.Branch,
			NeedsUpdate: status.Share.NeedsUpdate,
		}
	}
	if status.Remote != nil {
		out.Remote = &federationv1.Remote{
			Enabled:           status.Remote.Enabled,
			Mode:              status.Remote.Mode,
			Endpoint:          status.Remote.Endpoint,
			Archive:           status.Remote.Archive,
			LastIngestRfc3339: status.Remote.LastIngestAt,
			LastSyncRfc3339:   status.Remote.LastSyncAt,
			NeedsUpdate:       status.Remote.NeedsUpdate,
		}
	}
	return out, nil
}

func statusFailure(status *federationv1.SourceStatus) *federationv1.SourceFailure {
	code := federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE
	if status.State == "error" {
		code = federationv1.FailureCode_FAILURE_CODE_INTERNAL
	}
	for _, requirement := range status.SetupRequirements {
		if requirement.State != federationv1.SetupState_SETUP_STATE_NEEDS_ACTION {
			continue
		}
		switch requirement.Kind {
		case federationv1.SetupKind_SETUP_KIND_FULL_DISK_ACCESS, federationv1.SetupKind_SETUP_KIND_PHOTOS_PERMISSION:
			code = federationv1.FailureCode_FAILURE_CODE_PERMISSION
		case federationv1.SetupKind_SETUP_KIND_ACCOUNT:
			code = federationv1.FailureCode_FAILURE_CODE_AUTHENTICATION
		}
	}
	return &federationv1.SourceFailure{
		SourceId: status.Manifest.SourceId,
		Surface:  status.Manifest.Surface,
		Code:     code,
		Message:  firstText(status.Summary, "The source is unavailable."),
		Remedy:   "trawl doctor " + status.Manifest.SourceId,
	}
}

func projectCounts(counts []control.Count) []*federationv1.Count {
	out := make([]*federationv1.Count, 0, len(counts))
	for _, count := range counts {
		out = append(out, &federationv1.Count{Id: count.ID, Label: count.Label, Value: count.Value})
	}
	return out
}

func projectDatabases(databases []control.Database) []*federationv1.Database {
	out := make([]*federationv1.Database, 0, len(databases))
	for _, database := range databases {
		out = append(out, &federationv1.Database{
			Id:              database.ID,
			Label:           database.Label,
			Kind:            database.Kind,
			Role:            database.Role,
			Path:            database.Path,
			Endpoint:        database.Endpoint,
			Archive:         database.Archive,
			IsPrimary:       database.IsPrimary,
			Bytes:           database.Bytes,
			ModifiedRfc3339: database.ModifiedAt,
			Counts:          projectCounts(database.Counts),
		})
	}
	return out
}

func projectSetupRequirements(requirements []control.SetupRequirement) []*federationv1.SetupRequirement {
	out := make([]*federationv1.SetupRequirement, 0, len(requirements))
	for _, requirement := range requirements {
		out = append(out, &federationv1.SetupRequirement{
			Id:          requirement.ID,
			Kind:        setupKind(requirement.Kind),
			State:       setupState(requirement.State),
			Explanation: requirement.Explanation,
			Action:      setupAction(requirement.Action),
			Command:     append([]string(nil), requirement.Command...),
		})
	}
	return out
}

func setupKind(kind control.SetupKind) federationv1.SetupKind {
	switch kind {
	case control.SetupKindFullDiskAccess:
		return federationv1.SetupKind_SETUP_KIND_FULL_DISK_ACCESS
	case control.SetupKindPhotosPermission:
		return federationv1.SetupKind_SETUP_KIND_PHOTOS_PERMISSION
	case control.SetupKindAccount:
		return federationv1.SetupKind_SETUP_KIND_ACCOUNT
	case control.SetupKindPairing:
		return federationv1.SetupKind_SETUP_KIND_PAIRING
	case control.SetupKindArchiveImport:
		return federationv1.SetupKind_SETUP_KIND_ARCHIVE_IMPORT
	default:
		return federationv1.SetupKind_SETUP_KIND_UNSPECIFIED
	}
}

func setupState(state control.SetupState) federationv1.SetupState {
	switch state {
	case control.SetupStateReady:
		return federationv1.SetupState_SETUP_STATE_READY
	case control.SetupStateNeedsAction:
		return federationv1.SetupState_SETUP_STATE_NEEDS_ACTION
	case control.SetupStateUnavailable:
		return federationv1.SetupState_SETUP_STATE_UNAVAILABLE
	default:
		return federationv1.SetupState_SETUP_STATE_UNSPECIFIED
	}
}

func setupAction(action control.SetupActionKind) federationv1.SetupActionKind {
	switch action {
	case control.SetupActionNone:
		return federationv1.SetupActionKind_SETUP_ACTION_KIND_NONE
	case control.SetupActionOpenFullDiskAccess:
		return federationv1.SetupActionKind_SETUP_ACTION_KIND_OPEN_FULL_DISK_ACCESS
	case control.SetupActionRequestPhotos:
		return federationv1.SetupActionKind_SETUP_ACTION_KIND_REQUEST_PHOTOS
	case control.SetupActionRunCommand:
		return federationv1.SetupActionKind_SETUP_ACTION_KIND_RUN_COMMAND
	case control.SetupActionChooseArchive:
		return federationv1.SetupActionKind_SETUP_ACTION_KIND_CHOOSE_ARCHIVE
	default:
		return federationv1.SetupActionKind_SETUP_ACTION_KIND_UNSPECIFIED
	}
}

func firstText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
