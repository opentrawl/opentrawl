package gogcrawl

import (
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

func searchHit(hit archive.SearchHit) (trawlkit.Hit, error) {
	t, err := parseContractTime(hit.Time)
	if err != nil {
		return trawlkit.Hit{}, err
	}
	unread := hit.Unread
	title := strings.TrimSpace(hit.Where)
	if title == "" {
		title = "(no subject)"
	}
	var anchorID string
	evidence := searchEvidence(hit.Matches)
	if len(hit.Matches) > 0 {
		anchorID = hit.Matches[0].Field
	} else {
		anchorID = "subject"
		value := title
		evidence = []trawlkit.EvidenceFragment{{Label: "Message preview", Field: &trawlkit.FieldEvidence{Name: "subject", Value: []trawlkit.TextRun{{Text: value}}}}}
	}
	archiveContext := trawlkit.ArchiveContext{Kind: "received", Label: "Received"}
	if strings.EqualFold(strings.TrimSpace(hit.Who), "me") {
		archiveContext = trawlkit.ArchiveContext{Kind: "sent", Label: "Sent"}
	}
	return trawlkit.Hit{
		Ref:      hit.Ref,
		ShortRef: hit.ShortRef,
		Time:     t,
		AnchorID: anchorID,
		Summary:  trawlkit.ResultSummary{Title: title, Subtitle: hit.Who},
		Archive:  []trawlkit.ArchiveContext{archiveContext},
		Evidence: evidence,
		Unread:   &unread,
	}, nil
}

func searchEvidence(matches []archive.SearchMatch) []trawlkit.EvidenceFragment {
	evidence := make([]trawlkit.EvidenceFragment, 0, len(matches))
	for _, match := range matches {
		label := "Message body"
		if match.Field == "subject" {
			label = "Subject"
		}
		runs := make([]trawlkit.TextRun, 0, len(match.Runs))
		for _, run := range match.Runs {
			runs = append(runs, trawlkit.TextRun{Text: run.Text, Matched: run.Matched})
		}
		evidence = append(evidence, trawlkit.EvidenceFragment{
			Label: label,
			Field: &trawlkit.FieldEvidence{Name: match.Field, Value: runs},
		})
	}
	return evidence
}

func whoCandidate(candidate archive.WhoCandidate) whomatch.Candidate {
	lastSeen, _ := parseContractTime(candidate.LastSeen)
	return whomatch.Candidate{
		Who:         candidate.Who,
		Identifiers: append([]string(nil), candidate.Identifiers...),
		LastSeen:    lastSeen,
		Messages:    candidate.Messages,
	}
}

func parseContractTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid archive time %q", value)
	}
	return t, nil
}
