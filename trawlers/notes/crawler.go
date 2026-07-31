package notes

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	statusv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultListLimit = 20

type Crawler struct {
	syncStorePath string
	syncLabel     string
	storeLabel    string
	listLimit     int
}

var (
	_ trawlkit.Trawler  = (*Crawler)(nil)
	_ trawlkit.Syncer   = (*Crawler)(nil)
	_ trawlkit.Searcher = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{listLimit: defaultListLimit}
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:                           trawlkit.NewRegisteredTrawlerIdentity(archive.AppID),
		RegisteredTrawlerCommandName:                archive.AppID,
		RegisteredTrawlerDisplayName:                archive.DisplayName,
		TrawlerCommandNamesShownInBareTrawlOverview: []string{"notes", "folders", "versions"},
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Apple Notes' local database, including notes, folders, attachments and recoverable versions.",
			LeavesMachine:   "Nothing. Normal sync and search stay on your Mac.",
			NetworkRequests: "None. Normal sync is local.",
		},
	}
}

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{
			SharedTrawlerOperation:      federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SYNC,
			RegisterTrawlerCommandFlags: c.syncFlags,
		},
		{
			TrawlerCommandName:                    "notes",
			TrawlerCommandHelpDescription:         "List notes newest first, or list notes in one folder",
			TrawlerCommandPositionalArgumentNames: []string{"[FOLDER]"},
			RegisterTrawlerCommandFlags:           c.listFlags,
			TrawlerCommandArchiveAccess:           trawlkit.TrawlerCommandArchiveAccessRequired,
			ExecuteTrawlerCommand:                 c.runList,
		},
		{
			TrawlerCommandName:                 "folders",
			TrawlerCommandHelpDescription:      "List note folders",
			TrawlerCommandArchiveAccess:        trawlkit.TrawlerCommandArchiveAccessRequired,
			ExecuteTrawlerCommand:              c.runFolders,
			BuildTrawlerSpecificCommandActions: notesFolderListTrawlCommandActions,
		},
		{
			TrawlerCommandName:                    "sync-store",
			TrawlerCommandHelpDescription:         "Sync one copied or mounted NoteStore.sqlite",
			TrawlerCommandPositionalArgumentNames: []string{"PATH"},
			RegisterTrawlerCommandFlags:           c.syncStoreFlags,
			TrawlerCommandChangesArchive:          true,
			TrawlerCommandHelpListing:             trawlkit.TrawlerCommandHiddenFromHumanHelp,
			ExecuteTrawlerCommand:                 c.runSyncStore,
		},
		{
			TrawlerCommandName:                    "versions",
			TrawlerCommandHelpDescription:         "List recovered versions of one note",
			TrawlerCommandPositionalArgumentNames: []string{"LINK"},
			TrawlerCommandArchiveAccess:           trawlkit.TrawlerCommandArchiveAccessRequired,
			ExecuteTrawlerCommand:                 c.runVersions,
		},
		{
			TrawlerCommandName:                    "at-time",
			TrawlerCommandHelpDescription:         "Show the recovered version at or before a time",
			TrawlerCommandPositionalArgumentNames: []string{"LINK", "TIME"},
			TrawlerCommandHelpListing:             trawlkit.TrawlerCommandListedOnlyUnderMoreTrawlerCommands,
			TrawlerCommandArchiveAccess:           trawlkit.TrawlerCommandArchiveAccessRequired,
			ExecuteTrawlerCommand:                 c.runAtTime,
		},
	}
}

func (c *Crawler) listFlags(fs *flag.FlagSet) {
	c.listLimit = defaultListLimit
	fs.IntVar(&c.listLimit, "limit", defaultListLimit, "Maximum number of notes")
}

func (c *Crawler) syncFlags(fs *flag.FlagSet) {
	c.syncStorePath = ""
	c.syncLabel = ""
	fs.StringVar(&c.syncStorePath, "store", "", "Path to a copied NoteStore.sqlite file")
	fs.StringVar(&c.syncLabel, "label", "", "Archive label")
}

func (c *Crawler) syncStoreFlags(fs *flag.FlagSet) {
	c.storeLabel = ""
	fs.StringVar(&c.storeLabel, "label", "", "archive label")
}

func (c *Crawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*statusv1.TrawlerStatusResponse, error) {
	status := &statusv1.TrawlerArchiveStatus{}
	response := &statusv1.TrawlerStatusResponse{TrawlerArchiveStatus: status}
	if req.OpenedTrawlerArchiveStore == nil {
		return response, nil
	}
	archiveStore, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return response, nil
	}
	archiveStatus, err := archiveStore.Status(ctx)
	if err != nil {
		return response, nil
	}
	status.ArchiveContentCountsAfterLastSuccessfullyCompletedSync = []*statusv1.ArchiveContentCountAfterLastSuccessfullyCompletedSync{
		{ArchiveContentKindName: "notes", ArchiveContentKindDisplayName: "notes", ArchiveContentCount: uint64(archiveStatus.Notes)},
		{ArchiveContentKindName: "versions", ArchiveContentKindDisplayName: "versions", ArchiveContentCount: uint64(archiveStatus.Versions)},
	}
	if completedAt, err := time.Parse(time.RFC3339Nano, archiveStatus.LastSyncAt); err == nil {
		status.LastSuccessfullyCompletedArchiveSyncTime = timestamppb.New(completedAt)
	}
	status.TrawlerArchiveCanAnswerCurrentCommands = true
	return response, nil
}

func commandErr(code, message string, err error) error {
	return crawlerError{code: code, message: message, err: err}
}

type crawlerError struct {
	code    string
	message string
	err     error
}

func (e crawlerError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return e.message
}

func (e crawlerError) Unwrap() error {
	return e.err
}

func (e crawlerError) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{Code: e.code, Message: e.message}
}

func usageError(message string) error {
	return output.UsageError{Err: fmt.Errorf("%s", message)}
}
