package cli

// The who resolution error surfaces: what a reader (or agent) sees
// when --who matched nobody, matched too many people, or some sources
// could not answer.

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

func renderWhoResolutionLine(w io.Writer, input string, candidate WhoCandidate, surfaces map[string]string) error {
	if normalisePersonName(input) == normalisePersonName(candidate.Who) {
		return nil
	}
	resolutionSentence := fmt.Sprintf(
		"%s → %s (%s)",
		input,
		candidate.Who,
		whoSources(registeredTrawlerManifestIdentities(candidate), surfaces),
	)
	for _, line := range render.Wrap(resolutionSentence, render.OutputWidth(w)) {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) writeAmbiguousWho(
	input string,
	resolution federatedWhoResolution,
	surfaces map[string]string,
) error {
	_, _ = fmt.Fprintf(r.stderr, "More than one person matched %q.\n", input)
	personMatchCandidatesWithHumanReadableDisplayNames := personMatchCandidatesWithHumanReadableDisplayNames(resolution.Candidates)
	if len(personMatchCandidatesWithHumanReadableDisplayNames) > 0 {
		_, _ = fmt.Fprintln(r.stderr, "\nPossible matches:")
		if err := render.WriteAmbiguousFederatedTrawlerPersonMatchCandidates(
			r.stderr,
			federatedPersonMatchCandidates(personMatchCandidatesWithHumanReadableDisplayNames, surfaces),
		); err != nil {
			return err
		}
	}
	return exitErr{code: 1}
}

func (r *Runtime) writeUnknownWho(input string, resolution federatedWhoResolution, surfaces map[string]string) error {
	_, _ = fmt.Fprintf(r.stderr, "No person matched %q.\n", input)
	if len(resolution.DidYouMean) > 0 {
		_, _ = fmt.Fprintln(r.stderr, "\nPossible matches:")
		if err := render.WriteFederatedTrawlerPersonMatchOperation(r.stderr, &federation.FederatedTrawlerPersonMatchOperation{
			PersonMatchCandidates: federatedPersonMatchCandidates(resolution.DidYouMean, surfaces),
		}); err != nil {
			return err
		}
	}
	return exitErr{code: 1}
}

func personMatchCandidatesWithHumanReadableDisplayNames(personMatchCandidates []WhoCandidate) []WhoCandidate {
	personMatchCandidatesWithHumanReadableDisplayNames := make([]WhoCandidate, 0, len(personMatchCandidates))
	for _, personMatchCandidate := range personMatchCandidates {
		humanReadableDisplayNames := humanReadablePersonDisplayNames(personMatchCandidate)
		if len(humanReadableDisplayNames) == 0 {
			continue
		}
		personMatchCandidate.Who = humanReadableDisplayNames[0]
		personMatchCandidate.AlternativeNames = humanReadableDisplayNames[1:]
		personMatchCandidatesWithHumanReadableDisplayNames = append(personMatchCandidatesWithHumanReadableDisplayNames, personMatchCandidate)
	}
	return personMatchCandidatesWithHumanReadableDisplayNames
}

func humanReadablePersonDisplayNames(personMatchCandidate WhoCandidate) []string {
	possiblePersonDisplayNames := append([]string{personMatchCandidate.Who}, personMatchCandidate.AlternativeNames...)
	exactPersonFilterIdentifiers := exactPersonFilterIdentifiersFromWhoCandidate(personMatchCandidate)
	humanReadableDisplayNames := make([]string, 0, len(possiblePersonDisplayNames))
	seenHumanReadablePersonDisplayNames := map[string]bool{}
	for _, possibleDisplayName := range possiblePersonDisplayNames {
		possibleDisplayName = strings.Join(strings.Fields(possibleDisplayName), " ")
		if possibleDisplayName == "" || containsWhoIdentifier(exactPersonFilterIdentifiers, possibleDisplayName) || whomatch.IsIdentifierLike(possibleDisplayName, exactPersonFilterIdentifiers) {
			continue
		}
		if strings.Contains(possibleDisplayName, "@") || strings.HasPrefix(possibleDisplayName, "+") || strings.ContainsAny(possibleDisplayName, ":/") {
			continue
		}
		foldedPersonDisplayName := strings.ToLower(possibleDisplayName)
		if seenHumanReadablePersonDisplayNames[foldedPersonDisplayName] {
			continue
		}
		seenHumanReadablePersonDisplayNames[foldedPersonDisplayName] = true
		humanReadableDisplayNames = append(humanReadableDisplayNames, possibleDisplayName)
	}
	return humanReadableDisplayNames
}

func quoteExampleArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\"'") {
		return strconv.Quote(value)
	}
	return value
}
