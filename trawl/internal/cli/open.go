package cli

import (
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type OpenCmd struct {
	Link string `arg:"" help:"Link from search or a list"`
}

func (c *OpenCmd) Run(r *Runtime) error {
	route, err := trawlkit.ParseGloballyRoutableTrawlLink(c.Link)
	if err != nil {
		return r.renderOpenResponse(openFailureForRequestedLink(
			c.Link,
			federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT,
			"The link is not valid.",
		))
	}
	installedTrawlers := discoverInstalledTrawlers(r.ctx)
	openTrawlers := r.federationOpenTrawlers(installedTrawlers)
	return r.renderOpenResponse(r.canonicalOpen(
		openTrawlers,
		route.RegisteredTrawlerManifestIdentity,
		route.LocalShortReferenceAcceptedByRegisteredTrawler,
		c.Link,
	))
}

func (r *Runtime) renderOpenResponse(response *openv1.OpenResponse) error {
	if response.GetFailure() != nil {
		failure := response.GetFailure()
		r.logInfo("open_failed", "error="+logQuote(failure.GetFailureMessage()))
		if failure.GetFailureCode() == federationv1.FailureCode_FAILURE_CODE_NOT_FOUND {
			_, _ = fmt.Fprintln(r.stderr, "No result has that link.")
			return exitErr{code: 1}
		}
		if failure.GetFailureCode() == federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT {
			if err := render.WriteOpenResponse(r.stderr, response); err != nil {
				return err
			}
			return exitErr{code: 1}
		}
		name := firstNonEmpty(
			strings.TrimSpace(failure.GetRegisteredTrawlerDisplayName()),
			strings.TrimSpace(failure.GetRegisteredTrawlerManifestIdentity()),
			"OpenTrawl",
		)
		if failure.GetFailureCode() == federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE {
			r.writeTrawlerArchiveUnavailableError(name)
		} else {
			_, _ = fmt.Fprintf(r.stderr, "The command did not complete for %s.\n", name)
		}
		return exitErr{code: 1}
	}
	if response.GetRecord() == nil {
		return fmt.Errorf("open response has no record")
	}
	if err := render.WriteOpenResponse(r.stdout, response); err != nil {
		return err
	}
	return outcomeExit(response.GetOutcome())
}

func openFailureForRequestedLink(requestedGloballyRoutableTrawlLink string, code federationv1.FailureCode, message string) *openv1.OpenResponse {
	return &openv1.OpenResponse{RequestedGloballyRoutableTrawlLink: requestedGloballyRoutableTrawlLink, Outcome: federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED, Failure: &federationv1.TrawlerOperationFailure{FailureCode: code, FailureMessage: message}}
}
