package notes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*searchv1.TrawlerSearchResponse, error) {
	archiveStore, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	archiveSearchResults, totalSearchMatches, err := archiveStore.Search(ctx, query.Text, archive.SearchOptions{
		Limit:  query.Limit,
		After:  query.After,
		Before: query.Before,
	})
	if err != nil {
		return nil, err
	}
	trawlerSearchMatches := make([]*searchv1.TrawlerSearchMatch, 0, len(archiveSearchResults))
	for _, archiveSearchResult := range archiveSearchResults {
		matchingRecordAnchorIdentifier := trawlkit.MatchAnchorID
		if len(archiveSearchResult.Matches) > 0 {
			matchingRecordAnchorIdentifier = archiveSearchResult.Matches[0].Field
		}
		name := strings.TrimSpace(archiveSearchResult.Title)
		searchMatchPresentation := &searchv1.SearchMatchPresentation{
			MatchingRecordKindDisplayName:          "note",
			MatchingRecordDisplayName:              name,
			DigitalContainerNamesNearestToBroadest: noteSearchResultDigitalContainerNamesNearestToBroadest(archiveSearchResult),
			SearchMatchTextFieldsInDisplayOrder:    noteSearchMatchTextFields(name, archiveSearchResult.Matches),
		}
		if associatedExactTime := parseNotesArchiveTimeForPresentation(
			archiveSearchResult.Time,
		); !associatedExactTime.IsZero() {
			searchMatchPresentation.MatchingRecordAssociatedTime = &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(associatedExactTime)},
			}
		}
		trawlerSearchMatches = append(trawlerSearchMatches, &searchv1.TrawlerSearchMatch{
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(archiveSearchResult.Ref),
			RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(matchingRecordAnchorIdentifier),
			SearchMatchPresentation:  searchMatchPresentation,
		})
	}
	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info("search_complete", fmt.Sprintf("returned=%d total=%d", len(archiveSearchResults), totalSearchMatches))
	}
	return &searchv1.TrawlerSearchResponse{
		TrawlerSearchMatchesInDisplayOrder: trawlerSearchMatches,
		TotalSearchMatches:                 uint64(totalSearchMatches),
		MoreSearchMatchesExist:             query.Limit > 0 && len(archiveSearchResults) < int(totalSearchMatches),
	}, nil
}

func noteSearchMatchTextFields(title string, matches []archive.SearchMatch) []*searchv1.SearchMatchTextField {
	searchMatchTextFields := make([]*searchv1.SearchMatchTextField, 0, len(matches))
	for _, match := range matches {
		textRuns := match.Runs
		searchMatchTextFieldName := ""
		switch match.Field {
		case "title":
			searchMatchTextFieldName = "Title"
		case "body":
			searchMatchTextFieldName = "Note"
			textRuns = noteBodySearchRunsWithoutSeparatelyDisplayedTitle(title, textRuns)
		default:
			continue
		}
		if !searchTextRunsContainMarkedQueryMatch(textRuns) {
			continue
		}
		searchMatchTextField := trawlkit.NewSearchMatchTextFieldFromFTS5TextRuns(
			searchMatchTextFieldName,
			textRuns,
		)
		if searchMatchTextField != nil {
			searchMatchTextFields = append(searchMatchTextFields, searchMatchTextField)
		}
	}
	return searchMatchTextFields
}

func noteBodySearchRunsWithoutSeparatelyDisplayedTitle(title string, textRuns []store.FTS5TextRun) []store.FTS5TextRun {
	var displayedText strings.Builder
	for _, textRun := range textRuns {
		displayedText.WriteString(textRun.Text)
	}
	bodyWithTitle := displayedText.String()
	bodyWithoutTitle := noteBodyWithoutSeparatelyDisplayedTitle(title, bodyWithTitle)
	removedByteCount := len(bodyWithTitle) - len(bodyWithoutTitle)
	if removedByteCount <= 0 {
		return textRuns
	}
	trimmedRuns := make([]store.FTS5TextRun, 0, len(textRuns))
	for _, textRun := range textRuns {
		if removedByteCount >= len(textRun.Text) {
			removedByteCount -= len(textRun.Text)
			continue
		}
		if removedByteCount > 0 {
			textRun.Text = textRun.Text[removedByteCount:]
			removedByteCount = 0
		}
		if textRun.Text != "" {
			trimmedRuns = append(trimmedRuns, textRun)
		}
	}
	return trimmedRuns
}

func searchTextRunsContainMarkedQueryMatch(textRuns []store.FTS5TextRun) bool {
	for _, textRun := range textRuns {
		if textRun.Matched && strings.TrimSpace(textRun.Text) != "" {
			return true
		}
	}
	return false
}

func noteSearchResultDigitalContainerNamesNearestToBroadest(archiveSearchResult archive.SearchResult) []string {
	folderName := strings.TrimSpace(archiveSearchResult.Folder)
	if folderName == "" || strings.EqualFold(folderName, "Notes") {
		return []string{"Notes"}
	}
	return []string{folderName, "Notes"}
}

func parseNotesArchiveTimeForPresentation(storedTime string) time.Time {
	storedTime = strings.TrimSpace(storedTime)
	if storedTime == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsedTime, err := time.Parse(layout, storedTime); err == nil {
			return parsedTime
		}
	}
	return time.Time{}
}

func archiveErr(err error) error {
	return commandErr("archive_unreadable", "Notes archive could not be read", err)
}
