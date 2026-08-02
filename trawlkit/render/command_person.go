package render

import (
	"io"
	"sort"
	"strings"

	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

const (
	personListMinimumWidthForAlternativeNames = 160
	personListMinimumWidthForPerAppCounts     = 200
)

type personListRowFacts struct {
	personDisplayName             string
	alternativePersonDisplayNames string
	totalMessageCount             string
	apps                          []personAppFacts
	globallyRoutableTrawlLink     string
}

type personAppFacts struct {
	registeredTrawler *identity.RegisteredTrawlerIdentity
	appDisplayName    string
	messageCount      uint64
}

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
	rows := make([]personListRowFacts, 0, len(personListResponse.GetPersonRecordsInDisplayOrder()))
	for _, personRecord := range personListResponse.GetPersonRecordsInDisplayOrder() {
		if personRecord == nil {
			continue
		}
		rows = append(rows, personListRowFacts{
			personDisplayName: strings.TrimSpace(personRecord.GetPersonDisplayName()),
			alternativePersonDisplayNames: strings.TrimSpace(strings.Join(
				personRecord.GetAlternativePersonDisplayNames(),
				", ",
			)),
			totalMessageCount: formatOptionalInteger(
				personRecord.GetMessageCountInvolvingPersonAcrossTrawlers(),
			),
			apps: personAppsFromContributingTrawlersAndMessageCounts(
				personRecord.GetTrawlersContributingFactsToPersonRecord(),
				personRecord.GetPersonMessageCountsFromTrawlerArchives(),
			),
			globallyRoutableTrawlLink: globallyRoutableTrawlLinkText(
				globallyRoutableTrawlLinksByCanonicalRecordReference.
					globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
						personRecord.GetCanonicalRecordReference(),
					),
			),
		})
	}
	columns, tableRows := personListColumnsAndRowsForOutputWidth(
		rows,
		"known as",
		readableTableOutputWidth(writer),
	)
	return writeHumanRecordRowsWithPrimaryContentColumn(writer, columns, tableRows, 0)
}

func personListColumnsAndRowsForOutputWidth(
	rows []personListRowFacts,
	secondaryPersonIdentityColumnHeader string,
	outputWidth int,
) ([]TableColumn, [][]string) {
	showAlternativeNames := outputWidth >= personListMinimumWidthForAlternativeNames &&
		personListRowsHaveAlternativeNames(rows)
	perAppMessageCountColumns := []personAppFacts(nil)
	if outputWidth >= personListMinimumWidthForPerAppCounts {
		for _, app := range personAppsInActivityOrder(rows) {
			if app.messageCount == 0 {
				continue
			}
			candidatePerAppMessageCountColumns := append(
				append([]personAppFacts(nil), perAppMessageCountColumns...),
				app,
			)
			candidateColumns, candidateRows := personListColumnsAndRows(
				rows,
				secondaryPersonIdentityColumnHeader,
				showAlternativeNames,
				candidatePerAppMessageCountColumns,
			)
			if renderColumnsWidth(tableRenderColumns(candidateColumns, candidateRows, outputWidth)) > outputWidth {
				break
			}
			perAppMessageCountColumns = candidatePerAppMessageCountColumns
		}
	}
	columns, tableRows := personListColumnsAndRows(
		rows,
		secondaryPersonIdentityColumnHeader,
		showAlternativeNames,
		perAppMessageCountColumns,
	)
	if showAlternativeNames &&
		renderColumnsWidth(tableRenderColumns(columns, tableRows, outputWidth)) > outputWidth {
		columns, tableRows = personListColumnsAndRows(
			rows,
			secondaryPersonIdentityColumnHeader,
			false,
			perAppMessageCountColumns,
		)
	}
	return columns, tableRows
}

func personListColumnsAndRows(
	rows []personListRowFacts,
	secondaryPersonIdentityColumnHeader string,
	showAlternativeNames bool,
	perAppMessageCountColumns []personAppFacts,
) ([]TableColumn, [][]string) {
	columns := []TableColumn{{Header: "person", Wrap: true, MaximumWrappedLines: 2}}
	if showAlternativeNames {
		columns = append(columns, TableColumn{
			Header:              strings.TrimSpace(secondaryPersonIdentityColumnHeader),
			Wrap:                true,
			MaximumWrappedLines: 2,
		})
	}
	columns = append(columns, TableColumn{Header: "messages", AlignRight: true})
	for _, app := range perAppMessageCountColumns {
		columns = append(columns, TableColumn{
			Header:     app.appDisplayName + " messages",
			AlignRight: true,
		})
	}
	columns = append(columns,
		TableColumn{Header: "apps", Wrap: true, MaximumWrappedLines: 2},
		TableColumn{Header: "link", NeverTruncateCellValues: true},
	)
	tableRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		tableRow := []string{row.personDisplayName}
		if showAlternativeNames {
			tableRow = append(tableRow, row.alternativePersonDisplayNames)
		}
		tableRow = append(tableRow, row.totalMessageCount)
		for _, app := range perAppMessageCountColumns {
			tableRow = append(tableRow, formatOptionalInteger(
				personMessageCountForApp(row.apps, app.registeredTrawler),
			))
		}
		tableRow = append(
			tableRow,
			strings.Join(personAppDisplayNamesInAlphabeticalOrder(row.apps), ", "),
			row.globallyRoutableTrawlLink,
		)
		tableRows = append(tableRows, tableRow)
	}
	return columns, tableRows
}

func personListRowsHaveAlternativeNames(rows []personListRowFacts) bool {
	for _, row := range rows {
		if row.alternativePersonDisplayNames != "" {
			return true
		}
	}
	return false
}

func personAppDisplayNamesInAlphabeticalOrder(apps []personAppFacts) []string {
	apps = append([]personAppFacts(nil), apps...)
	sort.SliceStable(apps, func(left, right int) bool {
		if !strings.EqualFold(apps[left].appDisplayName, apps[right].appDisplayName) {
			return strings.ToLower(apps[left].appDisplayName) < strings.ToLower(apps[right].appDisplayName)
		}
		return registeredTrawlerIdentityText(apps[left].registeredTrawler) <
			registeredTrawlerIdentityText(apps[right].registeredTrawler)
	})
	appDisplayNames := make([]string, 0, len(apps))
	for _, app := range apps {
		appDisplayNames = append(appDisplayNames, app.appDisplayName)
	}
	return appDisplayNames
}

func personAppsFromContributingTrawlersAndMessageCounts(
	contributingTrawlers []*person.TrawlerContributingFactsToPersonRecord,
	messageCounts []*person.PersonMessageCountFromTrawlerArchive,
) []personAppFacts {
	apps := make([]personAppFacts, 0, len(contributingTrawlers))
	for _, contributingTrawler := range contributingTrawlers {
		if contributingTrawler == nil {
			continue
		}
		apps = mergePersonAppFactsByRegisteredTrawlerIdentity(apps, personAppFacts{
			registeredTrawler: contributingTrawler.GetRegisteredTrawler(),
			appDisplayName: strings.TrimSpace(
				contributingTrawler.GetRegisteredTrawlerDisplayName(),
			),
		})
	}
	for _, messageCount := range messageCounts {
		if messageCount == nil {
			continue
		}
		apps = mergePersonAppFactsByRegisteredTrawlerIdentity(apps, personAppFacts{
			registeredTrawler: messageCount.GetRegisteredTrawler(),
			appDisplayName:    strings.TrimSpace(messageCount.GetRegisteredTrawlerDisplayName()),
			messageCount:      messageCount.GetMessageCountInvolvingPersonInTrawlerArchive(),
		})
	}
	return apps
}

func personMessageCountForApp(
	apps []personAppFacts,
	registeredTrawler *identity.RegisteredTrawlerIdentity,
) uint64 {
	registeredTrawlerIdentity := registeredTrawlerIdentityText(registeredTrawler)
	for _, app := range apps {
		if registeredTrawlerIdentityText(app.registeredTrawler) == registeredTrawlerIdentity {
			return app.messageCount
		}
	}
	return 0
}

func personAppsInActivityOrder(rows []personListRowFacts) []personAppFacts {
	apps := []personAppFacts(nil)
	for _, row := range rows {
		for _, app := range row.apps {
			apps = mergePersonAppFactsByRegisteredTrawlerIdentity(apps, app)
		}
	}
	sort.SliceStable(apps, func(left, right int) bool {
		if apps[left].messageCount != apps[right].messageCount {
			return apps[left].messageCount > apps[right].messageCount
		}
		if !strings.EqualFold(apps[left].appDisplayName, apps[right].appDisplayName) {
			return strings.ToLower(apps[left].appDisplayName) < strings.ToLower(apps[right].appDisplayName)
		}
		return registeredTrawlerIdentityText(apps[left].registeredTrawler) <
			registeredTrawlerIdentityText(apps[right].registeredTrawler)
	})
	return apps
}

func mergePersonAppFactsByRegisteredTrawlerIdentity(
	apps []personAppFacts,
	app personAppFacts,
) []personAppFacts {
	registeredTrawlerIdentity := registeredTrawlerIdentityText(app.registeredTrawler)
	if registeredTrawlerIdentity == "" {
		return apps
	}
	app.appDisplayName = strings.TrimSpace(app.appDisplayName)
	if app.appDisplayName == "" {
		app.appDisplayName = registeredTrawlerIdentity
	}
	for index := range apps {
		if registeredTrawlerIdentityText(apps[index].registeredTrawler) != registeredTrawlerIdentity {
			continue
		}
		apps[index].messageCount += app.messageCount
		if apps[index].appDisplayName == "" ||
			apps[index].appDisplayName == registeredTrawlerIdentity {
			apps[index].appDisplayName = app.appDisplayName
		}
		return apps
	}
	return append(apps, app)
}

func registeredTrawlerIdentityText(
	registeredTrawler *identity.RegisteredTrawlerIdentity,
) string {
	return strings.TrimSpace(registeredTrawler.GetRegisteredTrawlerIdentity())
}

func formatOptionalInteger(value uint64) string {
	if value == 0 {
		return ""
	}
	return FormatInteger(int64(value))
}
