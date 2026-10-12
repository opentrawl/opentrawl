package photosarchive

import (
	"context"
	"fmt"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	"strings"
)

func (photosArchive *PhotosArchive) ReadPresentPhotoCanonicalRecordReferences(ctx context.Context) ([]*identity.CanonicalArchiveRecordReference, error) {
	rows, err := photosArchive.database.QueryContext(ctx, `select apple_photo_uuid from apple_library_photo where absent_from_apple_library_since_time_utc is null order by apple_photo_uuid`)
	if err != nil {
		return nil, fmt.Errorf("read present photo links: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var references []*identity.CanonicalArchiveRecordReference
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return nil, fmt.Errorf("read present photo link: %w", err)
		}
		references = append(references, &identity.CanonicalArchiveRecordReference{CanonicalArchiveRecordReference: "photos:" + uuid})
	}
	return references, rows.Err()
}

func (photosArchive *PhotosArchive) ReadTripsForPhotoFacts(ctx context.Context, facts []*photo.AppleLibraryPhotoFacts) ([]*photo.AppleLibraryTrip, error) {
	selectedUUIDs := map[string]bool{}
	for _, fact := range facts {
		for _, tripUUID := range fact.GetAppleTripUuids() {
			selectedUUIDs[tripUUID] = true
		}
	}
	if len(selectedUUIDs) == 0 {
		return nil, nil
	}
	trips, err := readAppleLibraryTrips(ctx, photosArchive.database)
	if err != nil {
		return nil, err
	}
	selectedTrips := make([]*photo.AppleLibraryTrip, 0, len(selectedUUIDs))
	for _, fact := range facts {
		for _, tripUUID := range fact.GetAppleTripUuids() {
			if selectedUUIDs[tripUUID] && trips[tripUUID] != nil {
				selectedTrips = append(selectedTrips, trips[tripUUID])
				selectedUUIDs[tripUUID] = false
			}
		}
	}
	return selectedTrips, nil
}

func containsPhotoFieldIgnoringCase(field, wanted string) bool {
	if wanted == "" {
		return true
	}
	fieldRunes, wantedRunes := []rune(field), []rune(wanted)
	for start := 0; start+len(wantedRunes) <= len(fieldRunes); start++ {
		if strings.EqualFold(string(fieldRunes[start:start+len(wantedRunes)]), wanted) {
			return true
		}
	}
	return false
}
