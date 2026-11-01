package agents

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/agents/internal/agentconversation"
	agentsproto "github.com/opentrawl/opentrawl/trawlers/agents/proto/trawl/agents"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/prototransport"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// One machine collects every machine's agent conversations into one archive
// and gives each record its link, so a link means the same record on every
// machine. Another machine reads its own stores, sends what changed to the
// collecting machine as an import file, and keeps a copy of the collecting
// machine's archive. Both are in the Agents archive folder under the
// default state root, ~/.opentrawl/agents.
const (
	importFilesFolderName                 = "imports"
	importFileExtension                   = ".binpb"
	archiveCopyForOtherMachinesFileName   = "archive-copy-for-other-machines.db"
	collectingMachineArchiveCopyFileName  = "archive-copy-from-collecting-machine.db"
	importFileBeingSentFileName           = "import-being-sent-to-collecting-machine" + importFileExtension
	collectingMachineAgentsArchiveFolder  = ".opentrawl/agents"
	defaultCollectingMachineTrawlLocation = "/Applications/OpenTrawl.app/Contents/Helpers/trawl"
)

func archiveFolderPath(req *trawlkit.TrawlerCommandExecutionRequest) string {
	return filepath.Dir(req.TrawlerArchivePaths.TrawlerArchivePath)
}

// updateThroughCollectingMachine sends this machine's changed conversations
// to the collecting machine, has it update, and copies its archive here.
func (app *App) updateThroughCollectingMachine(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) error {
	importFilePath := filepath.Join(archiveFolderPath(req), importFileBeingSentFileName)
	destination, err := newImportFile(importFilePath)
	if err != nil {
		return err
	}
	progress, readErr := app.readChangedAgentConversations(ctx, req, destination)
	if err := destination.close(); err != nil {
		return err
	}
	host := app.configuration.CollectingMachineSSHHost
	importFileName := safeFileNamePart(app.configuration.MachineName) + "-" + time.Now().UTC().Format("20060102T150405.000000000Z") + importFileExtension
	req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: "Sending to " + host})
	sendCommand := exec.CommandContext(ctx, "ssh", host, fmt.Sprintf(
		"mkdir -p %[1]s && cat > %[1]s/.%[2]s && mv %[1]s/.%[2]s %[1]s/%[2]s",
		collectingMachineAgentsArchiveFolder+"/"+importFilesFolderName, importFileName))
	importFile, err := os.Open(importFilePath)
	if err != nil {
		return err
	}
	sendCommand.Stdin = importFile
	sendOutput, sendErr := sendCommand.CombinedOutput()
	_ = importFile.Close()
	if sendErr != nil {
		return fmt.Errorf("could not reach the collecting machine %s: %w: %s", host, sendErr, strings.TrimSpace(string(sendOutput)))
	}
	if err := recordReadProgress(ctx, req.OpenedTrawlerArchiveStore.DB(), progress); err != nil {
		return err
	}
	if err := os.Remove(importFilePath); err != nil {
		return err
	}

	req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: "Updating on " + host})
	updateOutput, err := exec.CommandContext(ctx, "ssh", host,
		shellQuoted(app.configuration.CollectingMachineTrawlLocation)+" update agents").CombinedOutput()
	if err != nil {
		return fmt.Errorf("update on the collecting machine %s failed: %w: %s", host, err, strings.TrimSpace(string(updateOutput)))
	}

	req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: "Copying the archive from " + host})
	copyPath := filepath.Join(archiveFolderPath(req), collectingMachineArchiveCopyFileName)
	copyOutput, err := exec.CommandContext(ctx, "rsync", "--times", "--inplace",
		host+":"+collectingMachineAgentsArchiveFolder+"/"+archiveCopyForOtherMachinesFileName, copyPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy the archive from %s: %w: %s", host, err, strings.TrimSpace(string(copyOutput)))
	}
	if err := mirrorCollectingMachineArchiveCopy(ctx, req, copyPath); err != nil {
		return err
	}
	return readErr
}

// mirrorCollectingMachineArchiveCopy makes this machine's conversations,
// turns and links the same as the collecting machine's.
func mirrorCollectingMachineArchiveCopy(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, copyPath string) error {
	connection, err := req.OpenedTrawlerArchiveStore.DB().Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close() }()
	if _, err := connection.ExecContext(ctx, `ATTACH DATABASE ? AS collecting_machine_copy`, copyPath); err != nil {
		return err
	}
	defer func() { _ = detachCollectingMachineCopy(connection) }()
	transaction, err := connection.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	if err := shortref.EnsureSchema(ctx, transaction); err != nil {
		return err
	}
	for _, statement := range []string{
		`INSERT INTO agent_conversations SELECT * FROM collecting_machine_copy.agent_conversations WHERE true
			ON CONFLICT(conversation_reference) DO UPDATE SET
				agent_harness = excluded.agent_harness,
				agent_display_name = excluded.agent_display_name,
				conversation_title = excluded.conversation_title,
				working_folder_path = excluded.working_folder_path,
				link_that_opens_conversation = excluded.link_that_opens_conversation,
				machine_name = excluded.machine_name,
				most_recent_turn_time = excluded.most_recent_turn_time`,
		`INSERT INTO agent_conversation_turns SELECT * FROM collecting_machine_copy.agent_conversation_turns WHERE true
			ON CONFLICT(turn_reference) DO UPDATE SET
				conversation_reference = excluded.conversation_reference,
				turn_time = excluded.turn_time,
				turn_author = excluded.turn_author,
				turn_text = excluded.turn_text,
				attached_file_names = excluded.attached_file_names
			WHERE turn_text IS NOT excluded.turn_text OR turn_author IS NOT excluded.turn_author
				OR attached_file_names IS NOT excluded.attached_file_names
				OR conversation_reference IS NOT excluded.conversation_reference`,
		`INSERT OR IGNORE INTO short_refs SELECT alias, full_ref, canonical_ref FROM collecting_machine_copy.short_refs`,
	} {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("copy the collecting machine's archive: %w", err)
		}
	}
	return transaction.Commit()
}

func detachCollectingMachineCopy(connection *sql.Conn) error {
	_, err := connection.ExecContext(context.Background(), `DETACH DATABASE collecting_machine_copy`)
	return err
}

// applyImportFilesFromOtherMachines archives what other machines sent. An
// applied file is removed; the archive keeps its conversations.
func applyImportFilesFromOtherMachines(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, destination agentConversationDestination) (int, error) {
	importFolder := filepath.Join(archiveFolderPath(req), importFilesFolderName)
	entries, err := os.ReadDir(importFolder)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var importFileNames []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), importFileExtension) && !strings.HasPrefix(entry.Name(), ".") {
			importFileNames = append(importFileNames, entry.Name())
		}
	}
	// Names end with the time they were sent, so name order within one
	// machine is the order it read its stores.
	sort.Strings(importFileNames)
	applied := 0
	for _, importFileName := range importFileNames {
		req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: "Conversations from other machines", Done: int64(applied), Total: int64(len(importFileNames))})
		importFilePath := filepath.Join(importFolder, importFileName)
		if err := applyImportFile(ctx, importFilePath, destination); err != nil {
			return applied, fmt.Errorf("import %s: %w", importFileName, err)
		}
		if err := os.Remove(importFilePath); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

func applyImportFile(ctx context.Context, importFilePath string, destination agentConversationDestination) error {
	file, err := os.Open(importFilePath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(file)
	var conversation *agentconversation.AgentConversation
	deliver := func() error {
		if conversation == nil {
			return nil
		}
		return destination.deliverAgentConversation(ctx, *conversation)
	}
	for {
		frame := &agentsproto.AgentConversationImportFrame{}
		err := prototransport.ReadDelimited(reader, frame)
		if errors.Is(err, io.EOF) {
			return deliver()
		}
		if err != nil {
			return err
		}
		switch content := frame.GetAgentConversationImportFrameContent().(type) {
		case *agentsproto.AgentConversationImportFrame_ImportedAgentConversation:
			if err := deliver(); err != nil {
				return err
			}
			imported := content.ImportedAgentConversation
			conversation = &agentconversation.AgentConversation{
				CanonicalConversationReference:       imported.GetCanonicalConversationReference(),
				AgentHarness:                         agentconversation.AgentHarness(imported.GetAgentHarnessDisplayName()),
				AgentDisplayName:                     imported.GetAgentDisplayName(),
				ConversationTitle:                    imported.GetConversationTitle(),
				WorkingFolderPath:                    imported.GetWorkingFolderPath(),
				LinkThatOpensConversation:            imported.GetLinkThatOpensConversation(),
				MachineNameWhereConversationIsStored: imported.GetMachineNameWhereConversationIsStored(),
			}
		case *agentsproto.AgentConversationImportFrame_ImportedAgentConversationTurn:
			if conversation == nil {
				return errors.New("a turn comes before any conversation")
			}
			imported := content.ImportedAgentConversationTurn
			author, known := turnAuthorFromImport[imported.GetTurnAuthor()]
			if !known {
				return fmt.Errorf("turn %s has no author", imported.GetCanonicalTurnReference())
			}
			conversation.TurnsInConversationOrder = append(conversation.TurnsInConversationOrder, agentconversation.AgentConversationTurn{
				CanonicalTurnReference: imported.GetCanonicalTurnReference(),
				TurnTime:               imported.GetTurnTime().AsTime(),
				TurnAuthor:             author,
				TurnText:               imported.GetTurnText(),
				AttachedFileNames:      imported.GetAttachedFileNames(),
			})
		default:
			return errors.New("an import frame is empty")
		}
	}
}

var turnAuthorFromImport = map[agentsproto.AgentConversationTurnAuthor]agentconversation.AgentConversationTurnAuthor{
	agentsproto.AgentConversationTurnAuthor_AGENT_CONVERSATION_TURN_AUTHOR_PERSON_USING_THE_HARNESS: agentconversation.AgentConversationTurnAuthorPersonUsingTheHarness,
	agentsproto.AgentConversationTurnAuthor_AGENT_CONVERSATION_TURN_AUTHOR_AGENT_IN_CONVERSATION:    agentconversation.AgentConversationTurnAuthorAgentInConversation,
	agentsproto.AgentConversationTurnAuthor_AGENT_CONVERSATION_TURN_AUTHOR_ANOTHER_AGENT_OR_PROGRAM: agentconversation.AgentConversationTurnAuthorAnotherAgentOrProgram,
}

// importFile writes conversations as import frames, for the collecting machine.
type importFile struct {
	file   *os.File
	writer *bufio.Writer
}

func newImportFile(path string) (*importFile, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &importFile{file: file, writer: bufio.NewWriter(file)}, nil
}

func (destination *importFile) deliverAgentConversation(_ context.Context, conversation agentconversation.AgentConversation) error {
	if len(conversation.TurnsInConversationOrder) == 0 {
		return nil
	}
	if err := prototransport.WriteDelimited(destination.writer, &agentsproto.AgentConversationImportFrame{
		AgentConversationImportFrameContent: &agentsproto.AgentConversationImportFrame_ImportedAgentConversation{
			ImportedAgentConversation: &agentsproto.ImportedAgentConversation{
				CanonicalConversationReference:       conversation.CanonicalConversationReference,
				AgentHarnessDisplayName:              string(conversation.AgentHarness),
				AgentDisplayName:                     conversation.AgentDisplayName,
				ConversationTitle:                    conversation.ConversationTitle,
				WorkingFolderPath:                    conversation.WorkingFolderPath,
				LinkThatOpensConversation:            conversation.LinkThatOpensConversation,
				MachineNameWhereConversationIsStored: conversation.MachineNameWhereConversationIsStored,
			},
		},
	}); err != nil {
		return err
	}
	for _, turn := range conversation.TurnsInConversationOrder {
		var author agentsproto.AgentConversationTurnAuthor
		for importAuthor, turnAuthor := range turnAuthorFromImport {
			if turnAuthor == turn.TurnAuthor {
				author = importAuthor
			}
		}
		if err := prototransport.WriteDelimited(destination.writer, &agentsproto.AgentConversationImportFrame{
			AgentConversationImportFrameContent: &agentsproto.AgentConversationImportFrame_ImportedAgentConversationTurn{
				ImportedAgentConversationTurn: &agentsproto.ImportedAgentConversationTurn{
					CanonicalTurnReference: turn.CanonicalTurnReference,
					TurnTime:               timestamppb.New(turn.TurnTime),
					TurnAuthor:             author,
					TurnText:               turn.TurnText,
					AttachedFileNames:      turn.AttachedFileNames,
				},
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (destination *importFile) close() error {
	if err := destination.writer.Flush(); err != nil {
		_ = destination.file.Close()
		return err
	}
	return destination.file.Close()
}

// writeArchiveCopyForOtherMachines leaves a consistent single-file copy that
// another machine copies after asking for this update.
func writeArchiveCopyForOtherMachines(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) error {
	copyPath := filepath.Join(archiveFolderPath(req), archiveCopyForOtherMachinesFileName)
	partialCopyPath := filepath.Join(archiveFolderPath(req), "."+archiveCopyForOtherMachinesFileName)
	if err := os.Remove(partialCopyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := req.OpenedTrawlerArchiveStore.DB().ExecContext(ctx, `VACUUM INTO ?`, partialCopyPath); err != nil {
		return err
	}
	return os.Rename(partialCopyPath, copyPath)
}

var characterNotSafeInFileName = regexp.MustCompile(`[^A-Za-z0-9.-]+`)

func safeFileNamePart(text string) string {
	return strings.Trim(characterNotSafeInFileName.ReplaceAllString(text, "-"), "-.")
}

func shellQuoted(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}
