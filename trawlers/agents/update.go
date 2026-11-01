package agents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/agents/internal/agentconversation"
	"github.com/opentrawl/opentrawl/trawlers/agents/internal/claudecode"
	"github.com/opentrawl/opentrawl/trawlers/agents/internal/codex"
	"github.com/opentrawl/opentrawl/trawlers/agents/internal/t3code"
	"github.com/opentrawl/opentrawl/trawlkit"
	update "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

// T3 Code updates a thread's time slightly after it writes the message, so
// each read starts a little before the previous one did.
const t3CodeReadOverlap = 5 * time.Minute

// agentConversationDestination receives what the harness readers read: this
// machine's archive, or an import file for the collecting machine.
type agentConversationDestination interface {
	deliverAgentConversation(ctx context.Context, conversation agentconversation.AgentConversation) error
}

// readProgress is recorded only after the conversations it covers reached
// their destination, so a failed send is read again next time.
type readProgress struct {
	sourceFilesRead            []sourceFileState
	t3CodeStoreReadStartedTime time.Time
}

func (app *App) Update(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*update.TrawlerArchiveUpdateReport, error) {
	database := req.OpenedTrawlerArchiveStore.DB()
	if _, err := database.ExecContext(ctx, agentArchiveSchema); err != nil {
		return nil, err
	}
	turnCountBeforeUpdate, err := archivedTurnCount(ctx, database)
	if err != nil {
		return nil, err
	}
	var addedOrChangedTurnCount uint64
	if app.configuration.CollectingMachineSSHHost != "" {
		err = app.updateThroughCollectingMachine(ctx, req)
	} else {
		addedOrChangedTurnCount, err = app.updateAsCollectingMachine(ctx, req)
	}
	turnCountAfterUpdate, countErr := archivedTurnCount(ctx, database)
	if countErr != nil {
		return nil, countErr
	}
	addedTurnCount := turnCountAfterUpdate - turnCountBeforeUpdate
	return &update.TrawlerArchiveUpdateReport{
		ArchiveRecordCountAddedByThisUpdate:   proto.Uint64(addedTurnCount),
		ArchiveRecordCountUpdatedByThisUpdate: proto.Uint64(addedOrChangedTurnCount - min(addedTurnCount, addedOrChangedTurnCount)),
		ArchiveRecordCountRemovedByThisUpdate: proto.Uint64(0),
	}, err
}

func (*App) RecordSuccessfullyCompletedArchiveUpdate(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) error {
	return writeArchiveUpdateTime(ctx, req.OpenedTrawlerArchiveStore.DB(), updateTimeKindLastSuccessfullyCompletedArchiveUpdate, time.Now())
}

// updateAsCollectingMachine archives this machine's conversations and those
// other machines sent, gives every record a link, and leaves a copy for the
// machines that sent.
func (app *App) updateAsCollectingMachine(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (uint64, error) {
	destination := &thisMachineArchive{store: req.OpenedTrawlerArchiveStore}
	progress, readErr := app.readChangedAgentConversations(ctx, req, destination)
	if err := recordReadProgress(ctx, req.OpenedTrawlerArchiveStore.DB(), progress); err != nil {
		return destination.addedOrChangedTurnCount, err
	}
	appliedImportFileCount, importErr := applyImportFilesFromOtherMachines(ctx, req, destination)
	if err := req.OpenedTrawlerArchiveStore.WithTx(ctx, func(transaction *sql.Tx) error {
		return assignShortReferencesToArchivedRecordsWithoutOne(ctx, transaction)
	}); err != nil {
		return destination.addedOrChangedTurnCount, err
	}
	if appliedImportFileCount > 0 {
		if err := writeArchiveCopyForOtherMachines(ctx, req); err != nil {
			return destination.addedOrChangedTurnCount, err
		}
	}
	return destination.addedOrChangedTurnCount, errors.Join(readErr, importErr)
}

type thisMachineArchive struct {
	store                   *store.Store
	addedOrChangedTurnCount uint64
}

func (archive *thisMachineArchive) deliverAgentConversation(ctx context.Context, conversation agentconversation.AgentConversation) error {
	err := archive.store.WithTx(ctx, func(transaction *sql.Tx) error {
		count, err := writeAgentConversation(ctx, transaction, conversation)
		archive.addedOrChangedTurnCount += count
		return err
	})
	if err != nil {
		return fmt.Errorf("write %s: %w", conversation.CanonicalConversationReference, err)
	}
	return nil
}

// readChangedAgentConversations reads every harness on this machine and
// delivers the conversations that changed since the last recorded read.
func (app *App) readChangedAgentConversations(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	destination agentConversationDestination,
) (readProgress, error) {
	t3CodeStoreIsPresent := fileExists(t3code.T3CodeStateDatabasePath(app.configuration.T3CodeUserDataFolderPath))
	var progress readProgress
	var failures []error
	if t3CodeStoreIsPresent {
		if err := app.readT3Code(ctx, req, destination, &progress); err != nil {
			failures = append(failures, fmt.Errorf("read T3 Code: %w", err))
		}
	}
	if err := app.readClaudeCode(ctx, req, destination, &progress, t3CodeStoreIsPresent); err != nil {
		failures = append(failures, fmt.Errorf("read Claude Code: %w", err))
	}
	if err := app.readCodex(ctx, req, destination, &progress, t3CodeStoreIsPresent); err != nil {
		failures = append(failures, fmt.Errorf("read Codex: %w", err))
	}
	return progress, errors.Join(failures...)
}

func (app *App) readT3Code(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, destination agentConversationDestination, progress *readProgress) error {
	readStarted := time.Now()
	previousReadStarted, err := readArchiveUpdateTime(ctx, req.OpenedTrawlerArchiveStore.DB(), updateTimeKindT3CodeStoreReadStarted)
	if err != nil {
		return err
	}
	changedSince := time.Time{}
	if !previousReadStarted.IsZero() {
		changedSince = previousReadStarted.Add(-t3CodeReadOverlap)
	}
	conversations, err := t3code.ReadT3CodeConversationsChangedSince(ctx, app.configuration.T3CodeUserDataFolderPath, changedSince)
	if err != nil {
		return err
	}
	for conversationIndex, conversation := range conversations {
		req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: "T3 Code threads", Done: int64(conversationIndex), Total: int64(len(conversations))})
		if err := destination.deliverAgentConversation(ctx, app.onThisMachine(conversation)); err != nil {
			return err
		}
	}
	progress.t3CodeStoreReadStartedTime = readStarted
	return nil
}

func (app *App) readClaudeCode(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	destination agentConversationDestination,
	progress *readProgress,
	t3CodeStoreIsPresent bool,
) error {
	transcriptFilePaths, err := claudecode.ClaudeCodeTranscriptFilePaths(app.configuration.ClaudeCodeProjectsFolderPath)
	if err != nil {
		return err
	}
	changedFiles, err := sourceFilesChangedSinceLastRead(ctx, req.OpenedTrawlerArchiveStore.DB(), transcriptFilePaths)
	if err != nil {
		return err
	}
	for fileIndex, changedFile := range changedFiles {
		req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: "Claude Code sessions", Done: int64(fileIndex), Total: int64(len(changedFiles))})
		conversation, entrypoint, err := claudecode.ReadClaudeCodeConversation(changedFile.path)
		if err != nil {
			return err
		}
		sessionIsReadFromT3Code := t3CodeStoreIsPresent && claudecode.ClaudeCodeSessionWasDrivenThroughT3Code(entrypoint)
		if !sessionIsReadFromT3Code {
			if err := destination.deliverAgentConversation(ctx, app.onThisMachine(conversation)); err != nil {
				return err
			}
		}
		progress.sourceFilesRead = append(progress.sourceFilesRead, changedFile)
	}
	return nil
}

func (app *App) readCodex(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	destination agentConversationDestination,
	progress *readProgress,
	t3CodeStoreIsPresent bool,
) error {
	codexStateDatabasePath := filepath.Join(app.configuration.CodexHomeFolderPath, "state_5.sqlite")
	if !fileExists(codexStateDatabasePath) {
		return nil
	}
	threadIndex, err := codex.ReadCodexThreadIndex(ctx, codexStateDatabasePath)
	if err != nil {
		return fmt.Errorf("read thread index: %w", err)
	}
	threadIndexByRolloutFilePath := map[string]codex.CodexThreadIndexEntry{}
	var rolloutFilePaths []string
	for _, entry := range threadIndex {
		if entry.CodexThreadIsAnApprovalReview() || (t3CodeStoreIsPresent && entry.CodexThreadWasDrivenThroughT3Code()) {
			continue
		}
		threadIndexByRolloutFilePath[entry.RolloutFilePath] = entry
		rolloutFilePaths = append(rolloutFilePaths, entry.RolloutFilePath)
	}
	changedFiles, err := sourceFilesChangedSinceLastRead(ctx, req.OpenedTrawlerArchiveStore.DB(), rolloutFilePaths)
	if err != nil {
		return err
	}
	for fileIndex, changedFile := range changedFiles {
		req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: "Codex threads", Done: int64(fileIndex), Total: int64(len(changedFiles))})
		conversation, err := codex.ReadCodexConversation(threadIndexByRolloutFilePath[changedFile.path])
		if err != nil {
			return err
		}
		if err := destination.deliverAgentConversation(ctx, app.onThisMachine(conversation)); err != nil {
			return err
		}
		progress.sourceFilesRead = append(progress.sourceFilesRead, changedFile)
	}
	return nil
}

func (app *App) onThisMachine(conversation agentconversation.AgentConversation) agentconversation.AgentConversation {
	conversation.MachineNameWhereConversationIsStored = app.configuration.MachineName
	return conversation
}

func recordReadProgress(ctx context.Context, database *sql.DB, progress readProgress) error {
	for _, file := range progress.sourceFilesRead {
		if _, err := database.ExecContext(ctx, `INSERT INTO agent_source_files_already_read VALUES (?, ?, ?)
			ON CONFLICT(source_file_path) DO UPDATE SET byte_count = excluded.byte_count, modification_time = excluded.modification_time`,
			file.path, file.byteCount, archiveTimeText(file.modificationTime)); err != nil {
			return err
		}
	}
	if progress.t3CodeStoreReadStartedTime.IsZero() {
		return nil
	}
	return writeArchiveUpdateTime(ctx, database, updateTimeKindT3CodeStoreReadStarted, progress.t3CodeStoreReadStartedTime)
}

type sourceFileState struct {
	path             string
	byteCount        int64
	modificationTime time.Time
}

// sourceFilesChangedSinceLastRead returns, oldest first, the files whose size
// or modification time differs from the last read. Oldest first means an
// original session is archived before a resumed copy of it.
func sourceFilesChangedSinceLastRead(ctx context.Context, database *sql.DB, paths []string) ([]sourceFileState, error) {
	var changed []sourceFileState
	for _, path := range paths {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		current := sourceFileState{path: path, byteCount: info.Size(), modificationTime: info.ModTime()}
		var byteCount int64
		var modificationTime string
		err = database.QueryRowContext(ctx, `SELECT byte_count, modification_time FROM agent_source_files_already_read WHERE source_file_path = ?`, path).
			Scan(&byteCount, &modificationTime)
		if err == nil && byteCount == current.byteCount && modificationTime == archiveTimeText(current.modificationTime) {
			continue
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		changed = append(changed, current)
	}
	sort.Slice(changed, func(left, right int) bool {
		return changed[left].modificationTime.Before(changed[right].modificationTime)
	})
	return changed, nil
}

func readArchiveUpdateTime(ctx context.Context, database *sql.DB, kind string) (time.Time, error) {
	var value string
	err := database.QueryRowContext(ctx, `SELECT update_time FROM agent_archive_update_times WHERE update_time_kind = ?`, kind).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	return timeFromArchiveTimeText(value), err
}

func writeArchiveUpdateTime(ctx context.Context, database *sql.DB, kind string, value time.Time) error {
	_, err := database.ExecContext(ctx, `INSERT INTO agent_archive_update_times VALUES (?, ?)
		ON CONFLICT(update_time_kind) DO UPDATE SET update_time = excluded.update_time`, kind, archiveTimeText(value))
	return err
}

func archivedTurnCount(ctx context.Context, database *sql.DB) (uint64, error) {
	var count uint64
	err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_conversation_turns`).Scan(&count)
	return count, err
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
