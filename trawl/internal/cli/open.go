package cli

import (
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type OpenCmd struct {
	Link string `arg:"" help:"Link from search or a list"`
}

func (c *OpenCmd) Run(r *Runtime) error {
	requestedTrawlLink := trawlkit.NewGloballyRoutableTrawlLink(c.Link)
	route, err := trawlkit.ParseGloballyRoutableTrawlLink(requestedTrawlLink)
	if err != nil {
		return usageErr{humanFacingUsageErrorMessage("The link is not valid.")}
	}
	installedTrawlers := discoverInstalledTrawlers(r.ctx)
	openTrawlers := r.federationOpenTrawlers(installedTrawlers)
	return r.renderOpenResponse(r.canonicalOpen(
		openTrawlers,
		route.RegisteredTrawler,
		route.LocalShortReference,
		requestedTrawlLink,
	))
}

func (r *Runtime) renderOpenResponse(response *open.OpenResponse) error {
	if response.GetFailure() != nil {
		failure := response.GetFailure()
		r.logInfo("open_failed", "error="+logQuote(failure.GetFailureMessage()))
		if failure.GetFailureCode() == federation.FailureCode_FAILURE_CODE_NOT_FOUND {
			_, _ = fmt.Fprintln(r.stderr, "No result has that link.")
			return exitErr{code: 1}
		}
		if failure.GetFailureCode() == federation.FailureCode_FAILURE_CODE_INVALID_INPUT {
			if err := render.WriteOpenResponse(r.stderr, response, render.OpenResponseRenderContext{}); err != nil {
				return err
			}
			return exitErr{code: 1}
		}
		name := firstNonEmpty(
			strings.TrimSpace(failure.GetRegisteredTrawlerDisplayName()),
			trawlkit.RegisteredTrawlerIdentityText(failure.GetFailedTrawler()),
			"OpenTrawl",
		)
		if failureMeansArchiveUnavailable(failure.GetFailureCode()) {
			r.writeTrawlerArchiveUnavailableError(name)
		} else {
			_, _ = fmt.Fprintf(r.stderr, "The command did not complete for %s.\n", name)
		}
		return exitErr{code: 1}
	}
	if response.GetRecord() == nil {
		return fmt.Errorf("open response has no record")
	}
	renderContext, err := r.openResponseRenderContext(response)
	if err != nil {
		return err
	}
	if err := render.WriteOpenResponse(r.stdout, response, renderContext); err != nil {
		return err
	}
	return outcomeExit(response.GetOutcome())
}

func (r *Runtime) openResponseRenderContext(
	response *open.OpenResponse,
) (render.OpenResponseRenderContext, error) {
	openedRecord := response.GetRecord()
	if openedRecord == nil || openedRecord.GetTrawlerSpecificOpenedRecordPresentation() == nil {
		return render.OpenResponseRenderContext{}, nil
	}
	registeredTrawlerIdentity := trawlkit.RegisteredTrawlerIdentityText(openedRecord.GetRecordTrawler())
	installedTrawler, found := findInstalledTrawler(discoverInstalledTrawlers(r.ctx), registeredTrawlerIdentity)
	if !found || installedTrawler.Trawler == nil {
		return render.OpenResponseRenderContext{}, nil
	}
	actionBuilder, providesActions := installedTrawler.Trawler.(trawlkit.TrawlerSpecificOpenedRecordPresentationActionBuilder)
	if !providesActions {
		return render.OpenResponseRenderContext{}, nil
	}
	actions, err := actionBuilder.BuildTrawlerSpecificOpenedRecordPresentationActions(openedRecord)
	if err != nil {
		return render.OpenResponseRenderContext{}, err
	}
	return render.OpenResponseRenderContext{
		TrawlerSpecificOpenedRecordPresentationActions: actions,
	}, nil
}

func openFailureForRequestedLink(
	requestedGloballyRoutableTrawlLink *trawlkit.GloballyRoutableTrawlLink,
	code federation.FailureCode,
	message string,
) *open.OpenResponse {
	return &open.OpenResponse{
		RequestedTrawlLink: requestedGloballyRoutableTrawlLink,
		Outcome:            federation.OperationOutcome_OPERATION_OUTCOME_FAILED,
		Failure: &federation.TrawlerOperationFailure{
			FailureCode:    code,
			FailureMessage: message,
		},
	}
}
