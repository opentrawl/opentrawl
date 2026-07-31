package trawlkit

import (
	"context"
	"flag"
	"time"

	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type TrawlerCommand struct {
	SharedTrawlerOperation                federationv1.SharedTrawlerOperation
	TrawlerCommandName                    string
	TrawlerCommandHelpDescription         string
	TrawlerCommandPositionalArgumentNames []string
	RegisterTrawlerCommandFlags           func(fs *flag.FlagSet)
	TrawlerCommandChangesArchive          bool
	TrawlerCommandHelpListing             TrawlerCommandHelpListing
	// Store declares archive access. TrawlerCommandArchiveAccessDefault keeps the runner default.
	TrawlerCommandArchiveAccess        TrawlerCommandArchiveAccess
	TrawlerCommandMaximumExecutionTime time.Duration
	ExecuteTrawlerCommand              func(ctx context.Context, req *TrawlerCommandExecutionRequest) (*commandv1.TrawlerCommandResponse, error)
	BuildTrawlerSpecificCommandActions func(response *commandv1.TrawlerCommandResponse) render.TrawlerSpecificCommandActions
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
