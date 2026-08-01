package calendar

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/opentrawl/opentrawl/calendar/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Crawler struct {
	eventsLimit int
}

var (
	_ trawlkit.Trawler                = (*Crawler)(nil)
	_ trawlkit.Updater                = (*Crawler)(nil)
	_ trawlkit.Searcher               = (*Crawler)(nil)
	_ trawlkit.WhoMatcher             = (*Crawler)(nil)
	_ trawlkit.PeopleSnapshotProvider = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{eventsLimit: defaultEventListLimit}
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity(archive.AppID),
		RegisteredTrawlerCommandName: "calendar",
		RegisteredTrawlerDisplayName: archive.DisplayName,
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Apple Calendar's local database, including events, calendars and participants.",
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
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{
			TrawlerCommandName:                     "events",
			TrawlerCommandShownInBareTrawlOverview: true,
			TrawlerCommandHelpDescription:          "List all upcoming events or those in one calendar and one account",
			TrawlerCommandPositionalArgumentNames:  []string{"[CALENDAR]", "[ACCOUNT]"},
			RegisterTrawlerCommandFlags:            c.bindEventsFlags,
			TrawlerCommandArchiveAccess:            trawlkit.TrawlerCommandArchiveAccessRequired,
			ExecuteTrawlerCommand:                  c.runEvents,
		},
		{
			TrawlerCommandName:                    "calendars annotate",
			TrawlerCommandHelpDescription:         "Record the user's stated meaning for a calendar. This writes to the local archive.",
			TrawlerCommandPositionalArgumentNames: []string{"CALENDAR_ID", "MEANING"},
			TrawlerCommandChangesArchive:          true,
			TrawlerCommandHelpListing:             trawlkit.TrawlerCommandHiddenFromHumanHelp,
			TrawlerCommandArchiveAccess:           trawlkit.TrawlerCommandArchiveAccessRequired,
			ExecuteTrawlerCommand:                 c.annotateCalendar,
		},
		{
			TrawlerCommandName:                     "calendars",
			TrawlerCommandShownInBareTrawlOverview: true,
			TrawlerCommandHelpDescription:          "List calendars with events",
			TrawlerCommandArchiveAccess:            trawlkit.TrawlerCommandArchiveAccessRequired,
			ExecuteTrawlerCommand:                  c.calendars,
			BuildTrawlerSpecificCommandActions:     calendarListTrawlCommandActions,
		},
	}
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
		{ArchiveContentKindName: "events", ArchiveContentKindDisplayName: "events", ArchiveContentCount: uint64(archiveStatus.Events)},
		{ArchiveContentKindName: "calendars", ArchiveContentKindDisplayName: "calendars", ArchiveContentCount: uint64(archiveStatus.Calendars)},
	}
	if completedAt, err := time.Parse(time.RFC3339Nano, archiveStatus.LastUpdateAt); err == nil {
		trawlerArchiveStatus.LastSuccessfullyCompletedArchiveUpdateTime = timestamppb.New(completedAt)
	}
	trawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands = true
	return response, nil
}

func (c *Crawler) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*search.TrawlerSearchResponse, error) {
	archiveStore, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	var resolvedWhoFilter *archive.WhoFilter
	if resolvedPersonFilter := query.PersonFilter.ResolvedPersonFilter(); resolvedPersonFilter != nil {
		resolvedWhoFilter = &archive.WhoFilter{
			ExactPersonFilterIdentifiers: resolvedPersonFilter.ExactPersonFilterIdentifiers,
		}
	} else if unresolvedPersonFilterText := query.PersonFilter.UnresolvedPersonFilterText(); unresolvedPersonFilterText != "" {
		matchedWhoCandidate, err := resolveArchiveWho(ctx, archiveStore, unresolvedPersonFilterText)
		if err != nil {
			return nil, err
		}
		resolvedWhoFilter = matchedWhoCandidate.Filter()
	}
	archiveSearchResults, totalSearchMatches, err := archiveStore.Search(ctx, query.Text, archive.SearchOptions{
		Limit:  query.Limit,
		After:  unixOrZero(query.After),
		Before: unixOrZero(query.Before),
		Who:    resolvedWhoFilter,
	})
	if err != nil {
		return nil, err
	}
	searchMatches := make([]*search.TrawlerSearchMatch, 0, len(archiveSearchResults))
	for _, archiveSearchResult := range archiveSearchResults {
		matchingRecordAssociatedTime, err := parseEventTime(archiveSearchResult.Time)
		if err != nil {
			return nil, err
		}
		matchingRecordAnchorIdentifier := "summary"
		if len(archiveSearchResult.Matches) > 0 {
			matchingRecordAnchorIdentifier = archiveSearchResult.Matches[0].Field
		}
		searchMatchPresentation := &search.SearchMatchPresentation{
			MatchingRecordKindDisplayName:          "event",
			MatchingRecordDisplayName:              strings.Join(strings.Fields(archiveSearchResult.Title), " "),
			PeopleRelatedToMatchingRecord:          calendarSearchResultPeopleRelatedToMatchingRecord(archiveSearchResult),
			PhysicalPlaceNamesSpecificToBroadest:   calendarSearchResultPhysicalPlaceNamesSpecificToBroadest(archiveSearchResult.Location),
			DigitalContainerNamesNearestToBroadest: calendarSearchResultDigitalContainerNamesNearestToBroadest(archiveSearchResult.Calendar, archiveSearchResult.Account),
			SearchMatchTextFieldsInDisplayOrder:    calendarSearchMatchTextFieldsInDisplayOrder(archiveSearchResult.Matches),
		}
		if !matchingRecordAssociatedTime.IsZero() {
			if archiveSearchResult.AllDay {
				searchMatchPresentation.MatchingRecordAssociatedTime = &presentation.ArchiveRecordAssociatedTimeForDisplay{
					ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate{
						CalendarDate: &presentation.CalendarDate{
							CalendarYear: int32(matchingRecordAssociatedTime.Year()), CalendarMonthNumber: int32(matchingRecordAssociatedTime.Month()), CalendarDayOfMonth: int32(matchingRecordAssociatedTime.Day()),
						},
					},
				}
			} else {
				searchMatchPresentation.MatchingRecordAssociatedTime = &presentation.ArchiveRecordAssociatedTimeForDisplay{
					ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(matchingRecordAssociatedTime)},
				}
			}
		}
		searchMatches = append(searchMatches, &search.TrawlerSearchMatch{
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(archiveSearchResult.Ref),
			RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(matchingRecordAnchorIdentifier),
			SearchMatchPresentation:  searchMatchPresentation,
		})
	}
	_ = req.TrawlerCommandLog.Info("search_complete", fmt.Sprintf("returned=%d total=%d", len(archiveSearchResults), totalSearchMatches))
	return &search.TrawlerSearchResponse{
		TrawlerSearchMatchesInDisplayOrder: searchMatches,
		TotalSearchMatches:                 uint64(totalSearchMatches),
		MoreSearchMatchesExist:             query.Limit > 0 && int64(len(archiveSearchResults)) < totalSearchMatches,
	}, nil
}

func calendarSearchResultPeopleRelatedToMatchingRecord(archiveSearchResult archive.SearchResult) []*person.PersonRelatedToArchiveRecord {
	peopleRelatedToMatchingRecord := make(
		[]*person.PersonRelatedToArchiveRecord,
		0,
		len(archiveSearchResult.Attendees)+1,
	)
	normalizedPersonDisplayNamesAlreadyAdded := make(map[string]struct{}, len(archiveSearchResult.Attendees)+1)
	addPersonRelatedToMatchingRecord := func(
		personDisplayName string,
		personTechnicalIdentifiers []string,
		personRoleInMatchingRecord person.PersonRoleInArchiveRecord,
	) {
		personDisplayName = calendarSearchResultSafeHumanPersonDisplayName(personDisplayName, personTechnicalIdentifiers...)
		if personDisplayName == "" {
			return
		}
		normalizedPersonDisplayName := strings.ToLower(personDisplayName)
		if _, alreadyAdded := normalizedPersonDisplayNamesAlreadyAdded[normalizedPersonDisplayName]; alreadyAdded {
			return
		}
		normalizedPersonDisplayNamesAlreadyAdded[normalizedPersonDisplayName] = struct{}{}
		peopleRelatedToMatchingRecord = append(
			peopleRelatedToMatchingRecord,
			&person.PersonRelatedToArchiveRecord{
				PersonDisplayName:         personDisplayName,
				PersonRoleInArchiveRecord: personRoleInMatchingRecord,
			},
		)
	}
	addPersonRelatedToMatchingRecord(
		archiveSearchResult.Organizer.DisplayName,
		[]string{archiveSearchResult.Organizer.Email, archiveSearchResult.Organizer.PhoneNumber},
		person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_ORGANIZER,
	)
	for _, attendee := range archiveSearchResult.Attendees {
		if attendee.Self {
			continue
		}
		addPersonRelatedToMatchingRecord(
			attendee.DisplayName,
			[]string{attendee.Email, attendee.PhoneNumber, attendee.Address},
			person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_ATTENDEE,
		)
	}
	return peopleRelatedToMatchingRecord
}

func calendarSearchResultSafeHumanPersonDisplayName(personDisplayName string, personTechnicalIdentifiers ...string) string {
	personDisplayName = strings.Join(strings.Fields(personDisplayName), " ")
	if personDisplayName == "" {
		return ""
	}
	normalizedPersonDisplayName := strings.ToLower(personDisplayName)
	for _, personTechnicalIdentifier := range personTechnicalIdentifiers {
		if normalizedPersonDisplayName == strings.ToLower(strings.TrimSpace(personTechnicalIdentifier)) {
			return ""
		}
	}
	if strings.Contains(personDisplayName, "@") {
		return ""
	}
	personDisplayNameContainsOnlyPhoneCharacters := true
	personDisplayNameDigitCount := 0
	for _, character := range personDisplayName {
		switch {
		case unicode.IsDigit(character):
			personDisplayNameDigitCount++
		case unicode.IsSpace(character), strings.ContainsRune("+()-.", character):
		default:
			personDisplayNameContainsOnlyPhoneCharacters = false
		}
	}
	if personDisplayNameContainsOnlyPhoneCharacters && personDisplayNameDigitCount >= 3 {
		return ""
	}
	return personDisplayName
}

func calendarSearchResultPhysicalPlaceNamesSpecificToBroadest(location *archive.Location) []string {
	if location == nil {
		return nil
	}
	physicalPlaceNamesSpecificToBroadest := make([]string, 0, 2)
	normalizedPhysicalPlaceNamesAlreadyAdded := make(map[string]struct{}, 2)
	for _, physicalPlaceName := range []string{location.Title, location.Address} {
		physicalPlaceName = strings.Join(strings.Fields(physicalPlaceName), " ")
		normalizedPhysicalPlaceName := strings.ToLower(physicalPlaceName)
		if physicalPlaceName == "" {
			continue
		}
		if _, alreadyAdded := normalizedPhysicalPlaceNamesAlreadyAdded[normalizedPhysicalPlaceName]; alreadyAdded {
			continue
		}
		normalizedPhysicalPlaceNamesAlreadyAdded[normalizedPhysicalPlaceName] = struct{}{}
		physicalPlaceNamesSpecificToBroadest = append(physicalPlaceNamesSpecificToBroadest, physicalPlaceName)
	}
	return physicalPlaceNamesSpecificToBroadest
}

func calendarSearchResultDigitalContainerNamesNearestToBroadest(calendarDisplayTitle, accountDisplayName string) []string {
	digitalContainerNamesNearestToBroadest := make([]string, 0, 2)
	normalizedDigitalContainerNamesAlreadyAdded := make(map[string]struct{}, 2)
	for _, digitalContainerName := range []string{calendarDisplayTitle, accountDisplayName} {
		digitalContainerName = strings.Join(strings.Fields(digitalContainerName), " ")
		normalizedDigitalContainerName := strings.ToLower(digitalContainerName)
		if digitalContainerName == "" {
			continue
		}
		if _, alreadyAdded := normalizedDigitalContainerNamesAlreadyAdded[normalizedDigitalContainerName]; alreadyAdded {
			continue
		}
		normalizedDigitalContainerNamesAlreadyAdded[normalizedDigitalContainerName] = struct{}{}
		digitalContainerNamesNearestToBroadest = append(digitalContainerNamesNearestToBroadest, digitalContainerName)
	}
	return digitalContainerNamesNearestToBroadest
}

func calendarSearchMatchTextFieldsInDisplayOrder(matches []archive.SearchMatch) []*search.SearchMatchTextField {
	searchMatchTextFields := make([]*search.SearchMatchTextField, 0, len(matches))
	for _, match := range matches {
		searchMatchTextFieldName := ""
		switch match.Field {
		case "summary":
			searchMatchTextFieldName = "Title"
		case "description":
			searchMatchTextFieldName = "Description"
		default:
			continue
		}
		searchMatchTextField := trawlkit.NewSearchMatchTextFieldFromFTS5TextRuns(
			searchMatchTextFieldName,
			match.Runs,
		)
		if searchMatchTextField != nil {
			searchMatchTextFields = append(searchMatchTextFields, searchMatchTextField)
		}
	}
	return searchMatchTextFields
}

func (c *Crawler) Who(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, personQuery string) (*person.TrawlerPersonMatchResponse, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	candidates, err := st.ResolveWho(ctx, personQuery)
	if err != nil {
		return nil, err
	}
	out := make([]*person.TrawlerPersonMatchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, calendarWhoCandidate(candidate, personQuery))
	}
	return &person.TrawlerPersonMatchResponse{PersonMatchCandidates: out}, nil
}

func (c *Crawler) PeopleSnapshot(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*person.TrawlerPeopleSnapshot, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	contacts, err := st.ExportContacts(ctx)
	if err != nil {
		return nil, err
	}
	return &person.TrawlerPeopleSnapshot{TrawlerPersonIdentities: contacts}, nil
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func calendarWhoCandidate(
	candidate archive.WhoCandidate,
	query string,
) *person.TrawlerPersonMatchCandidate {
	lastSeen, _ := parseEventTime(candidate.LastSeen)
	result := &person.TrawlerPersonMatchCandidate{
		PersonDisplayName: candidate.Who,
		PersonNameOrHumanReadableContactValueThatMatchedQuery: calendarPersonNameOrHumanReadableContactValueThatMatchedQuery(
			candidate,
			query,
		),
		PersonMatchFactsFromTrawlers: []*person.PersonMatchFactsFromTrawler{
			trawlkit.NewPersonMatchFactsFromTrawler(
				trawlkit.NewRegisteredTrawlerIdentity(archive.AppID),
				candidate.Identifiers,
				candidate.Who,
			),
		},
	}
	if !lastSeen.IsZero() {
		result.LatestMatchingArchiveRecordTime = timestamppb.New(lastSeen)
	}
	return result
}

func calendarPersonNameOrHumanReadableContactValueThatMatchedQuery(
	candidate archive.WhoCandidate,
	query string,
) string {
	humanReadableNamesAndContactValues := []string{candidate.Who}
	for _, identifier := range candidate.Identifiers {
		if calendarHumanReadableContactValue(identifier) {
			humanReadableNamesAndContactValues = append(
				humanReadableNamesAndContactValues,
				identifier,
			)
		}
	}
	bestMatchRank := whomatch.Rank(0)
	bestHumanReadableMatch := ""
	for _, humanReadableNameOrContactValue := range humanReadableNamesAndContactValues {
		matchRank, matches := whomatch.MatchRank(
			query,
			[]string{humanReadableNameOrContactValue},
		)
		if matches && matchRank.BetterThan(bestMatchRank) {
			bestMatchRank = matchRank
			bestHumanReadableMatch = strings.TrimSpace(humanReadableNameOrContactValue)
		}
	}
	return bestHumanReadableMatch
}

func calendarHumanReadableContactValue(value string) bool {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "@") {
		return true
	}
	digitCount := 0
	for _, character := range value {
		switch {
		case unicode.IsDigit(character):
			digitCount++
		case unicode.IsSpace(character), strings.ContainsRune("+()-.", character):
		default:
			return false
		}
	}
	return digitCount >= 3
}

func resolveArchiveWho(ctx context.Context, st *archive.Store, who string) (archive.WhoCandidate, error) {
	candidates, err := st.ResolveWho(ctx, who)
	if err != nil {
		return archive.WhoCandidate{}, err
	}
	resolved := make([]archive.WhoCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ResolvesWho(who) {
			resolved = append(resolved, candidate)
		}
	}
	switch len(resolved) {
	case 0:
		return archive.WhoCandidate{}, unknownWhoError(who)
	case 1:
		return resolved[0], nil
	default:
		return archive.WhoCandidate{}, ambiguousWhoError(who, resolved)
	}
}

func ambiguousWhoError(who string, candidates []archive.WhoCandidate) error {
	return commandError{
		code:    4,
		name:    "ambiguous_who",
		message: "--who matched more than one person",
		err:     fmt.Errorf("ambiguous --who %q", who),
	}
}

func unknownWhoError(who string) error {
	return commandError{
		code:    5,
		name:    "unknown_who",
		message: "--who did not match a person",
		err:     fmt.Errorf("unknown --who %q", who),
	}
}

func parseEventTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid event time %q", value)
}
