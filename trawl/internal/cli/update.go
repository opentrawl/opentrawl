package cli

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	federationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	updatecontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type UpdateCmd struct {
	Args []string `arg:"" optional:"" passthrough:"partial" name:"trawler" help:"Trawler names; one selected trawler can use its own flags"`
}

func (c *UpdateCmd) Run(r *Runtime) error {
	trawlerNames, trawlerArguments, err := splitUpdateArgs(c.Args)
	if err != nil {
		return err
	}
	trawlers, err := r.selectedTrawlerArguments(trawlerNames)
	if err != nil {
		return err
	}
	trawlers, err = trawlersForSharedOperation(trawlers,
		federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE, len(trawlerNames) > 0)
	if err != nil {
		return err
	}
	trawlers = canonicalUpdateTrawlers(trawlers)
	if len(trawlerArguments) > 0 && len(trawlers) != 1 {
		return render.TrawlerUpdateOptionsNeedOneTrawler()
	}
	if len(trawlers) == 0 {
		return render.NoTrawlersAreAvailable()
	}
	if updateHelpRequested(trawlerArguments) {
		return r.writeTrawlerUpdateHelp(trawlers[0])
	}
	trawlerArgumentsWithSelectedTrawlerLocalConversationShortReference, _, err := replaceGloballyRoutableConversationLinkWithLocalShortReferenceForSelectedTrawler(
		trawlerArguments,
		trawlers[0],
	)
	if err != nil {
		return err
	}

	operation, err := r.runUpdateBatch(
		trawlers,
		trawlerArgumentsWithSelectedTrawlerLocalConversationShortReference,
		discoverInstalledTrawlers(r.ctx),
		nil,
		nil,
	)
	if err != nil {
		return err
	}
	if err := userInputErrorFromFederatedTrawlerOperationFailures(operation.GetOperationFailures()); err != nil {
		return err
	}
	r.logFederationOutcomes(operation.GetOperationFailures(), operation.GetTrawlersSkippedFromOperation())
	if err := r.output.WriteArchiveUpdate(operation); err != nil {
		return err
	}
	return outcomeExit(operation.GetOutcome())
}

func (r *Runtime) updateTrawler(
	ctx context.Context,
	trawler InstalledTrawler,
	trawlerArguments []string,
) (*federationcontract.TrawlerArchiveUpdateResult, *federationcontract.TrawlerOperationFailure, *federationcontract.TrawlerSkippedFromOperation) {
	started := r.logTrawlerStart(trawler, "update")
	if trawler.TrawlerDiscoveryError != nil {
		r.logTrawlerDone(trawler, "update", started, trawler.TrawlerDiscoveryError)
		return nil, federation.FailureForError(
			trawler.RegisteredTrawlerManifest,
			federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
			trawler.TrawlerDiscoveryError,
		), nil
	}
	report, err := r.runTrawlerUpdateContext(ctx, trawler, trawlerArguments)
	if err != nil {
		r.logTrawlerDone(trawler, "update", started, err)
		failureError := err
		if isTimeoutError(err) {
			failureError = context.DeadlineExceeded
		}
		return nil, federation.FailureForError(
			trawler.RegisteredTrawlerManifest,
			federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
			failureError,
		), nil
	}
	r.logTrawlerDone(trawler, "update", started, nil)
	return &federationcontract.TrawlerArchiveUpdateResult{
		RegisteredTrawler:            trawler.RegisteredTrawlerManifest.GetRegisteredTrawler(),
		RegisteredTrawlerDisplayName: trawlerHumanName(trawler),
		TrawlerArchiveUpdateReport:   report,
	}, nil, nil
}

func (r *Runtime) runTrawlerUpdateContext(
	ctx context.Context,
	trawler InstalledTrawler,
	trawlerArguments []string,
) (*updatecontract.TrawlerArchiveUpdateReport, error) {
	return r.trawlerExecutor().Update(ctx, trawler.Trawler, trawlerArguments)
}

func updateHelpRequested(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "-h" || argument == "--help" {
			return true
		}
	}
	return false
}

func (r *Runtime) writeTrawlerUpdateHelp(trawler InstalledTrawler) error {
	updateHelp := render.CommandHelp{
		CommandWordsAfterTrawlInvocation: []string{"update", trawlerCommandToken(trawler)},
		CommandDescription:               "Update the " + trawlerHumanName(trawler) + " archive",
	}
	for _, command := range trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandDeclarations() {
		if command != nil &&
			command.GetSharedTrawlerOperation() == federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE {
			updateHelp.CommandFlagsInDisplayOrder = trawlerCommandFlagHelp(command)
		}
	}
	return r.output.WriteCommandHelp(updateHelp)
}

func splitUpdateArgs(arguments []string) ([]string, []string, error) {
	firstFlag := len(arguments)
	for index, argument := range arguments {
		if argument == "--" || strings.HasPrefix(argument, "-") {
			firstFlag = index
			break
		}
	}
	trawlerNames := append([]string(nil), arguments[:firstFlag]...)
	trawlerArguments := append([]string(nil), arguments[firstFlag:]...)
	if len(trawlerArguments) > 0 && trawlerArguments[0] == "--" {
		trawlerArguments = trawlerArguments[1:]
	}
	return trawlerNames, trawlerArguments, nil
}
