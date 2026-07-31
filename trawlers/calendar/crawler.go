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
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
	statusv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status/v1"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Crawler struct {
	eventsLimit int
}

var (
	_ trawlkit.Trawler                = (*Crawler)(nil)
	_ trawlkit.Syncer                 = (*Crawler)(nil)
	_ trawlkit.Searcher               = (*Crawler)(nil)
	_ trawlkit.WhoMatcher             = (*Crawler)(nil)
	_ trawlkit.PeopleSnapshotProvider = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{eventsLimit: defaultEventListLimit}
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:                           trawlkit.NewRegisteredTrawlerIdentity(archive.AppID),
		RegisteredTrawlerCommandName:                "calendar",
		RegisteredTrawlerDisplayName:                archive.DisplayName,
		TrawlerCommandNamesShownInBareTrawlOverview: []string{"events", "calendars"},
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Apple Calendar's local database, including events, calendars and participants.",
			LeavesMachine:   "Nothing. Normal sync and search stay on your Mac.",
			NetworkRequests: "None. Normal sync is local.",
		},
	}
}

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{
			TrawlerCommandName:            "events",
			TrawlerCommandHelpDescription: "List upcoming events",
			RegisterTrawlerCommandFlags:   c.bindEventsFlags,
			TrawlerCommandArchiveAccess:   trawlkit.TrawlerCommandArchiveAccessRequired,
			ExecuteTrawlerCommand:         c.runEvents,
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
			TrawlerCommandName:            "calendars",
			TrawlerCommandHelpDescription: "List calendars with events",
			TrawlerCommandArchiveAccess:   trawlkit.TrawlerCommandArchiveAccessRequired,
			ExecuteTrawlerCommand:         c.calendars,
		},
	}
}

func (c *Crawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*statusv1.TrawlerStatusResponse, error) {
	status := &statusv1.TrawlerArchiveStatus{}
	response := &statusv1.TrawlerStatusResponse{TrawlerArchiveStatus: status}
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
	status.ArchiveContentCountsAfterLastSuccessfullyCompletedSync = []*statusv1.ArchiveContentCountAfterLastSuccessfullyCompletedSync{
		{ArchiveContentKindName: "events", ArchiveContentKindDisplayName: "events", ArchiveContentCount: uint64(archiveStatus.Events)},
		{ArchiveContentKindName: "calendars", ArchiveContentKindDisplayName: "calendars", ArchiveContentCount: uint64(archiveStatus.Calendars)},
	}
	if completedAt, err := time.Parse(time.RFC3339Nano, archiveStatus.LastSyncAt); err == nil {
		status.LastSuccessfullyCompletedArchiveSyncTime = timestamppb.New(completedAt)
	}
	status.TrawlerArchiveCanAnswerCurrentCommands = true
	return response, nil
}

func (c *Crawler) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*searchv1.TrawlerSearchResponse, error) {
	archiveStore, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	var resolvedWhoFilter *archive.WhoFilter
	if strings.TrimSpace(query.Who) != "" {
		matchedWhoCandidate, err := resolveArchiveWho(ctx, archiveStore, query.Who)
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
	searchMatches := make([]*searchv1.TrawlerSearchMatch, 0, len(archiveSearchResults))
	for _, archiveSearchResult := range archiveSearchResults {
		matchingRecordAssociatedTime, err := parseEventTime(archiveSearchResult.Time)
		if err != nil {
			return nil, err
		}
		matchingRecordAnchorIdentifier := "summary"
		if len(archiveSearchResult.Matches) > 0 {
			matchingRecordAnchorIdentifier = archiveSearchResult.Matches[0].Field
		}
		presentation := &searchv1.SearchMatchPresentation{
			MatchingRecordKindDisplayName:          "event",
			MatchingRecordDisplayName:              strings.Join(strings.Fields(archiveSearchResult.Title), " "),
			PeopleRelatedToMatchingRecord:          calendarSearchResultPeopleRelatedToMatchingRecord(archiveSearchResult),
			PhysicalPlaceNamesSpecificToBroadest:   calendarSearchResultPhysicalPlaceNamesSpecificToBroadest(archiveSearchResult.Location),
			DigitalContainerNamesNearestToBroadest: calendarSearchResultDigitalContainerNamesNearestToBroadest(archiveSearchResult.Calendar, archiveSearchResult.Account),
			SearchMatchTextFieldsInDisplayOrder:    calendarSearchMatchTextFieldsInDisplayOrder(archiveSearchResult.Matches),
		}
		if !matchingRecordAssociatedTime.IsZero() {
			if archiveSearchResult.AllDay {
				presentation.MatchingRecordAssociatedTime = &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
					ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_CalendarDate{
						CalendarDate: &presentationv1.CalendarDate{
							CalendarYear: int32(matchingRecordAssociatedTime.Year()), CalendarMonthNumber: int32(matchingRecordAssociatedTime.Month()), CalendarDayOfMonth: int32(matchingRecordAssociatedTime.Day()),
						},
					},
				}
			} else {
				presentation.MatchingRecordAssociatedTime = &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
					ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(matchingRecordAssociatedTime)},
				}
			}
		}
		searchMatches = append(searchMatches, &searchv1.TrawlerSearchMatch{
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(archiveSearchResult.Ref),
			RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(matchingRecordAnchorIdentifier),
			SearchMatchPresentation:  presentation,
		})
	}
	_ = req.TrawlerCommandLog.Info("search_complete", fmt.Sprintf("returned=%d total=%d", len(archiveSearchResults), totalSearchMatches))
	return &searchv1.TrawlerSearchResponse{
		TrawlerSearchMatchesInDisplayOrder: searchMatches,
		TotalSearchMatches:                 uint64(totalSearchMatches),
		MoreSearchMatchesExist:             query.Limit > 0 && int64(len(archiveSearchResults)) < totalSearchMatches,
	}, nil
}

func calendarSearchResultPeopleRelatedToMatchingRecord(archiveSearchResult archive.SearchResult) []*personv1.PersonRelatedToArchiveRecord {
	peopleRelatedToMatchingRecord := make(
		[]*personv1.PersonRelatedToArchiveRecord,
		0,
		len(archiveSearchResult.Attendees)+1,
	)
	normalizedPersonDisplayNamesAlreadyAdded := make(map[string]struct{}, len(archiveSearchResult.Attendees)+1)
	addPersonRelatedToMatchingRecord := func(
		personDisplayName string,
		personTechnicalIdentifiers []string,
		personRoleInMatchingRecord personv1.PersonRoleInArchiveRecord,
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
			&personv1.PersonRelatedToArchiveRecord{
				PersonDisplayName:         personDisplayName,
				PersonRoleInArchiveRecord: personRoleInMatchingRecord,
			},
		)
	}
	addPersonRelatedToMatchingRecord(
		archiveSearchResult.Organizer.DisplayName,
		[]string{archiveSearchResult.Organizer.Email, archiveSearchResult.Organizer.PhoneNumber},
		personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_ORGANIZER,
	)
	for _, attendee := range archiveSearchResult.Attendees {
		if attendee.Self {
			continue
		}
		addPersonRelatedToMatchingRecord(
			attendee.DisplayName,
			[]string{attendee.Email, attendee.PhoneNumber, attendee.Address},
			personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_ATTENDEE,
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

func calendarSearchMatchTextFieldsInDisplayOrder(matches []archive.SearchMatch) []*searchv1.SearchMatchTextField {
	searchMatchTextFields := make([]*searchv1.SearchMatchTextField, 0, len(matches))
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

func (c *Crawler) Who(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, person string) (*personv1.TrawlerPersonMatchResponse, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	candidates, err := st.ResolveWho(ctx, person)
	if err != nil {
		return nil, err
	}
	out := make([]*personv1.TrawlerPersonMatchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, calendarWhoCandidate(candidate, person))
	}
	return &personv1.TrawlerPersonMatchResponse{PersonMatchCandidates: out}, nil
}

func (c *Crawler) PeopleSnapshot(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*personv1.TrawlerPeopleSnapshot, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	contacts, err := st.ExportContacts(ctx)
	if err != nil {
		return nil, err
	}
	return &personv1.TrawlerPeopleSnapshot{TrawlerPersonIdentities: contacts}, nil
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
) *personv1.TrawlerPersonMatchCandidate {
	lastSeen, _ := parseEventTime(candidate.LastSeen)
	result := &personv1.TrawlerPersonMatchCandidate{
		PersonDisplayName: candidate.Who,
		PersonNameOrHumanReadableContactValueThatMatchedQuery: calendarPersonNameOrHumanReadableContactValueThatMatchedQuery(
			candidate,
			query,
		),
		PersonMatchFactsFromTrawlers: []*personv1.PersonMatchFactsFromTrawler{
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
