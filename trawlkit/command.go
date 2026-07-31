package trawlkit

import (
	"context"
	"flag"
	"time"

	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type TrawlerCommand struct {
	SharedTrawlerOperation                 federation.SharedTrawlerOperation
	TrawlerCommandName                     string
	TrawlerCommandHelpDescription          string
	TrawlerCommandPositionalArgumentNames  []string
	RegisterTrawlerCommandFlags            func(fs *flag.FlagSet)
	TrawlerCommandChangesArchive           bool
	TrawlerCommandHelpListing              TrawlerCommandHelpListing
	TrawlerCommandShownInBareTrawlOverview bool
	// Store declares archive access. TrawlerCommandArchiveAccessDefault keeps the runner default.
	TrawlerCommandArchiveAccess        TrawlerCommandArchiveAccess
	TrawlerCommandMaximumExecutionTime time.Duration
	ExecuteTrawlerCommand              func(ctx context.Context, req *TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error)
	BuildTrawlerSpecificCommandActions func(response *command.TrawlerCommandResponse) render.TrawlerSpecificCommandActions
}

type TrawlerCommandHelpListing int

const (
	TrawlerCommandListedInNormalTrawlerHelp TrawlerCommandHelpListing = iota
	TrawlerCommandListedOnlyUnderMoreTrawlerCommands
	TrawlerCommandHiddenFromHumanHelp
)

type TrawlerCommandArchiveAccess int

const (
	TrawlerCommandArchiveAccessDefault TrawlerCommandArchiveAccess = iota
	TrawlerCommandArchiveAccessNone
	// TrawlerCommandArchiveAccessOptional opens the archive read-only when it exists. It is only
	// valid on non-mutating commands.
	TrawlerCommandArchiveAccessOptional
	// TrawlerCommandArchiveAccessRequired opens a bespoke command's archive, read-only for
	// non-mutating commands and read-write for mutating commands.
	TrawlerCommandArchiveAccessRequired
)
