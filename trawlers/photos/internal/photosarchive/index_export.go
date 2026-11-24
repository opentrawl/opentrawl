package photosarchive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	"github.com/opentrawl/opentrawl/trawlkit/unifiedindex"
)

const photoIndexExportBatchSize = 4000

// ExportArchiveIndexChanges streams all active Apple Photos records (with their
// Apple facts and written photo cards) and Apple Photos trips into core
// index.db so global search, semantic hybrid search and `trawl around` cover
// Photos natively.
func (photosArchive *PhotosArchive) ExportArchiveIndexChanges(
	ctx context.Context,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	nextCursor, activePhotoCount, err := photosArchive.readIndexExportCursor(ctx)
	if err != nil {
		return err
	}
	if sinceOpaqueCursor != "" && sinceOpaqueCursor == nextCursor {
		return nil
	}

	namedPeopleByUUID, err := photosArchive.loadAllPhotoNamedPeople(ctx)
	if err != nil {
		return err
	}
	albumsByUUID, err := photosArchive.loadAllPhotoAlbums(ctx)
	if err != nil {
		return err
	}
	placeAreasByUUID, err := photosArchive.loadAllPhotoPlaceAreas(ctx)
	if err != nil {
		return err
	}
	recognisedTextByUUID, err := photosArchive.loadAllPhotoRecognisedText(ctx)
	if err != nil {
		return err
	}
	cardVisibleTextByUUID, err := photosArchive.loadAllPhotoCardVisibleText(ctx)
	if err != nil {
		return err
	}
	timeWindows, err := photosArchive.loadAllTripTimeWindows(ctx)
	if err != nil {
		return err
	}

	rows, err := photosArchive.database.QueryContext(ctx, `
select p.apple_photo_uuid, p.apple_media_kind, p.is_apple_detected_screenshot,
  p.capture_time_utc, p.capture_utc_offset_seconds,
  p.apple_stored_full_address, p.apple_stored_iso_country_code, p.apple_stored_country_name,
  p.apple_stored_state_name, p.apple_stored_county_name, p.apple_stored_town_name,
  p.apple_stored_neighbourhood_name, p.apple_stored_street,
  p.apple_stored_point_of_interest_name, p.apple_stored_water_name, p.apple_stored_island_name,
  p.apple_moment_title, p.camera_make, p.camera_model,
  p.apple_caption, p.apple_title, p.is_favourite,
  case when c.photo_card_outcome = ? then c.one_line_summary else null end,
  case when c.photo_card_outcome = ? then c.description else null end,
  case when c.photo_card_outcome = ? then c.place_shown_name else null end
from apple_library_photo p
left join photo_card c on c.apple_photo_uuid = p.apple_photo_uuid
where p.absent_from_apple_library_since_time_utc is null
order by p.capture_time_utc desc, p.apple_photo_uuid`,
		photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_WRITTEN.String(),
		photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_WRITTEN.String(),
		photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_WRITTEN.String(),
	)
	if err != nil {
		return fmt.Errorf("read photos for unified index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	batchCapacity := min(int(activePhotoCount), photoIndexExportBatchSize)
	if batchCapacity < 0 {
		batchCapacity = 0
	}
	upserts := make([]*indexcontract.IndexedArchiveRecordUpsert, 0, batchCapacity)
	emittedFirstBatch := false

	for rows.Next() {
		var (
			uuid                                                  string
			mediaKind                                             int32
			isScreenshot, isFavourite                             bool
			captureTimeUTC                                        string
			captureUTCOffset                                      sql.NullInt64
			fullAddress, isoCountry, country, state, county, town sql.NullString
			neighbourhood, street, poi, water, island             sql.NullString
			momentTitle, cameraMake, cameraModel                  sql.NullString
			caption, title                                        sql.NullString
			cardSummary, cardDescription, cardPlaceShown          sql.NullString
		)
		if err := rows.Scan(
			&uuid, &mediaKind, &isScreenshot,
			&captureTimeUTC, &captureUTCOffset,
			&fullAddress, &isoCountry, &country,
			&state, &county, &town,
			&neighbourhood, &street,
			&poi, &water, &island,
			&momentTitle, &cameraMake, &cameraModel,
			&caption, &title, &isFavourite,
			&cardSummary, &cardDescription, &cardPlaceShown,
		); err != nil {
			return fmt.Errorf("scan photo for unified index: %w", err)
		}

		canonicalRefText := "photos:" + uuid
		ref := trawlkit.NewCanonicalArchiveRecordReference(canonicalRefText)

		var occurredUnix int64
		hasOccurredTime := false
		if parsedTime, parseErr := parseArchiveTime(captureTimeUTC); parseErr == nil && !parsedTime.IsZero() {
			occurredUnix = parsedTime.Unix()
			hasOccurredTime = true
		}
		var utcOffsetSeconds int32
		if captureUTCOffset.Valid {
			utcOffsetSeconds = int32(captureUTCOffset.Int64)
		}

		displayContext := strings.TrimSpace(cardPlaceShown.String)
		if displayContext == "" {
			displayContext = compactPhotoPlaceDisplay(
				poi.String, street.String, neighbourhood.String, town.String, state.String, country.String,
			)
		}

		recordTitle := firstNonEmptyTrimmed(
			cardSummary.String,
			title.String,
			caption.String,
			momentTitle.String,
			photoKindFallbackTitle(mediaKind, isScreenshot),
		)

		visibleTextEntries := cardVisibleTextByUUID[uuid]
		recognisedLines := recognisedTextByUUID[uuid]

		bodyParts := make([]string, 0, 6)
		if d := strings.TrimSpace(cardDescription.String); d != "" {
			bodyParts = append(bodyParts, d)
		}
		if len(visibleTextEntries) > 0 {
			bodyParts = append(bodyParts, strings.Join(visibleTextEntries, "\n"))
		}
		if c := strings.TrimSpace(caption.String); c != "" && c != recordTitle {
			bodyParts = append(bodyParts, c)
		}
		if t := strings.TrimSpace(title.String); t != "" && t != recordTitle {
			bodyParts = append(bodyParts, t)
		}
		if len(recognisedLines) > 0 {
			bodyParts = append(bodyParts, strings.Join(recognisedLines, "\n"))
		}
		bodyText := strings.Join(bodyParts, "\n")

		albums := albumsByUUID[uuid]
		placeAreas := placeAreasByUUID[uuid]
		metaParts := make([]string, 0, 16+len(albums)+len(placeAreas))
		for _, field := range []string{
			cardPlaceShown.String,
			fullAddress.String,
			poi.String,
			street.String,
			neighbourhood.String,
			town.String,
			county.String,
			state.String,
			country.String,
			isoCountry.String,
			water.String,
			island.String,
			momentTitle.String,
			strings.TrimSpace(cameraMake.String + " " + cameraModel.String),
		} {
			if trimmed := strings.TrimSpace(field); trimmed != "" {
				metaParts = append(metaParts, trimmed)
			}
		}
		metaParts = append(metaParts, placeAreas...)
		metaParts = append(metaParts, albums...)
		metadataText := strings.Join(dedupeNonEmptyStrings(metaParts), " ")

		people := namedPeopleByUUID[uuid]
		participants := make([]*indexcontract.ArchiveRecordParticipant, 0, len(people))
		for _, personName := range people {
			participants = append(participants, &indexcontract.ArchiveRecordParticipant{
				DisplayName:     personName,
				ParticipantRole: "person",
			})
		}

		labels := make([]string, 0, len(albums)+4)
		labels = append(labels, albums...)
		if m := strings.TrimSpace(momentTitle.String); m != "" {
			labels = append(labels, m)
		}
		if isFavourite {
			labels = append(labels, "Favourite")
		}
		if isScreenshot {
			labels = append(labels, "Screenshot")
		}
		if mediaKind == 1 {
			labels = append(labels, "Video")
		}

		anchorSource := strings.Join(visibleTextEntries, " ") + " " + strings.Join(recognisedLines, " ") + " " + caption.String
		anchors := buildPhotoMechanicalJoinAnchors(anchorSource)

		upserts = append(upserts, &indexcontract.IndexedArchiveRecordUpsert{
			StableRecordReference:          ref,
			ResolvedRecordReference:        ref,
			RecordAnchor:                   trawlkit.NewRecordAnchorIdentifier(openrecord.PhotoDetailAnchorID),
			RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_PHOTO,
			ConversationOrThreadIdentifier: canonicalRefText,
			DisplayContextTitle:            displayContext,
			OccurredUnixSeconds:            occurredUnix,
			HasOccurredTime:                hasOccurredTime,
			HasTimeOfDay:                   hasOccurredTime,
			UtcOffsetSeconds:               utcOffsetSeconds,
			Title:                          recordTitle,
			BodyText:                       bodyText,
			MetadataText:                   metadataText,
			Participants:                   participants,
			ObservedSourceLabels:           dedupeNonEmptyStrings(labels),
			MechanicalJoinAnchors:          anchors,
		})

		if len(upserts) >= photoIndexExportBatchSize {
			batch := &indexcontract.ArchiveIndexChangeBatch{
				Upserts:                     upserts,
				CompleteSnapshotReplacement: !emittedFirstBatch,
			}
			if err := emit(batch); err != nil {
				return err
			}
			emittedFirstBatch = true
			upserts = make([]*indexcontract.IndexedArchiveRecordUpsert, 0, photoIndexExportBatchSize)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate photos for unified index: %w", err)
	}

	return emit(&indexcontract.ArchiveIndexChangeBatch{
		Upserts:                     upserts,
		TimeWindows:                 timeWindows,
		CompleteSnapshotReplacement: !emittedFirstBatch,
		NextOpaqueCursor:            nextCursor,
	})
}

func (photosArchive *PhotosArchive) readIndexExportCursor(ctx context.Context) (string, int64, error) {
	var activeCount int64
	var maxMod, maxSeen string
	if err := photosArchive.database.QueryRowContext(ctx, `
select count(*),
  coalesce(max(apple_modification_time_utc), ''),
  coalesce(max(first_seen_time_utc), '')
from apple_library_photo
where absent_from_apple_library_since_time_utc is null`,
	).Scan(&activeCount, &maxMod, &maxSeen); err != nil {
		return "", 0, fmt.Errorf("read photos index cursor state: %w", err)
	}

	var writtenCardCount int64
	var maxCardWritten string
	if err := photosArchive.database.QueryRowContext(ctx, `
select count(*), coalesce(max(written_time_utc), '')
from photo_card
where photo_card_outcome = ?`,
		photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_WRITTEN.String(),
	).Scan(&writtenCardCount, &maxCardWritten); err != nil {
		return "", 0, fmt.Errorf("read photo cards index cursor state: %w", err)
	}

	var tripCount int64
	var maxTripEnd string
	if err := photosArchive.database.QueryRowContext(ctx, `
select count(*), coalesce(max(apple_trip_end_time_utc), '')
from apple_library_trip`,
	).Scan(&tripCount, &maxTripEnd); err != nil {
		return "", 0, fmt.Errorf("read photo trips index cursor state: %w", err)
	}

	cursor := fmt.Sprintf("photos:%d:%s:%s:%d:%s:%d:%s",
		activeCount, maxMod, maxSeen, writtenCardCount, maxCardWritten, tripCount, maxTripEnd)
	return cursor, activeCount, nil
}

func (photosArchive *PhotosArchive) loadAllPhotoNamedPeople(ctx context.Context) (map[string][]string, error) {
	return photosArchive.loadPhotoStringListMap(ctx,
		`select apple_photo_uuid, person_full_name from apple_library_photo_named_person order by apple_photo_uuid, person_full_name`,
		"named people",
	)
}

func (photosArchive *PhotosArchive) loadAllPhotoAlbums(ctx context.Context) (map[string][]string, error) {
	return photosArchive.loadPhotoStringListMap(ctx,
		`select apple_photo_uuid, album_title from apple_library_photo_album order by apple_photo_uuid, album_title`,
		"albums",
	)
}

func (photosArchive *PhotosArchive) loadAllPhotoPlaceAreas(ctx context.Context) (map[string][]string, error) {
	return photosArchive.loadPhotoStringListMap(ctx,
		`select apple_photo_uuid, area_name from apple_library_photo_place_area order by apple_photo_uuid, area_rank_in_apple_source_order`,
		"place areas",
	)
}

func (photosArchive *PhotosArchive) loadAllPhotoRecognisedText(ctx context.Context) (map[string][]string, error) {
	return photosArchive.loadPhotoStringListMap(ctx,
		`select apple_photo_uuid, recognised_text from apple_library_photo_recognised_text_line order by apple_photo_uuid, line_rank`,
		"recognised text",
	)
}

func (photosArchive *PhotosArchive) loadPhotoStringListMap(ctx context.Context, query, label string) (map[string][]string, error) {
	rows, err := photosArchive.database.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read photo %s for unified index: %w", label, err)
	}
	defer func() { _ = rows.Close() }()
	result := map[string][]string{}
	for rows.Next() {
		var uuid, value string
		if err := rows.Scan(&uuid, &value); err != nil {
			return nil, fmt.Errorf("scan photo %s for unified index: %w", label, err)
		}
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result[uuid] = append(result[uuid], trimmed)
		}
	}
	return result, rows.Err()
}

func (photosArchive *PhotosArchive) loadAllPhotoCardVisibleText(ctx context.Context) (map[string][]string, error) {
	rows, err := photosArchive.database.QueryContext(ctx, `
select v.apple_photo_uuid, v.text_as_written, coalesce(v.english_rendering, '')
from photo_card_visible_text v
join photo_card c on c.apple_photo_uuid = v.apple_photo_uuid
where c.photo_card_outcome = ?
order by v.apple_photo_uuid, v.text_group_rank`,
		photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_WRITTEN.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("read photo card visible text for unified index: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := map[string][]string{}
	for rows.Next() {
		var uuid, textAsWritten, englishRendering string
		if err := rows.Scan(&uuid, &textAsWritten, &englishRendering); err != nil {
			return nil, fmt.Errorf("scan photo card visible text for unified index: %w", err)
		}
		if trimmed := strings.TrimSpace(textAsWritten); trimmed != "" {
			result[uuid] = append(result[uuid], trimmed)
		}
		if trimmed := strings.TrimSpace(englishRendering); trimmed != "" {
			result[uuid] = append(result[uuid], trimmed)
		}
	}
	return result, rows.Err()
}

func (photosArchive *PhotosArchive) loadAllTripTimeWindows(ctx context.Context) ([]*indexcontract.IndexedArchiveTimeWindow, error) {
	rows, err := photosArchive.database.QueryContext(ctx, `
select apple_trip_uuid, coalesce(apple_trip_place_title, ''), coalesce(apple_trip_date_title, ''),
  apple_trip_start_time_utc, apple_trip_end_time_utc
from apple_library_trip
order by apple_trip_start_time_utc, apple_trip_uuid`)
	if err != nil {
		return nil, fmt.Errorf("read photo trips for unified index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var windows []*indexcontract.IndexedArchiveTimeWindow
	for rows.Next() {
		var tripUUID, placeTitle, dateTitle, startUTC, endUTC string
		if err := rows.Scan(&tripUUID, &placeTitle, &dateTitle, &startUTC, &endUTC); err != nil {
			return nil, fmt.Errorf("scan photo trip for unified index: %w", err)
		}
		startTime, err := parseArchiveTime(startUTC)
		if err != nil {
			continue
		}
		endTime, err := parseArchiveTime(endUTC)
		if err != nil {
			continue
		}
		windows = append(windows, &indexcontract.IndexedArchiveTimeWindow{
			TimeWindowIdentifier: "photos:trip:" + tripUUID,
			TimeWindowKind:       "trip",
			PlaceTitle:           strings.TrimSpace(placeTitle),
			DateTitle:            strings.TrimSpace(dateTitle),
			StartUnixSeconds:     startTime.Unix(),
			EndUnixSeconds:       endTime.Unix(),
		})
	}
	return windows, rows.Err()
}

func compactPhotoPlaceDisplay(parts ...string) string {
	return strings.Join(dedupeNonEmptyStrings(parts), " › ")
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func photoKindFallbackTitle(mediaKind int32, isScreenshot bool) string {
	if isScreenshot {
		return "Screenshot"
	}
	if mediaKind == 1 {
		return "Video"
	}
	return "Photo"
}

func dedupeNonEmptyStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, trimmed)
	}
	return out
}

func buildPhotoMechanicalJoinAnchors(sourceText string) []*indexcontract.MechanicalJoinAnchor {
	trimmed := strings.TrimSpace(sourceText)
	if trimmed == "" {
		return nil
	}
	domains := unifiedindex.ExtractRegistrableDomains(unifiedindex.TruncateRunes(trimmed, 2000))
	if len(domains) == 0 {
		return nil
	}
	anchors := make([]*indexcontract.MechanicalJoinAnchor, 0, len(domains))
	for _, domain := range domains {
		anchors = append(anchors, &indexcontract.MechanicalJoinAnchor{
			AnchorKind:  indexcontract.MechanicalJoinAnchorKind_MECHANICAL_JOIN_ANCHOR_KIND_REGISTRABLE_DOMAIN,
			AnchorValue: domain,
		})
	}
	return anchors
}
