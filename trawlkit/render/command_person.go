package render

import (
	"io"
	"strings"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

func WritePersonListResponse(
	writer io.Writer,
	personListResponse *person.PersonListResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) error {
	if personListResponse == nil {
		return nil
	}
	if len(personListResponse.GetPersonRecordsInDisplayOrder()) == 0 {
		_, err := io.WriteString(writer, "No people match.\n")
		return err
	}
	allRows := make([][]string, 0, len(personListResponse.GetPersonRecordsInDisplayOrder()))
	showAlternativeNames := false
	showContributingTrawlers := false
	showMessageCounts := false
	for _, personRecord := range personListResponse.GetPersonRecordsInDisplayOrder() {
		if personRecord == nil {
			continue
		}
		alternativeNames := strings.TrimSpace(strings.Join(
			personRecord.GetAlternativePersonDisplayNames(),
			", ",
		))
		contributingTrawlers := personTrawlerNamesWithMessageCounts(
			personRecord.GetPersonFactContributingTrawlerDisplayNames(),
			personRecord.GetPersonMessageCountsFromTrawlerArchives(),
		)
		showAlternativeNames = showAlternativeNames || alternativeNames != ""
		showContributingTrawlers = showContributingTrawlers || contributingTrawlers != ""
		showMessageCounts = showMessageCounts ||
			personRecord.GetMessageCountInvolvingPersonAcrossTrawlers() > 0
		allRows = append(allRows, []string{
			globallyRoutableTrawlLinkText(
				globallyRoutableTrawlLinksByCanonicalRecordReference.
					globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
						personRecord.GetCanonicalRecordReference(),
					),
			),
			strings.TrimSpace(personRecord.GetPersonDisplayName()),
			alternativeNames,
			formatOptionalInteger(personRecord.GetMessageCountInvolvingPersonAcrossTrawlers()),
			contributingTrawlers,
		})
	}
	columns := []TableColumn{
		{Header: "person", Wrap: true},
	}
	if showAlternativeNames {
		columns = append(columns, TableColumn{Header: "known as", Wrap: true, MaximumWrappedLines: 2})
	}
	if showMessageCounts {
		columns = append(columns, TableColumn{Header: "messages", AlignRight: true})
	}
	if showContributingTrawlers {
		columns = append(columns, TableColumn{Header: "trawlers", Wrap: true, MaximumWrappedLines: 2})
	}
	columns = append(columns, TableColumn{Header: "link", NeverTruncateCellValues: true})
	rows := make([][]string, 0, len(allRows))
	for _, allRow := range allRows {
		row := []string{allRow[1]}
		if showAlternativeNames {
			row = append(row, allRow[2])
		}
		if showMessageCounts {
			row = append(row, allRow[3])
		}
		if showContributingTrawlers {
			row = append(row, allRow[4])
		}
		row = append(row, allRow[0])
		rows = append(rows, row)
	}
	return WriteTable(writer, columns, rows)
}

func formatOptionalInteger(value uint64) string {
	if value == 0 {
		return ""
	}
	return FormatInteger(int64(value))
}

func personTrawlerNamesWithMessageCounts(
	trawlerDisplayNames []string,
	messageCounts []*person.PersonMessageCountFromTrawlerArchive,
) string {
	messageCountByNormalizedTrawlerDisplayName := make(map[string]uint64, len(messageCounts))
	for _, messageCount := range messageCounts {
		if messageCount == nil {
			continue
		}
		trawlerDisplayName := strings.TrimSpace(messageCount.GetRegisteredTrawlerDisplayName())
		if trawlerDisplayName == "" {
			trawlerDisplayName = strings.TrimSpace(
				messageCount.GetRegisteredTrawler().GetRegisteredTrawlerIdentity(),
			)
		}
		if trawlerDisplayName != "" {
			messageCountByNormalizedTrawlerDisplayName[strings.ToLower(trawlerDisplayName)] +=
				messageCount.GetMessageCountInvolvingPersonInTrawlerArchive()
		}
	}
	values := make([]string, 0, len(trawlerDisplayNames))
	seenTrawlerDisplayNames := map[string]struct{}{}
	for _, trawlerDisplayName := range trawlerDisplayNames {
		trawlerDisplayName = strings.TrimSpace(trawlerDisplayName)
		normalizedTrawlerDisplayName := strings.ToLower(trawlerDisplayName)
		if trawlerDisplayName == "" {
			continue
		}
		if _, seen := seenTrawlerDisplayNames[normalizedTrawlerDisplayName]; seen {
			continue
		}
		seenTrawlerDisplayNames[normalizedTrawlerDisplayName] = struct{}{}
		if messageCount := messageCountByNormalizedTrawlerDisplayName[normalizedTrawlerDisplayName]; messageCount > 0 {
			values = append(values, trawlerDisplayName+" "+FormatInteger(int64(messageCount)))
		} else {
			values = append(values, trawlerDisplayName)
		}
	}
	return strings.Join(values, ", ")
}
