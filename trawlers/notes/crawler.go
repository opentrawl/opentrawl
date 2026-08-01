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
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultListLimit = 20

type Crawler struct {
	listLimit             int
	versionListLimit      int
	versionAtOrBeforeTime string
}

var (
	_ trawlkit.Trawler  = (*Crawler)(nil)
	_ trawlkit.Updater  = (*Crawler)(nil)
	_ trawlkit.Searcher = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{listLimit: defaultListLimit, versionListLimit: defaultListLimit}
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity(archive.AppID),
		RegisteredTrawlerCommandName: archive.AppID,
		RegisteredTrawlerDisplayName: archive.DisplayName,
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Apple Notes' local database, including notes, folders, attachments and recoverable versions.",
			LeavesMachine:   "Nothing. Updates and searches stay on your Mac.",
			NetworkRequests: "None. Updates use only local data.",
		},
	}
}

func (*Crawler) LoadTrawlerConfiguration(trawlkit.TrawlerConfigurationFilePath) error {
	return nil
}

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{
			SharedTrawlerOperation:    federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
			TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp,
		},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{
			TrawlerCommandName:                     "notes",
			TrawlerCommandShownInBareTrawlOverview: true,
			TrawlerCommandHelpDescription:          "List notes newest first, or list notes in one folder",
			TrawlerCommandPositionalArgumentNames:  []string{"[FOLDER]"},
			RegisterTrawlerCommandFlags:            c.listFlags,
			TrawlerCommandArchiveAccess:            trawlkit.TrawlerCommandArchiveAccessRequired,
			ExecuteTrawlerCommand:                  c.runList,
		},
		{
			TrawlerCommandName:                     "folders",
			TrawlerCommandShownInBareTrawlOverview: true,
			TrawlerCommandHelpDescription:          "List note folders",
			TrawlerCommandArchiveAccess:            trawlkit.TrawlerCommandArchiveAccessRequired,
			ExecuteTrawlerCommand:                  c.runFolders,
			BuildTrawlerSpecificCommandActions:     notesFolderListTrawlCommandActions,
		},
		{
			TrawlerCommandName:                     "versions",
			TrawlerCommandShownInBareTrawlOverview: true,
			TrawlerCommandHelpDescription:          "List recovered versions of one note",
			TrawlerCommandPositionalArgumentNames:  []string{"LINK"},
			RegisterTrawlerCommandFlags:            c.versionFlags,
			TrawlerCommandArchiveAccess:            trawlkit.TrawlerCommandArchiveAccessRequired,
			ExecuteTrawlerCommand:                  c.runVersions,
		},
	}
}

func (c *Crawler) listFlags(fs *flag.FlagSet) {
	c.listLimit = defaultListLimit
	fs.IntVar(&c.listLimit, "limit", defaultListLimit, "Maximum number of notes")
}

func (c *Crawler) versionFlags(fs *flag.FlagSet) {
	c.versionListLimit = defaultListLimit
	c.versionAtOrBeforeTime = ""
	fs.IntVar(&c.versionListLimit, "limit", defaultListLimit, "Maximum number of versions")
	fs.StringVar(&c.versionAtOrBeforeTime, "at", "", "Show the version at or before this time")
}

func (c *Crawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	trawlerArchiveStatus := &status.TrawlerArchiveStatus{}
	response := &status.TrawlerStatusResponse{TrawlerArchiveStatus: trawlerArchiveStatus}
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
	trawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{
		{ArchiveContentKindName: "notes", ArchiveContentKindDisplayName: "notes", ArchiveContentCount: uint64(archiveStatus.Notes)},
		{ArchiveContentKindName: "versions", ArchiveContentKindDisplayName: "versions", ArchiveContentCount: uint64(archiveStatus.Versions)},
	}
	if completedAt, err := time.Parse(time.RFC3339Nano, archiveStatus.LastUpdateAt); err == nil {
		trawlerArchiveStatus.LastSuccessfullyCompletedArchiveUpdateTime = timestamppb.New(completedAt)
	}
	trawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands = true
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
