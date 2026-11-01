// Package agents archives conversations held with AI agents in Claude Code,
// Codex and T3 Code. One reader per harness turns its store into
// conversations and turns; this trawler keeps them and serves them through
// OpenTrawl's shared conversation and message records.
package agents

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AgentsTrawlerConfiguration is read from the trawler's config.toml. Every
// value has a default for the usual install on this machine.
type AgentsTrawlerConfiguration struct {
	// MachineName is recorded with each conversation, so an archive that
	// holds several machines' conversations says where each one lives.
	MachineName                  string `toml:"machine_name,omitempty"`
	ClaudeCodeProjectsFolderPath string `toml:"claude_code_projects_folder,omitempty"`
	CodexHomeFolderPath          string `toml:"codex_home_folder,omitempty"`
	T3CodeUserDataFolderPath     string `toml:"t3_code_user_data_folder,omitempty"`
}

type App struct {
	configuration      AgentsTrawlerConfiguration
	messageListOptions agentMessageListOptions
}

var (
	_ trawlkit.Trawler                                    = (*App)(nil)
	_ trawlkit.Updater                                    = (*App)(nil)
	_ trawlkit.Searcher                                   = (*App)(nil)
	_ trawlkit.ConversationLister                         = (*App)(nil)
	_ trawlkit.TrawlerMessageLister                       = (*App)(nil)
	_ trawlkit.RecordOpener                               = (*App)(nil)
	_ trawlkit.SuccessfullyCompletedArchiveUpdateRecorder = (*App)(nil)
)

func New() *App { return &App{} }

func (*App) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity("agents"),
		RegisteredTrawlerCommandName: "agents",
		RegisteredTrawlerDisplayName: "Agents",
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Conversations stored on this Mac by Claude Code, Codex and T3 Code.",
			LeavesMachine:   "Nothing. Updates and searches stay on your Mac.",
			NetworkRequests: "None. Updates use only local files.",
		},
	}
}

func (app *App) LoadTrawlerConfiguration(trawlerConfigurationFilePath trawlkit.TrawlerConfigurationFilePath) error {
	homeFolderPath, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	machineName, _ := os.Hostname()
	configuration := AgentsTrawlerConfiguration{
		MachineName:                  strings.TrimSuffix(machineName, ".local"),
		ClaudeCodeProjectsFolderPath: filepath.Join(homeFolderPath, ".claude", "projects"),
		CodexHomeFolderPath:          filepath.Join(homeFolderPath, ".codex"),
		T3CodeUserDataFolderPath:     filepath.Join(homeFolderPath, ".t3", "userdata"),
	}
	if err := config.LoadTOMLFileIfPresent(string(trawlerConfigurationFilePath), &configuration); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	app.configuration = configuration
	return nil
}

func (app *App) TrawlerCommands() []trawlkit.TrawlerCommand {
	commands := make([]trawlkit.TrawlerCommand, 0, 6)
	for _, operation := range []federation.SharedTrawlerOperation{
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN,
	} {
		commands = append(commands, trawlkit.TrawlerCommand{
			SharedTrawlerOperation:           operation,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand,
		})
	}
	return append(commands,
		trawlkit.TrawlerCommand{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES,
			RegisterTrawlerCommandFlags:      app.registerMessageListFlags,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
		},
		trawlkit.TrawlerCommand{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
		},
	)
}

func (app *App) registerMessageListFlags(flags *flag.FlagSet) {
	app.messageListOptions = agentMessageListOptions{}
	flags.BoolVar(&app.messageListOptions.onlyMessagesYouSent, "from-me", false, "Show only messages you sent to agents, including text you pasted")
	flags.StringVar(&app.messageListOptions.matchingWords, "matching", "", "Show only messages containing these `WORDS`, at the matching passage")
	flags.StringVar(&app.messageListOptions.after, "after", "", render.OnOrAfterDateOptionDescription)
	flags.StringVar(&app.messageListOptions.before, "before", "", render.OnOrBeforeDateOptionDescription)
}

func (*App) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	response := &status.TrawlerStatusResponse{TrawlerArchiveStatus: &status.TrawlerArchiveStatus{}}
	if req.OpenedTrawlerArchiveStore == nil {
		return response, nil
	}
	database := req.OpenedTrawlerArchiveStore.DB()
	var conversationCount, turnCount uint64
	err := database.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM agent_conversations), (SELECT COUNT(*) FROM agent_conversation_turns)`).
		Scan(&conversationCount, &turnCount)
	if err != nil {
		// The archive exists but has never been updated.
		return response, nil
	}
	completed, err := readArchiveUpdateTime(ctx, database, updateTimeKindLastSuccessfullyCompletedArchiveUpdate)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if !completed.IsZero() {
		response.TrawlerArchiveStatus.LastSuccessfullyCompletedArchiveUpdateTime = timestamppb.New(completed)
	}
	response.TrawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands = true
	response.TrawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{
		{ArchiveContentKindName: "conversations", ArchiveContentKindDisplayName: "conversations", ArchiveContentCount: conversationCount},
		{ArchiveContentKindName: "messages", ArchiveContentKindDisplayName: "messages", ArchiveContentCount: turnCount},
	}
	return response, nil
}
