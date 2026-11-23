package health

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	contract "github.com/opentrawl/opentrawl/trawlers/health/proto/trawl/health"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	plugincontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/plugin"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type App struct{}

var (
	_ trawlkit.CanonicalRecordOpener      = (*App)(nil)
	_ trawlkit.ArchiveIndexChangeExporter = (*App)(nil)
	_ trawlkit.Searcher                   = (*App)(nil)
	_ trawlkit.Updater                    = (*App)(nil)
	_ trawlkit.PluginCapabilityDeclarator = (*App)(nil)
)

func New() *App                                                                   { return &App{} }
func (*App) LoadTrawlerConfiguration(trawlkit.TrawlerConfigurationFilePath) error { return nil }

func (*App) DeclaredPluginCapabilities() *plugincontract.PluginDeclaredCapabilities {
	return &plugincontract.PluginDeclaredCapabilities{
		SupportsArchiveIndexChangeExport: true,
	}
}

func (*App) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity("health"),
		RegisteredTrawlerCommandName: "health",
		RegisteredTrawlerDisplayName: "Health",
		RegisteredTrawlerBranding: &federation.TrawlerBranding{
			SymbolName:  "heart.fill",
			AccentColor: "#FF2D55",
		},
		SourceFreshnessDependency: federation.SourceFreshnessDependency_SOURCE_FRESHNESS_DEPENDENCY_NONE,
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Apple Health exports, HealthFit .fit files, and HealthKit batches placed in imports/ or passed to import.",
			LeavesMachine:   "Nothing.",
			NetworkRequests: "None.",
		},
	}
}

func (a *App) TrawlerCommands() []trawlkit.TrawlerCommand {
	var commands []trawlkit.TrawlerCommand
	for _, operation := range []federation.SharedTrawlerOperation{
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN,
	} {
		commands = append(commands, trawlkit.TrawlerCommand{
			SharedTrawlerOperation:           operation,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand,
		})
	}

	var (
		workoutLimit  int
		workoutSport  string
		workoutAfter  string
		workoutBefore string

		sleepLimit  int
		sleepAfter  string
		sleepBefore string
		sleepStages bool

		metricsType   string
		metricsAfter  string
		metricsBefore string
		metricsLimit  int

		summaryDays   int
		summaryAfter  string
		summaryBefore string
	)

	commands = append(commands,
		trawlkit.TrawlerCommand{
			TrawlerCommandName:                    "import",
			TrawlerCommandHelpDescription:         "Import an Apple Health export.zip, export.xml, HealthFit .fit file or directory, or HealthKit batch",
			TrawlerCommandDiscoveryPlacement:      trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandPositionalArgumentNames: []string{"PATH"},
			TrawlerCommandChangesArchive:          true,
			TrawlerCommandArchiveAccess:           trawlkit.TrawlerCommandArchiveAccessRequired,
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
				if len(req.TrawlerCommandPositionalArguments) == 0 {
					return nil, errors.New("path to an Apple Health export, .fit file, directory, or batch is required")
				}
				db := req.OpenedTrawlerArchiveStore.DB()
				if err := ensureHealthArchiveSchema(ctx, db); err != nil {
					return nil, err
				}
				progressFn := func(phase string, done int64, message string) {
					if req.ReportTrawlerCommandProgress != nil {
						req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: phase, Done: done, Message: message})
					}
				}
				var totalStats healthImportStatistics
				for _, pathArg := range req.TrawlerCommandPositionalArguments {
					stats, err := importHealthSourcePath(ctx, db, pathArg, progressFn)
					if err != nil {
						return nil, err
					}
					totalStats.merge(stats)
				}
				if err := recordHealthUpdateCompletion(ctx, db); err != nil {
					return nil, err
				}
				return buildHealthImportReportPresentation(strings.Join(req.TrawlerCommandPositionalArguments, ", "), totalStats), nil
			},
		},
		trawlkit.TrawlerCommand{
			TrawlerCommandName:               "workouts",
			TrawlerCommandHelpDescription:    "List workouts, newest first",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
			TrawlerCommandArchiveAccess:      trawlkit.TrawlerCommandArchiveAccessRequired,
			RegisterTrawlerCommandFlags: func(flags *flag.FlagSet) {
				flags.IntVar(&workoutLimit, "limit", 30, "Maximum workouts")
				flags.StringVar(&workoutSport, "sport", "", "Filter by sport (e.g. cycling, running, walking)")
				flags.StringVar(&workoutAfter, "after", "", "First date, YYYY-MM-DD")
				flags.StringVar(&workoutBefore, "before", "", "Last date, YYYY-MM-DD")
			},
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
				return runHealthWorkoutsCommand(ctx, req, workoutLimit, workoutSport, workoutAfter, workoutBefore)
			},
		},
		trawlkit.TrawlerCommand{
			TrawlerCommandName:               "sleep",
			TrawlerCommandHelpDescription:    "List nightly sleep summaries or individual sleep stages, newest first",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
			TrawlerCommandArchiveAccess:      trawlkit.TrawlerCommandArchiveAccessRequired,
			RegisterTrawlerCommandFlags: func(flags *flag.FlagSet) {
				flags.IntVar(&sleepLimit, "limit", 30, "Maximum nights or stages")
				flags.StringVar(&sleepAfter, "after", "", "First date, YYYY-MM-DD")
				flags.StringVar(&sleepBefore, "before", "", "Last date, YYYY-MM-DD")
				flags.BoolVar(&sleepStages, "stages", false, "Show individual sleep stage intervals instead of nightly summaries")
			},
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
				return runHealthSleepCommand(ctx, req, sleepLimit, sleepAfter, sleepBefore, sleepStages)
			},
		},
		trawlkit.TrawlerCommand{
			TrawlerCommandName:               "metrics",
			TrawlerCommandHelpDescription:    "List archived HealthKit metric types or daily rollups for one metric",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
			TrawlerCommandArchiveAccess:      trawlkit.TrawlerCommandArchiveAccessRequired,
			RegisterTrawlerCommandFlags: func(flags *flag.FlagSet) {
				flags.StringVar(&metricsType, "type", "", "Metric name or HealthKit type identifier (e.g. heart rate, resting, steps, hrv, weight)")
				flags.StringVar(&metricsAfter, "after", "", "First date, YYYY-MM-DD")
				flags.StringVar(&metricsBefore, "before", "", "Last date, YYYY-MM-DD")
				flags.IntVar(&metricsLimit, "limit", 30, "Maximum daily rows")
			},
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
				return runHealthMetricsCommand(ctx, req, metricsType, metricsAfter, metricsBefore, metricsLimit)
			},
		},
		trawlkit.TrawlerCommand{
			TrawlerCommandName:               "summary",
			TrawlerCommandHelpDescription:    "Show daily health summary with activity, vitals, sleep and workouts",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
			TrawlerCommandArchiveAccess:      trawlkit.TrawlerCommandArchiveAccessRequired,
			RegisterTrawlerCommandFlags: func(flags *flag.FlagSet) {
				flags.IntVar(&summaryDays, "days", 14, "Number of most recent days")
				flags.StringVar(&summaryAfter, "after", "", "First date, YYYY-MM-DD")
				flags.StringVar(&summaryBefore, "before", "", "Last date, YYYY-MM-DD")
			},
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
				return runHealthSummaryCommand(ctx, req, summaryDays, summaryAfter, summaryBefore)
			},
		},
	)
	return commands
}

func validateOptionalCalendarDateFlag(name, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if !healthCalendarDatePattern.MatchString(trimmed) {
		return "", fmt.Errorf("%s must be YYYY-MM-DD", name)
	}
	return trimmed, nil
}

func runHealthWorkoutsCommand(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	limit int,
	sportFilter string,
	afterRaw string,
	beforeRaw string,
) (*command.TrawlerCommandResponse, error) {
	if limit < 1 {
		return nil, errors.New("limit must be at least 1")
	}
	afterDate, err := validateOptionalCalendarDateFlag("after", afterRaw)
	if err != nil {
		return nil, err
	}
	beforeDate, err := validateOptionalCalendarDateFlag("before", beforeRaw)
	if err != nil {
		return nil, err
	}
	db := req.OpenedTrawlerArchiveStore.DB()
	if err := ensureHealthArchiveSchema(ctx, db); err != nil {
		return nil, err
	}
	var where []string
	var args []any
	where = append(where, "record_kind=?")
	args = append(args, string(healthRecordKindWorkout))
	if afterDate != "" {
		where = append(where, "calendar_date >= ?")
		args = append(args, afterDate)
	}
	if beforeDate != "" {
		where = append(where, "calendar_date <= ?")
		args = append(args, beforeDate)
	}
	records, err := readStoredHealthRecords(ctx, db, "WHERE "+strings.Join(where, " AND ")+" ORDER BY start_unix DESC", args...)
	if err != nil {
		return nil, err
	}
	sportQuery := strings.ToLower(strings.TrimSpace(sportFilter))
	if sportQuery != "" {
		sportFamily := normalizeWorkoutSportFamily(sportQuery)
		filtered := make([]storedHealthRecord, 0, len(records))
		for _, stored := range records {
			workout, ok := stored.record.(*contract.HealthWorkout)
			if !ok {
				continue
			}
			activity := workout.GetHealthkitWorkoutActivityTypeName()
			if normalizeWorkoutSportFamily(activity) == sportFamily ||
				strings.Contains(strings.ToLower(activity), sportQuery) ||
				strings.Contains(strings.ToLower(workout.GetSportSubType()), sportQuery) {
				filtered = append(filtered, stored)
			}
		}
		records = filtered
	}
	if len(records) > limit {
		records = records[:limit]
	}
	return buildHealthWorkoutsPresentation(records), nil
}

func runHealthSleepCommand(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	limit int,
	afterRaw string,
	beforeRaw string,
	showStages bool,
) (*command.TrawlerCommandResponse, error) {
	if limit < 1 {
		return nil, errors.New("limit must be at least 1")
	}
	afterDate, err := validateOptionalCalendarDateFlag("after", afterRaw)
	if err != nil {
		return nil, err
	}
	beforeDate, err := validateOptionalCalendarDateFlag("before", beforeRaw)
	if err != nil {
		return nil, err
	}
	db := req.OpenedTrawlerArchiveStore.DB()
	if err := ensureHealthArchiveSchema(ctx, db); err != nil {
		return nil, err
	}
	if showStages {
		var where []string
		var args []any
		where = append(where, "record_kind=?")
		args = append(args, string(healthRecordKindSleepSession))
		if afterDate != "" {
			where = append(where, "calendar_date >= ?")
			args = append(args, afterDate)
		}
		if beforeDate != "" {
			where = append(where, "calendar_date <= ?")
			args = append(args, beforeDate)
		}
		args = append(args, limit)
		records, err := readStoredHealthRecords(ctx, db, "WHERE "+strings.Join(where, " AND ")+" ORDER BY start_unix DESC LIMIT ?", args...)
		if err != nil {
			return nil, err
		}
		return buildHealthSleepStagesPresentation(records), nil
	}
	nights, err := buildSleepNightRollups(ctx, db, afterDate, beforeDate)
	if err != nil {
		return nil, err
	}
	if len(nights) > limit {
		nights = nights[:limit]
	}
	return buildHealthSleepNightsPresentation(nights), nil
}

func runHealthMetricsCommand(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	metricTypeQuery string,
	afterRaw string,
	beforeRaw string,
	limit int,
) (*command.TrawlerCommandResponse, error) {
	if limit < 1 {
		return nil, errors.New("limit must be at least 1")
	}
	afterDate, err := validateOptionalCalendarDateFlag("after", afterRaw)
	if err != nil {
		return nil, err
	}
	beforeDate, err := validateOptionalCalendarDateFlag("before", beforeRaw)
	if err != nil {
		return nil, err
	}
	db := req.OpenedTrawlerArchiveStore.DB()
	if err := ensureHealthArchiveSchema(ctx, db); err != nil {
		return nil, err
	}
	if strings.TrimSpace(metricTypeQuery) == "" {
		catalog, err := readHealthMetricCatalog(ctx, db)
		if err != nil {
			return nil, err
		}
		return buildHealthMetricsCatalogPresentation(catalog), nil
	}
	matchedTypes, err := resolveHealthMetricTypeQuery(ctx, db, metricTypeQuery)
	if err != nil {
		return nil, err
	}
	if len(matchedTypes) == 0 {
		return buildHealthMetricTrendPresentation(metricTypeQuery, nil), nil
	}
	aggs, err := readDailyAggregatesForMetricTypes(ctx, db, matchedTypes, afterDate, beforeDate, limit)
	if err != nil {
		return nil, err
	}
	return buildHealthMetricTrendPresentation(metricTypeQuery, aggs), nil
}

func runHealthSummaryCommand(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	days int,
	afterRaw string,
	beforeRaw string,
) (*command.TrawlerCommandResponse, error) {
	if days < 1 {
		return nil, errors.New("days must be at least 1")
	}
	afterDate, err := validateOptionalCalendarDateFlag("after", afterRaw)
	if err != nil {
		return nil, err
	}
	beforeDate, err := validateOptionalCalendarDateFlag("before", beforeRaw)
	if err != nil {
		return nil, err
	}
	db := req.OpenedTrawlerArchiveStore.DB()
	if err := ensureHealthArchiveSchema(ctx, db); err != nil {
		return nil, err
	}
	rollups, err := buildDailyHealthRollups(ctx, db, afterDate, beforeDate)
	if err != nil {
		return nil, err
	}
	if len(rollups) > days {
		rollups = rollups[:days]
	}
	return buildHealthSummaryPresentation(rollups), nil
}

func (*App) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	response := &status.TrawlerStatusResponse{TrawlerArchiveStatus: &status.TrawlerArchiveStatus{}}
	if req.OpenedTrawlerArchiveStore == nil {
		return response, nil
	}
	db := req.OpenedTrawlerArchiveStore.DB()
	if err := ensureHealthArchiveSchema(ctx, db); err != nil {
		return nil, err
	}

	var sampleCount uint64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM health_samples").Scan(&sampleCount); err != nil {
		return nil, err
	}

	counts := map[healthRecordKind]uint64{}
	rows, err := db.QueryContext(ctx, "SELECT record_kind, count(*) FROM health_records GROUP BY record_kind")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind string
		var count uint64
		if err := rows.Scan(&kind, &count); err != nil {
			_ = rows.Close()
			return nil, err
		}
		counts[healthRecordKind(kind)] = count
	}
	_ = rows.Close()

	var distinctDays uint64
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM (
			SELECT calendar_date FROM health_samples WHERE calendar_date != ''
			UNION
			SELECT calendar_date FROM health_records WHERE calendar_date != ''
		)`).Scan(&distinctDays); err != nil {
		return nil, err
	}

	appendCount := func(name, display string, count uint64, alwaysShow bool) {
		if !alwaysShow && count == 0 {
			return
		}
		response.TrawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = append(
			response.TrawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate,
			&status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{
				ArchiveContentKindName:        name,
				ArchiveContentKindDisplayName: display,
				ArchiveContentCount:           count,
			},
		)
	}
	appendCount("samples", "samples", sampleCount, sampleCount > 0)
	appendCount(string(healthRecordKindWorkout), "workouts", counts[healthRecordKindWorkout], true)
	appendCount(string(healthRecordKindSleepSession), "sleep stages", counts[healthRecordKindSleepSession], true)
	appendCount(string(healthRecordKindDailyMetrics), "days", distinctDays, true)
	appendCount(string(healthRecordKindCorrelation), "correlations", counts[healthRecordKindCorrelation], false)
	appendCount(string(healthRecordKindECG), "ECGs", counts[healthRecordKindECG], false)
	appendCount(string(healthRecordKindAudiogram), "audiograms", counts[healthRecordKindAudiogram], false)
	appendCount(string(healthRecordKindClinical), "clinical records", counts[healthRecordKindClinical], false)

	var completedText string
	if err := db.QueryRowContext(ctx, "SELECT completed_time FROM health_update_completion WHERE singleton=1").Scan(&completedText); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if completedText != "" {
		completed := &timestamppb.Timestamp{}
		if err := prototext.Unmarshal([]byte(completedText), completed); err != nil {
			return nil, err
		}
		response.TrawlerArchiveStatus.LastSuccessfullyCompletedArchiveUpdateTime = completed
	}

	var newestRecordUnix, newestSampleUnix sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT max(start_unix) FROM health_records").Scan(&newestRecordUnix); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx, "SELECT max(start_unix) FROM health_samples").Scan(&newestSampleUnix); err != nil {
		return nil, err
	}
	newest := int64(0)
	if newestRecordUnix.Valid && newestRecordUnix.Int64 > newest {
		newest = newestRecordUnix.Int64
	}
	if newestSampleUnix.Valid && newestSampleUnix.Int64 > newest {
		newest = newestSampleUnix.Int64
	}
	if newest > 0 {
		response.TrawlerArchiveStatus.NewestArchivedRecordTime = timestamppb.New(time.Unix(newest, 0))
	}
	response.TrawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands = true
	return response, nil
}

func (*App) OpenCanonicalRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	canonicalReference *trawlkit.CanonicalArchiveRecordReference,
) (*open.OpenRecord, error) {
	db := req.OpenedTrawlerArchiveStore.DB()
	if err := ensureHealthArchiveSchema(ctx, db); err != nil {
		return nil, err
	}
	ref := trawlkit.CanonicalArchiveRecordReferenceText(canonicalReference)

	wrapOpened := func(pres *open.TrawlerSpecificOpenedRecordPresentation) *open.OpenRecord {
		return &open.OpenRecord{
			RecordTrawler:            trawlkit.NewRegisteredTrawlerIdentity("health"),
			CanonicalRecordReference: canonicalReference,
			TypedOpenedRecord: &open.OpenRecord_TrawlerSpecificOpenedRecordPresentation{
				TrawlerSpecificOpenedRecordPresentation: pres,
			},
		}
	}

	// 1. Dynamic nightly sleep rollup reference: health:sleep-night/YYYY-MM-DD.
	if nightDate, ok := strings.CutPrefix(ref, healthReferencePrefixSleepNight); ok {
		nights, err := buildSleepNightRollups(ctx, db, nightDate, nightDate)
		if err != nil {
			return nil, err
		}
		if len(nights) == 0 {
			return nil, fmt.Errorf("health sleep night %s not found", nightDate)
		}
		return wrapOpened(buildOpenedSleepNightPresentation(nights[0])), nil
	}

	// 2. Dynamic notable category event reference: health:event/<content_hash>.
	if contentHash, ok := strings.CutPrefix(ref, healthReferencePrefixEvent); ok {
		events, err := readNotableCategoryEvents(ctx, db, contentHash)
		if err != nil {
			return nil, err
		}
		if len(events) == 0 {
			return nil, fmt.Errorf("health event not found")
		}
		return wrapOpened(buildOpenedNotableEventPresentation(events[0])), nil
	}

	// 3. Unified daily health reference: health:day/YYYY-MM-DD.
	if dayDate, ok := strings.CutPrefix(ref, "health:day/"); ok && healthCalendarDatePattern.MatchString(dayDate) {
		days, err := buildDailyHealthRollups(ctx, db, dayDate, dayDate)
		if err != nil {
			return nil, err
		}
		if len(days) > 0 {
			return wrapOpened(buildOpenedDailyHealthPresentation(days[0])), nil
		}
	}

	// 4. Direct stored protobuf record in health_records.
	records, err := readStoredHealthRecords(ctx, db, "WHERE reference=?", ref)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("health record not found")
	}
	return wrapOpened(buildOpenedHealthRecordPresentation(records[0])), nil
}

// Search is the per-trawler fallback search used when the unified index.db is
// unavailable. Normal searches read index.db (populated by ExportArchiveIndexChanges).
func (*App) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*search.TrawlerSearchResponse, error) {
	response := &search.TrawlerSearchResponse{}
	if strings.TrimSpace(query.Text) == "" || query.PersonFilter.ResolvedPersonFilter() != nil || query.PersonFilter.UnresolvedPersonFilterText() != "" {
		return response, nil
	}
	db := req.OpenedTrawlerArchiveStore.DB()
	if err := ensureHealthArchiveSchema(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		"SELECT reference, "+store.FTS5MarkedSearchResultSnippetSQLExpression("health_search", 1)+" FROM health_search WHERE health_search MATCH ?",
		store.FTS5TokenQuery(query.Text))
	if err != nil {
		return nil, err
	}
	snippets := map[string]string{}
	for rows.Next() {
		var reference, snippet string
		if err := rows.Scan(&reference, &snippet); err != nil {
			_ = rows.Close()
			return nil, err
		}
		snippets[reference] = snippet
	}
	_ = rows.Close()
	for reference, snippet := range snippets {
		records, err := readStoredHealthRecords(ctx, db, "WHERE reference=?", reference)
		if err != nil || len(records) == 0 {
			continue
		}
		stored := records[0]
		if (!query.After.IsZero() && stored.startUnix < query.After.Unix()) || (!query.Before.IsZero() && stored.startUnix > query.Before.Unix()) {
			continue
		}
		title, _ := healthRecordTitleAndFacts(stored.record)
		response.TrawlerSearchMatchesInDisplayOrder = append(response.TrawlerSearchMatchesInDisplayOrder, &search.TrawlerSearchMatch{
			RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(healthDetailAnchorIdentifier),
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(reference),
			SearchMatchPresentation: &search.SearchMatchPresentation{
				MatchingRecordKindDisplayName:       "Health",
				MatchingRecordDisplayName:           title,
				MatchingRecordAssociatedTime:        healthAssociatedTime(stored.startUnix, stored.kind == healthRecordKindDailyMetrics, stored.calendarDate),
				SearchMatchTextFieldsInDisplayOrder: []*search.SearchMatchTextField{trawlkit.NewSearchMatchTextFieldFromFTS5TextRuns("Health", store.ParseFTS5MarkedText(snippet))},
			},
		})
	}
	sort.SliceStable(response.TrawlerSearchMatchesInDisplayOrder, func(i, j int) bool {
		return response.TrawlerSearchMatchesInDisplayOrder[i].GetSearchMatchPresentation().GetMatchingRecordAssociatedTime().GetExactTime().AsTime().After(
			response.TrawlerSearchMatchesInDisplayOrder[j].GetSearchMatchPresentation().GetMatchingRecordAssociatedTime().GetExactTime().AsTime())
	})
	response.TotalSearchMatches = uint64(len(response.TrawlerSearchMatchesInDisplayOrder))
	if query.Limit > 0 && len(response.TrawlerSearchMatchesInDisplayOrder) > query.Limit {
		response.TrawlerSearchMatchesInDisplayOrder = response.TrawlerSearchMatchesInDisplayOrder[:query.Limit]
		response.MoreSearchMatchesExist = true
	}
	return response, nil
}

func (*App) RecordSuccessfullyCompletedArchiveUpdate(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) error {
	return recordHealthUpdateCompletion(ctx, req.OpenedTrawlerArchiveStore.DB())
}
