package health

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
)

// ExportArchiveIndexChanges emits bounded, high-signal health records into the
// unified OpenTrawl index.db:
//   - Every workout (Apple Health, HealthKit, and deduplicated HealthFit .fit)
//   - Every nightly sleep rollup (avoiding 20+ fragmented stage rows per night)
//   - Every daily health metric rollup (combining activity rings, HealthDailyMetrics,
//     and SQL daily rollups across every archived HealthKit metric type)
//   - Every correlation (blood pressure, nutrition), ECG, audiogram, and clinical record
//   - Every notable discrete category event (symptoms, irregular rhythm, high/low HR, mindfulness)
//
// Raw 1-second sensor ticks remain in health.db for exact SQL queries without
// flooding index.db or semantic embeddings.
func (*App) ExportArchiveIndexChanges(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	if req.OpenedTrawlerArchiveStore == nil {
		return nil
	}
	db := req.OpenedTrawlerArchiveStore.DB()
	if err := ensureHealthArchiveSchema(ctx, db); err != nil {
		return err
	}
	nextCursor, err := healthArchiveCursor(ctx, db)
	if err != nil {
		return err
	}
	if sinceOpaqueCursor != "" && sinceOpaqueCursor == nextCursor {
		return nil
	}

	var upserts []*indexcontract.IndexedArchiveRecordUpsert
	var shortRefOnly []*indexcontract.ShortReferenceOnlyRegistrationEntry

	// 1. Stored structured records (workouts, correlations, ECGs, audiograms, clinical records).
	// Individual sleep stages are registered as short-ref-only so `trawl health sleep --stages`
	// links open cleanly while search indexes nightly rollups.
	records, err := readStoredHealthRecords(ctx, db, "ORDER BY start_unix ASC")
	if err != nil {
		return err
	}
	for _, stored := range records {
		canonical := trawlkit.NewCanonicalArchiveRecordReference(stored.reference)
		switch stored.kind {
		case healthRecordKindSleepSession:
			shortRefOnly = append(shortRefOnly, &indexcontract.ShortReferenceOnlyRegistrationEntry{
				StableRecordReference:   canonical,
				ResolvedRecordReference: canonical,
			})
			continue
		case healthRecordKindDailyMetrics, healthRecordKindActivitySummary:
			// Folded into unified daily health rollups below so each day appears
			// once in index.db with all metrics, rings, sleep, and workouts.
			continue
		case healthRecordKindWorkout, healthRecordKindCorrelation, healthRecordKindECG, healthRecordKindAudiogram, healthRecordKindClinical:
			title, facts := healthRecordTitleAndFacts(stored.record)
			contextTitle := healthRecordSourceName(stored.record)
			if contextTitle == "" {
				contextTitle = stored.deviceName
			}
			upserts = append(upserts, &indexcontract.IndexedArchiveRecordUpsert{
				StableRecordReference:          canonical,
				ResolvedRecordReference:        canonical,
				RecordAnchor:                   trawlkit.NewRecordAnchorIdentifier(healthDetailAnchorIdentifier),
				RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_HEALTH_RECORD,
				ConversationOrThreadIdentifier: stored.reference,
				DisplayContextTitle:            contextTitle,
				OccurredUnixSeconds:            stored.startUnix,
				HasOccurredTime:                stored.startUnix > 0,
				HasTimeOfDay:                   true,
				UtcOffsetSeconds:               stored.utcOffsetSeconds,
				EndUnixSeconds:                 stored.endUnix,
				HasEndTime:                     stored.endUnix > stored.startUnix,
				Title:                          title,
				BodyText:                       strings.Join(facts, " · "),
				MetadataText:                   strings.TrimSpace(healthRecordSearchableText(stored.record) + " " + stored.calendarDate),
			})
		}
	}

	// 2. Nightly sleep rollups (one high-signal document per night).
	sleepNights, err := buildSleepNightRollups(ctx, db, "", "")
	if err != nil {
		return err
	}
	for _, night := range sleepNights {
		ref := healthSleepNightReference(night.calendarDate)
		canonical := trawlkit.NewCanonicalArchiveRecordReference(ref)
		asleepText := healthDurationText(night.totalAsleep)
		breakdown := formatSleepNightBreakdown(night)
		body := asleepText + " asleep"
		if breakdown != "" {
			body += " · " + breakdown
		}
		body += " · " + formatSleepNightWindow(night)
		upserts = append(upserts, &indexcontract.IndexedArchiveRecordUpsert{
			StableRecordReference:          canonical,
			ResolvedRecordReference:        canonical,
			RecordAnchor:                   trawlkit.NewRecordAnchorIdentifier(healthDetailAnchorIdentifier),
			RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_HEALTH_RECORD,
			ConversationOrThreadIdentifier: ref,
			DisplayContextTitle:            strings.Join(night.sources, ", "),
			OccurredUnixSeconds:            night.bedtimeUnix,
			HasOccurredTime:                true,
			HasTimeOfDay:                   true,
			UtcOffsetSeconds:               night.utcOffset,
			EndUnixSeconds:                 night.wakeUnix,
			HasEndTime:                     night.wakeUnix > night.bedtimeUnix,
			Title:                          "Sleep " + night.calendarDate,
			BodyText:                       body,
			MetadataText:                   strings.TrimSpace("sleep night core deep rem awake bedtime wake " + night.calendarDate + " " + strings.Join(night.sources, " ")),
		})
	}

	// 3. Unified daily health rollups (one bounded document per day across all metrics).
	dailyRollups, err := buildDailyHealthRollups(ctx, db, "", "")
	if err != nil {
		return err
	}
	for _, day := range dailyRollups {
		_, _, allFacts, keywords := formatDailyRollupFacts(day)
		if day.sleepNight != nil && day.sleepNight.totalAsleep > 0 {
			allFacts = append(allFacts, healthDurationText(day.sleepNight.totalAsleep)+" sleep")
			keywords = append(keywords, "sleep")
		}
		for _, w := range day.workouts {
			wTitle, _ := healthRecordTitleAndFacts(w.record)
			allFacts = append(allFacts, wTitle)
			keywords = append(keywords, wTitle)
		}
		if len(allFacts) == 0 {
			continue
		}
		ref := healthRecordReference(healthRecordKindDailyMetrics, day.calendarDate)
		canonical := trawlkit.NewCanonicalArchiveRecordReference(ref)
		dayStart, err := time.Parse("2006-01-02", day.calendarDate)
		if err != nil {
			continue
		}
		upserts = append(upserts, &indexcontract.IndexedArchiveRecordUpsert{
			StableRecordReference:          canonical,
			ResolvedRecordReference:        canonical,
			RecordAnchor:                   trawlkit.NewRecordAnchorIdentifier(healthDetailAnchorIdentifier),
			RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_HEALTH_RECORD,
			ConversationOrThreadIdentifier: ref,
			DisplayContextTitle:            "Daily health",
			OccurredUnixSeconds:            dayStart.UTC().Unix(),
			HasOccurredTime:                true,
			HasTimeOfDay:                   false,
			Title:                          "Daily health " + day.calendarDate,
			BodyText:                       strings.Join(allFacts, " · "),
			MetadataText:                   strings.TrimSpace("daily health activity metrics trend " + day.calendarDate + " " + strings.Join(keywords, " ")),
		})
	}

	// 4. Notable discrete category events (symptoms, irregular rhythm, high/low HR, mindfulness).
	notableEvents, err := readNotableCategoryEvents(ctx, db, "")
	if err != nil {
		return err
	}
	for _, ev := range notableEvents {
		ref := healthNotableEventReference(ev.contentHash)
		canonical := trawlkit.NewCanonicalArchiveRecordReference(ref)
		title := describeHealthMetricType(ev.healthkitType).displayName
		var bodyParts []string
		if ev.endUnix > ev.startUnix {
			bodyParts = append(bodyParts, healthDurationText(time.Duration(ev.endUnix-ev.startUnix)*time.Second))
		}
		if raw := strings.TrimSpace(ev.rawValue); raw != "" {
			bodyParts = append(bodyParts, humanizeHealthKitIdentifier(raw))
		}
		if ev.sourceName != "" {
			bodyParts = append(bodyParts, ev.sourceName)
		}
		upserts = append(upserts, &indexcontract.IndexedArchiveRecordUpsert{
			StableRecordReference:          canonical,
			ResolvedRecordReference:        canonical,
			RecordAnchor:                   trawlkit.NewRecordAnchorIdentifier(healthDetailAnchorIdentifier),
			RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_HEALTH_RECORD,
			ConversationOrThreadIdentifier: ref,
			DisplayContextTitle:            ev.sourceName,
			OccurredUnixSeconds:            ev.startUnix,
			HasOccurredTime:                ev.startUnix > 0,
			HasTimeOfDay:                   true,
			UtcOffsetSeconds:               ev.utcOffsetSeconds,
			EndUnixSeconds:                 ev.endUnix,
			HasEndTime:                     ev.endUnix > ev.startUnix,
			Title:                          title,
			BodyText:                       strings.Join(bodyParts, " · "),
			MetadataText:                   fmt.Sprintf("health event %s %s %s %s", title, ev.healthkitType, ev.calendarDate, ev.sourceName),
		})
	}

	return emit(&indexcontract.ArchiveIndexChangeBatch{
		Upserts:                     upserts,
		ShortReferenceOnlyEntries:   shortRefOnly,
		CompleteSnapshotReplacement: true,
		NextOpaqueCursor:            nextCursor,
	})
}
