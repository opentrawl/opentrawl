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
	"google.golang.org/protobuf/proto"
)

// T3 Code updates a thread's time slightly after it writes the message, so
// each read starts a little before the previous one did.
const t3CodeReadOverlap = 5 * time.Minute

func (app *App) Update(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*update.TrawlerArchiveUpdateReport, error) {
	database := req.OpenedTrawlerArchiveStore.DB()
	if _, err := database.ExecContext(ctx, agentArchiveSchema); err != nil {
		return nil, err
	}
	turnCountBeforeUpdate, err := archivedTurnCount(ctx, database)
	if err != nil {
		return nil, err
	}
	t3CodeStoreIsPresent := fileExists(t3code.T3CodeStateDatabasePath(app.configuration.T3CodeUserDataFolderPath))
	var addedOrChangedTurnCount uint64
	var failures []error
	for _, readHarness := range []func(context.Context, *trawlkit.TrawlerCommandExecutionRequest, bool) (uint64, error){
		app.updateFromT3Code, app.updateFromClaudeCode, app.updateFromCodex,
	} {
		count, err := readHarness(ctx, req, t3CodeStoreIsPresent)
		addedOrChangedTurnCount += count
		if err != nil {
			failures = append(failures, err)
		}
	}
	if err := req.OpenedTrawlerArchiveStore.WithTx(ctx, func(transaction *sql.Tx) error {
		return assignShortReferencesToArchivedRecordsWithoutOne(ctx, transaction)
	}); err != nil {
		return nil, err
	}
	turnCountAfterUpdate, err := archivedTurnCount(ctx, database)
	if err != nil {
		return nil, err
	}
	addedTurnCount := turnCountAfterUpdate - turnCountBeforeUpdate
	return &update.TrawlerArchiveUpdateReport{
		ArchiveRecordCountAddedByThisUpdate:   proto.Uint64(addedTurnCount),
		ArchiveRecordCountUpdatedByThisUpdate: proto.Uint64(addedOrChangedTurnCount - min(addedTurnCount, addedOrChangedTurnCount)),
		ArchiveRecordCountRemovedByThisUpdate: proto.Uint64(0),
	}, errors.Join(failures...)
}

func (*App) RecordSuccessfullyCompletedArchiveUpdate(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) error {
	return writeArchiveUpdateTime(ctx, req.OpenedTrawlerArchiveStore.DB(), updateTimeKindLastSuccessfullyCompletedArchiveUpdate, time.Now())
}

func (app *App) updateFromT3Code(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, t3CodeStoreIsPresent bool) (uint64, error) {
	if !t3CodeStoreIsPresent {
		return 0, nil
	}
	database := req.OpenedTrawlerArchiveStore.DB()
	readStarted := time.Now()
	previousReadStarted, err := readArchiveUpdateTime(ctx, database, updateTimeKindT3CodeStoreReadStarted)
	if err != nil {
		return 0, err
	}
	changedSince := time.Time{}
	if !previousReadStarted.IsZero() {
		changedSince = previousReadStarted.Add(-t3CodeReadOverlap)
	}
	conversations, err := t3code.ReadT3CodeConversationsChangedSince(ctx, app.configuration.T3CodeUserDataFolderPath, changedSince)
	if err != nil {
		return 0, fmt.Errorf("read T3 Code: %w", err)
	}
	count, err := app.writeAgentConversations(ctx, req, conversations, "T3 Code threads")
	if err != nil {
		return count, err
	}
	return count, writeArchiveUpdateTime(ctx, database, updateTimeKindT3CodeStoreReadStarted, readStarted)
}

func (app *App) updateFromClaudeCode(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, t3CodeStoreIsPresent bool) (uint64, error) {
	transcriptFilePaths, err := claudecode.ClaudeCodeTranscriptFilePaths(app.configuration.ClaudeCodeProjectsFolderPath)
	if err != nil {
		return 0, err
	}
	changedFiles, err := sourceFilesChangedSinceLastRead(ctx, req.OpenedTrawlerArchiveStore.DB(), transcriptFilePaths)
	if err != nil {
		return 0, err
	}
	var addedOrChangedTurnCount uint64
	for fileIndex, changedFile := range changedFiles {
		req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: "Claude Code sessions", Done: int64(fileIndex), Total: int64(len(changedFiles))})
		conversation, entrypoint, err := claudecode.ReadClaudeCodeConversation(changedFile.path)
		if err != nil {
			return addedOrChangedTurnCount, err
		}
		sessionIsReadFromT3Code := t3CodeStoreIsPresent && claudecode.ClaudeCodeSessionWasDrivenThroughT3Code(entrypoint)
		if !sessionIsReadFromT3Code {
			count, err := app.writeAgentConversations(ctx, req, []agentconversation.AgentConversation{conversation}, "")
			addedOrChangedTurnCount += count
			if err != nil {
				return addedOrChangedTurnCount, err
			}
		}
		if err := recordSourceFileRead(ctx, req.OpenedTrawlerArchiveStore.DB(), changedFile); err != nil {
			return addedOrChangedTurnCount, err
		}
	}
	return addedOrChangedTurnCount, nil
}

func (app *App) updateFromCodex(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, t3CodeStoreIsPresent bool) (uint64, error) {
	codexStateDatabasePath := filepath.Join(app.configuration.CodexHomeFolderPath, "state_5.sqlite")
	if !fileExists(codexStateDatabasePath) {
		return 0, nil
	}
	threadIndex, err := codex.ReadCodexThreadIndex(ctx, codexStateDatabasePath)
	if err != nil {
		return 0, fmt.Errorf("read Codex thread index: %w", err)
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
		return 0, err
	}
	var addedOrChangedTurnCount uint64
	for fileIndex, changedFile := range changedFiles {
		req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: "Codex threads", Done: int64(fileIndex), Total: int64(len(changedFiles))})
		conversation, err := codex.ReadCodexConversation(threadIndexByRolloutFilePath[changedFile.path])
		if err != nil {
			return addedOrChangedTurnCount, err
		}
		count, err := app.writeAgentConversations(ctx, req, []agentconversation.AgentConversation{conversation}, "")
		addedOrChangedTurnCount += count
		if err != nil {
			return addedOrChangedTurnCount, err
		}
		if err := recordSourceFileRead(ctx, req.OpenedTrawlerArchiveStore.DB(), changedFile); err != nil {
			return addedOrChangedTurnCount, err
		}
	}
	return addedOrChangedTurnCount, nil
}

func (app *App) writeAgentConversations(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	conversations []agentconversation.AgentConversation,
	progressPhase string,
) (uint64, error) {
	var addedOrChangedTurnCount uint64
	for conversationIndex, conversation := range conversations {
		if progressPhase != "" {
			req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: progressPhase, Done: int64(conversationIndex), Total: int64(len(conversations))})
		}
		err := req.OpenedTrawlerArchiveStore.WithTx(ctx, func(transaction *sql.Tx) error {
			count, err := writeAgentConversation(ctx, transaction, conversation, app.configuration.MachineName)
			addedOrChangedTurnCount += count
			return err
		})
		if err != nil {
			return addedOrChangedTurnCount, fmt.Errorf("write %s: %w", conversation.CanonicalConversationReference, err)
		}
	}
	return addedOrChangedTurnCount, nil
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

func recordSourceFileRead(ctx context.Context, database *sql.DB, file sourceFileState) error {
	_, err := database.ExecContext(ctx, `INSERT INTO agent_source_files_already_read VALUES (?, ?, ?)
		ON CONFLICT(source_file_path) DO UPDATE SET byte_count = excluded.byte_count, modification_time = excluded.modification_time`,
		file.path, file.byteCount, archiveTimeText(file.modificationTime))
	return err
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
