package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	federationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	synccontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/sync"
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
	trawlers = canonicalSyncTrawlers(trawlers)
	if len(trawlerArguments) > 0 && len(trawlers) != 1 {
		return usageErr{fmt.Errorf("trawler-specific update flags require exactly one trawler")}
	}
	if len(trawlers) == 0 {
		_, err := fmt.Fprintln(r.stdout, "No trawlers found.")
		return err
	}
	if updateHelpRequested(trawlerArguments) {
		return r.writeTrawlerUpdateHelp(trawlers[0], trawlerArguments)
	}
	trawlerArgumentsWithSelectedTrawlerLocalConversationShortReference, _, err := replaceGloballyRoutableConversationLinkWithLocalShortReferenceForSelectedTrawler(
		trawlerArguments,
		trawlers[0],
	)
	if err != nil {
		return err
	}

	operation, err := r.runSyncBatch(
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
	if err := render.WriteFederatedTrawlerArchiveSyncOperation(r.stdout, operation); err != nil {
		return err
	}
	return outcomeExit(operation.GetOutcome())
}

func (r *Runtime) syncTrawler(
	ctx context.Context,
	trawler InstalledTrawler,
	trawlerArguments []string,
) (*federationcontract.TrawlerArchiveSyncResult, *federationcontract.TrawlerOperationFailure, *federationcontract.TrawlerSkippedFromOperation) {
	started := r.logTrawlerStart(trawler, "sync")
	if trawler.TrawlerDiscoveryError != nil {
		r.logTrawlerDone(trawler, "sync", started, trawler.TrawlerDiscoveryError)
		return nil, federation.FailureForError(
			trawler.RegisteredTrawlerManifest,
			federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SYNC,
			trawler.TrawlerDiscoveryError,
		), nil
	}
	if !supportsSharedTrawlerOperation(trawler, federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SYNC) {
		r.logTrawlerDone(trawler, "sync", started, nil, "outcome=unsupported")
		return nil, nil, &federationcontract.TrawlerSkippedFromOperation{
			SkippedTrawler:               trawler.RegisteredTrawlerManifest.GetRegisteredTrawler(),
			RegisteredTrawlerDisplayName: trawlerHumanName(trawler),
			SkipReason:                   "This archive cannot be updated.",
		}
	}
	report, err := r.runTrawlerSyncContext(ctx, trawler, trawlerArguments)
	if err != nil {
		r.logTrawlerDone(trawler, "sync", started, err)
		failureError := err
		if isTimeoutError(err) {
			failureError = context.DeadlineExceeded
		}
		return nil, federation.FailureForError(
			trawler.RegisteredTrawlerManifest,
			federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SYNC,
			failureError,
		), nil
	}
	r.logTrawlerDone(trawler, "sync", started, nil)
	return &federationcontract.TrawlerArchiveSyncResult{
		RegisteredTrawler:            trawler.RegisteredTrawlerManifest.GetRegisteredTrawler(),
		RegisteredTrawlerDisplayName: trawlerHumanName(trawler),
		TrawlerArchiveSyncReport:     report,
	}, nil, nil
}

func (r *Runtime) runTrawlerSyncContext(
	ctx context.Context,
	trawler InstalledTrawler,
	trawlerArguments []string,
) (*synccontract.TrawlerArchiveSyncReport, error) {
	return r.trawlerExecutor().Sync(ctx, trawler.Trawler, trawlerArguments)
}

func updateHelpRequested(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "-h" || argument == "--help" {
			return true
		}
	}
	return false
}

func (r *Runtime) writeTrawlerUpdateHelp(trawler InstalledTrawler, trawlerArguments []string) error {
	_ = trawlerArguments
	flags := trawlerUpdateFlags(trawler)
	usage := render.TrawlInvocationDisplay(r.stdout) + " update " + trawlerCommandToken(trawler)
	if len(flags) > 0 {
		usage += " [flags]"
	}
	if _, err := fmt.Fprintln(
		r.stdout,
		strings.Join(render.WrapWithIndent("Usage: ", usage, render.OutputWidth(r.stdout), "  "), "\n"),
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(
		r.stdout,
		"\n"+wrapTextForOutputWidth("Get new items from the app", render.OutputWidth(r.stdout)),
	); err != nil {
		return err
	}
	if len(flags) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(r.stdout, "\nFlags:"); err != nil {
		return err
	}
	flagRows := make([][2]string, 0, len(flags))
	for _, updateFlag := range flags {
		argument := " VALUE"
		if updateFlag.isBoolean {
			argument = ""
		}
		flagRows = append(flagRows, [2]string{"--" + updateFlag.name + argument, updateFlag.help})
	}
	for _, flagRow := range formatRowsForOutputWidth(flagRows, 2, render.OutputWidth(r.stdout)) {
		if _, err := fmt.Fprintln(r.stdout, flagRow); err != nil {
			return err
		}
	}
	return nil
}

func trawlerUpdateFlags(trawler InstalledTrawler) []namespaceCommandFlag {
	for _, command := range trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandDeclarations() {
		if command != nil &&
			command.GetSharedTrawlerOperation() ==
				federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SYNC {
			return namespaceCommandFlags(command)
		}
	}
	return nil
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
