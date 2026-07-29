package render

import (
	"io"
	"strings"

	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
)

func WritePersonListResponse(
	writer io.Writer,
	personListResponse *personv1.PersonListResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference map[string]string,
) error {
	if personListResponse == nil {
		return nil
	}
	allRows := make([][]string, 0, len(personListResponse.GetPersonRecordsInDisplayOrder()))
	showAlternativeNames := false
	showContributingTrawlers := false
	for _, personRecord := range personListResponse.GetPersonRecordsInDisplayOrder() {
		if personRecord == nil {
			continue
		}
		alternativeNames := strings.TrimSpace(strings.Join(
			personRecord.GetAlternativePersonDisplayNames(),
			", ",
		))
		contributingTrawlers := strings.TrimSpace(strings.Join(
			personRecord.GetPersonFactContributingTrawlerDisplayNames(),
			", ",
		))
		showAlternativeNames = showAlternativeNames || alternativeNames != ""
		showContributingTrawlers = showContributingTrawlers || contributingTrawlers != ""
		allRows = append(allRows, []string{
			strings.TrimSpace(globallyRoutableTrawlLinksByCanonicalRecordReference[strings.TrimSpace(
				personRecord.GetCanonicalPersonRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
			)]),
			strings.TrimSpace(personRecord.GetPersonDisplayName()),
			alternativeNames,
			contributingTrawlers,
		})
	}
	columns := []TableColumn{
		{Header: "person", Wrap: true},
	}
	if showAlternativeNames {
		columns = append(columns, TableColumn{Header: "known as", Wrap: true, MaximumWrappedLines: 2})
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
		if showContributingTrawlers {
			row = append(row, allRow[3])
		}
		row = append(row, allRow[0])
		rows = append(rows, row)
	}
	return WriteTable(writer, columns, rows)
}
