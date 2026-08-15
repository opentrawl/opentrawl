package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/opentrawl/opentrawl/calendar"
	"github.com/opentrawl/opentrawl/gmail"
	"github.com/opentrawl/opentrawl/trawlers/imessage"
	"github.com/opentrawl/opentrawl/trawlers/notes"
	"github.com/opentrawl/opentrawl/trawlers/telegram"
	"github.com/opentrawl/opentrawl/trawlers/whatsapp"
	"github.com/opentrawl/opentrawl/trawlkit"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	searchablerecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/searchable_record"
	"google.golang.org/protobuf/proto"
)

type searchableRecordExportTrawler struct {
	registeredTrawler registeredTrawlerName
	trawler           trawlkit.Trawler
}

func searchableRecordExportTrawlers() []searchableRecordExportTrawler {
	return []searchableRecordExportTrawler{
		{registeredTrawler: "calendar", trawler: calendar.New()},
		{registeredTrawler: "gmail", trawler: gmail.New()},
		{registeredTrawler: "imessage", trawler: imessage.New()},
		{registeredTrawler: "notes", trawler: notes.New()},
		{registeredTrawler: "telegram", trawler: telegram.New()},
		{registeredTrawler: "whatsapp", trawler: whatsapp.New()},
	}
}

func buildSearchResearchCorpus(arguments []string, output io.Writer, progressOutput io.Writer) error {
	databasePath, err := corpusDatabasePathFromBuildArguments(arguments)
	if err != nil {
		return err
	}
	passageChunkingStarted, err := corpusPassageChunkingStarted(databasePath)
	if err != nil {
		return err
	}
	if !passageChunkingStarted {
		if err := projectSearchResearchCorpus(arguments, io.Discard, progressOutput); err != nil {
			return err
		}
	}
	return chunkSearchResearchCorpus([]string{"--database", databasePath}, output)
}

func corpusDatabasePathFromBuildArguments(arguments []string) (string, error) {
	for argumentIndex, argument := range arguments {
		if argument == "--database" && argumentIndex+1 < len(arguments) {
			if databasePath := strings.TrimSpace(arguments[argumentIndex+1]); databasePath != "" {
				return databasePath, nil
			}
		}
		if databasePath, found := strings.CutPrefix(argument, "--database="); found && strings.TrimSpace(databasePath) != "" {
			return strings.TrimSpace(databasePath), nil
		}
	}
	return "", errors.New("--database is required")
}

func corpusPassageChunkingStarted(databasePath string) (bool, error) {
	if _, err := os.Stat(databasePath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	database, err := sql.Open("sqlite3", "file:"+databasePath+"?mode=ro")
	if err != nil {
		return false, err
	}
	defer func() { _ = database.Close() }()
	var passageTableCount int
	if err := database.QueryRow(`select count(*) from sqlite_master where type = 'table' and name = 'searchable_passages'`).Scan(&passageTableCount); err != nil {
		return false, err
	}
	if passageTableCount == 0 {
		return false, nil
	}
	var passageCount int
	if err := database.QueryRow(`select count(*) from searchable_passages`).Scan(&passageCount); err != nil {
		return false, err
	}
	return passageCount > 0, nil
}

func projectSearchResearchCorpus(arguments []string, output io.Writer, progressOutput io.Writer) (returnedError error) {
	flags := flag.NewFlagSet("experiment corpus project", flag.ContinueOnError)
	stateRoot := flags.String("state-root", "", "frozen OpenTrawl state root")
	databasePath := flags.String("database", "", "private derived corpus database")
	pageRecordCount := flags.Uint("page-record-count", 10000, "records requested from each crawler page")
	maximumRecordsPerTrawler := flags.Uint64("maximum-records-per-trawler", 0, "bounded record count per trawler; zero exports all records")
	selectedTrawler := flags.String("trawler", "", "one trawler to export; empty exports all research trawlers")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if strings.TrimSpace(*stateRoot) == "" || strings.TrimSpace(*databasePath) == "" || *pageRecordCount == 0 {
		return errors.New("--state-root, --database and a non-zero --page-record-count are required")
	}
	if *selectedTrawler != "" && !isSearchResearchTrawler(registeredTrawlerName(*selectedTrawler)) {
		return fmt.Errorf("unknown research trawler %q", *selectedTrawler)
	}
	sourceSnapshot, err := calculateSearchResearchSourceSnapshot(
		*stateRoot,
		registeredTrawlerName(*selectedTrawler),
	)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*databasePath), 0o700); err != nil {
		return err
	}
	corpusDatabase, err := sql.Open("sqlite3", *databasePath)
	if err != nil {
		return err
	}
	defer func() { returnedError = errors.Join(returnedError, corpusDatabase.Close()) }()
	corpusDatabase.SetMaxOpenConns(1)
	if err := initializeSearchResearchCorpus(corpusDatabase); err != nil {
		return err
	}
	if err := os.Chmod(*databasePath, 0o600); err != nil {
		return err
	}
	if err := establishCorpusProjectionScope(corpusDatabase, *selectedTrawler, *maximumRecordsPerTrawler); err != nil {
		return err
	}
	if err := establishSearchResearchSourceSnapshot(corpusDatabase, sourceSnapshot); err != nil {
		return err
	}
	var existingPassageCount int64
	if err := corpusDatabase.QueryRow(`select count(*) from searchable_passages`).Scan(&existingPassageCount); err != nil {
		return err
	}
	if existingPassageCount != 0 {
		return errors.New("corpus projection is immutable after passage chunking starts")
	}
	runIdentifier, measurement, err := startExperimentRun(corpusDatabase, "corpus_projection", 0, 0)
	if err != nil {
		return err
	}
	memorySampler := startProcessMemorySampler(0)
	defer func() {
		memoryMeasurement := memorySampler.finish()
		measurement.baselineHarnessProcessMemoryBytes = memoryMeasurement.baselineHarnessProcessMemoryBytes
		measurement.baselineRuntimeProcessMemoryBytes = memoryMeasurement.baselineRuntimeProcessMemoryBytes
		measurement.peakProcessMemoryBytes = memoryMeasurement.peakHarnessAndRuntimeMemoryBytes
		measurement.steadyProcessMemoryBytes = memoryMeasurement.steadyHarnessAndRuntimeMemoryBytes
		measurement.indexBytes = sqliteDatabaseFileSetSize(*databasePath)
		if returnedError != nil {
			measurement.failureStage = corpusProjectionFailure
			measurement.failureMessage = returnedError.Error()
		}
		returnedError = errors.Join(returnedError, finishExperimentRun(corpusDatabase, runIdentifier, measurement))
	}()
	executor := trawlkit.NewTrawlerExecutor(trawlkit.TrawlerExecutorOptions{
		StateRoot: sourceSnapshot.stateRoot,
		Timeout:   10 * time.Minute,
		Stderr:    progressOutput,
	})
	for _, source := range searchableRecordExportTrawlers() {
		if *selectedTrawler != "" && string(source.registeredTrawler) != *selectedTrawler {
			continue
		}
		storageStartedAt := time.Now()
		err := exportTrawlerSearchableRecords(
			context.Background(), executor, corpusDatabase, source,
			uint32(*pageRecordCount), *maximumRecordsPerTrawler,
		)
		measurement.storageElapsed += time.Since(storageStartedAt)
		if err != nil {
			return fmt.Errorf("export %s searchable records: %w", source.registeredTrawler, err)
		}
	}
	projectionSHA256, err := calculateSourceProjectionSHA256(corpusDatabase)
	if err != nil {
		return err
	}
	if _, err := corpusDatabase.Exec(`update corpus_metadata set source_projection_sha256 = ? where singleton = 1`, projectionSHA256); err != nil {
		return err
	}
	currentSourceSnapshot, err := calculateSearchResearchSourceSnapshot(
		sourceSnapshot.stateRoot,
		sourceSnapshot.selectedTrawler,
	)
	if err != nil {
		return err
	}
	if currentSourceSnapshot.contentSHA256 != sourceSnapshot.contentSHA256 {
		return errors.New("source archive snapshot changed during corpus projection")
	}
	return writeCorpusMeasurement(corpusDatabase, *databasePath, output)
}

func chunkSearchResearchCorpus(arguments []string, output io.Writer) (returnedError error) {
	flags := flag.NewFlagSet("experiment corpus chunk", flag.ContinueOnError)
	databasePath := flags.String("database", "", "private derived corpus database")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if strings.TrimSpace(*databasePath) == "" {
		return errors.New("--database is required")
	}
	database, err := sql.Open("sqlite3", *databasePath)
	if err != nil {
		return err
	}
	defer func() { returnedError = errors.Join(returnedError, database.Close()) }()
	database.SetMaxOpenConns(1)
	if err := initializeSearchResearchCorpus(database); err != nil {
		return err
	}
	if _, err := verifySearchResearchSourceSnapshot(database); err != nil {
		return err
	}
	var projectionSHA256 string
	if err := database.QueryRow(`select source_projection_sha256 from corpus_metadata where singleton = 1`).Scan(&projectionSHA256); err != nil {
		return err
	}
	if projectionSHA256 == "" {
		return errors.New("corpus projection has not completed")
	}
	if err := confirmCorpusProjectionReachedItsScope(database); err != nil {
		return err
	}
	runIdentifier, measurement, err := startExperimentRun(database, "corpus_chunking", 0, 0)
	if err != nil {
		return err
	}
	memorySampler := startProcessMemorySampler(0)
	defer func() {
		memoryMeasurement := memorySampler.finish()
		measurement.baselineHarnessProcessMemoryBytes = memoryMeasurement.baselineHarnessProcessMemoryBytes
		measurement.baselineRuntimeProcessMemoryBytes = memoryMeasurement.baselineRuntimeProcessMemoryBytes
		measurement.peakProcessMemoryBytes = memoryMeasurement.peakHarnessAndRuntimeMemoryBytes
		measurement.steadyProcessMemoryBytes = memoryMeasurement.steadyHarnessAndRuntimeMemoryBytes
		measurement.indexBytes = sqliteDatabaseFileSetSize(*databasePath)
		if returnedError != nil {
			measurement.failureStage = corpusChunkingFailure
			measurement.failureMessage = returnedError.Error()
		}
		returnedError = errors.Join(returnedError, finishExperimentRun(database, runIdentifier, measurement))
	}()
	if err := chunkRemainingSearchableRecordTextSections(database, &measurement); err != nil {
		return err
	}
	passageSHA256, err := calculatePassageCorpusSHA256(database)
	if err != nil {
		return err
	}
	if _, err := database.Exec(`update corpus_metadata set passage_corpus_sha256 = ? where singleton = 1`, passageSHA256); err != nil {
		return err
	}
	return writeCorpusMeasurement(database, *databasePath, output)
}

func initializeSearchResearchCorpus(database *sql.DB) error {
	if _, err := database.Exec(fmt.Sprintf(`
		pragma journal_mode = wal;
		pragma synchronous = normal;
		create table if not exists corpus_metadata (
			singleton integer primary key check (singleton = 1),
			maximum_searchable_passage_content_utf8_bytes integer not null,
			source_projection_sha256 text not null,
			passage_corpus_sha256 text not null,
			source_archive_snapshot_state_root text not null,
			source_archive_snapshot_sha256 text not null,
			created_at text not null
		);
		insert or ignore into corpus_metadata values (1, %d, '', '', '', '', strftime('%%Y-%%m-%%dT%%H:%%M:%%fZ', 'now'));
		create table if not exists corpus_projection_scope (
			singleton integer primary key check (singleton = 1),
			selected_registered_trawler text not null,
			maximum_records_per_trawler integer not null
		);
		create table if not exists trawler_export_progress (
			registered_trawler text primary key,
			next_page_starts_after_canonical_record_reference text not null,
			exported_record_count integer not null,
			all_searchable_records_exported integer not null check (all_searchable_records_exported in (0, 1)),
			projection_bound_reached integer not null check (projection_bound_reached in (0, 1))
		);
		create table if not exists archive_records (
			archive_record_identifier integer primary key,
			registered_trawler text not null,
			canonical_archive_record_reference text not null unique,
			canonical_search_result_group_reference text not null,
			local_trawler_short_reference text not null,
			archive_record_kind_display_name text not null,
			archive_record_associated_unix_nanoseconds integer,
			search_match_presentation_protobuf blob not null
		);
		create table if not exists searchable_record_text_sections (
			searchable_record_text_section_identifier integer primary key,
			archive_record_identifier integer not null references archive_records(archive_record_identifier),
			searchable_record_text_section_number integer not null,
			searchable_record_text_section_name text not null,
			opened_record_anchor_identifier text not null,
			searchable_record_text_section_content text not null,
			searchable_record_text_section_content_sha256 blob not null,
			unique(archive_record_identifier, searchable_record_text_section_number)
		);
		create table if not exists searchable_passages (
			searchable_passage_identifier integer primary key,
			searchable_record_text_section_identifier integer not null references searchable_record_text_sections(searchable_record_text_section_identifier),
			section_passage_number integer not null,
			section_start_utf8_byte_offset integer not null,
			section_end_utf8_byte_offset integer not null,
			searchable_passage_content text not null,
			searchable_passage_content_sha256 blob not null,
			unique(searchable_record_text_section_identifier, section_passage_number)
		);
		create table if not exists corpus_chunking_progress (
			singleton integer primary key check (singleton = 1),
			last_chunked_searchable_record_text_section_identifier integer not null
		);
		insert or ignore into corpus_chunking_progress values (1, 0);
		create virtual table if not exists searchable_passages_fts using fts5(
			searchable_passage_content,
			content='searchable_passages',
			content_rowid='searchable_passage_identifier'
		);
		create trigger if not exists searchable_passages_after_insert after insert on searchable_passages begin
			insert into searchable_passages_fts(rowid, searchable_passage_content) values (new.searchable_passage_identifier, new.searchable_passage_content);
		end;`, maximumSearchablePassageContentUTF8Bytes)); err != nil {
		return err
	}
	if err := initializeExperimentRunSchema(database); err != nil {
		return err
	}
	var storedMaximumPassageBytes int
	if err := database.QueryRow(`select maximum_searchable_passage_content_utf8_bytes from corpus_metadata where singleton = 1`).Scan(&storedMaximumPassageBytes); err != nil {
		return err
	}
	if storedMaximumPassageBytes != maximumSearchablePassageContentUTF8Bytes {
		return errors.New("corpus uses a different searchable passage contract")
	}
	return nil
}

func establishCorpusProjectionScope(database *sql.DB, selectedTrawler string, maximumRecordsPerTrawler uint64) error {
	var existingSelectedTrawler string
	var existingMaximumRecords uint64
	err := database.QueryRow(`select selected_registered_trawler, maximum_records_per_trawler from corpus_projection_scope where singleton = 1`).Scan(&existingSelectedTrawler, &existingMaximumRecords)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = database.Exec(`insert into corpus_projection_scope values (1, ?, ?)`, selectedTrawler, maximumRecordsPerTrawler)
		return err
	}
	if err != nil {
		return err
	}
	if existingSelectedTrawler != selectedTrawler || existingMaximumRecords != maximumRecordsPerTrawler {
		return errors.New("existing corpus uses a different projection scope")
	}
	return nil
}

func confirmCorpusProjectionReachedItsScope(database *sql.DB) error {
	var selectedTrawler string
	if err := database.QueryRow(`select selected_registered_trawler from corpus_projection_scope where singleton = 1`).Scan(&selectedTrawler); err != nil {
		return err
	}
	neededTrawlers := 0
	completedTrawlers := 0
	for _, source := range searchableRecordExportTrawlers() {
		if selectedTrawler != "" && selectedTrawler != string(source.registeredTrawler) {
			continue
		}
		neededTrawlers++
		var allExported, boundReached bool
		if err := database.QueryRow(`select all_searchable_records_exported, projection_bound_reached from trawler_export_progress where registered_trawler = ?`, source.registeredTrawler).Scan(&allExported, &boundReached); err != nil {
			return fmt.Errorf("corpus projection for %s is incomplete", source.registeredTrawler)
		}
		if allExported || boundReached {
			completedTrawlers++
		}
	}
	if neededTrawlers == 0 || completedTrawlers != neededTrawlers {
		return errors.New("corpus projection has not reached its declared scope")
	}
	return nil
}

func exportTrawlerSearchableRecords(
	ctx context.Context,
	executor trawlkit.TrawlerExecutor,
	corpusDatabase *sql.DB,
	source searchableRecordExportTrawler,
	pageRecordCount uint32,
	maximumRecordsPerTrawler uint64,
) error {
	var recordsAfterCanonicalReferenceText string
	var exportedRecordCount uint64
	var allRecordsExported, projectionBoundReached bool
	err := corpusDatabase.QueryRow(`
		select next_page_starts_after_canonical_record_reference, exported_record_count,
		       all_searchable_records_exported, projection_bound_reached
		from trawler_export_progress where registered_trawler = ?`, source.registeredTrawler).Scan(
		&recordsAfterCanonicalReferenceText, &exportedRecordCount, &allRecordsExported, &projectionBoundReached,
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if allRecordsExported || projectionBoundReached {
		return nil
	}
	for {
		requestedPageRecordCount := pageRecordCount
		if maximumRecordsPerTrawler > 0 {
			remainingRecordCount := maximumRecordsPerTrawler - exportedRecordCount
			if remainingRecordCount < uint64(requestedPageRecordCount) {
				requestedPageRecordCount = uint32(remainingRecordCount)
			}
		}
		request := &searchablerecord.SearchableRecordExportRequest{MaximumRecordCount: requestedPageRecordCount}
		if recordsAfterCanonicalReferenceText != "" {
			request.RecordsAfterCanonicalRecordReference = trawlkit.NewCanonicalArchiveRecordReference(recordsAfterCanonicalReferenceText)
		}
		page, err := executor.ExportSearchableRecordPage(ctx, source.trawler, request)
		if err != nil {
			return err
		}
		exportedRecordCountAfterPage := exportedRecordCount + uint64(len(page.GetSearchableArchiveRecordsInCanonicalReferenceOrder()))
		projectionBoundReached = maximumRecordsPerTrawler > 0 && exportedRecordCountAfterPage >= maximumRecordsPerTrawler
		if err := storeSearchableRecordExportPage(corpusDatabase, source.registeredTrawler, page, exportedRecordCount, projectionBoundReached); err != nil {
			return err
		}
		exportedRecordCount = exportedRecordCountAfterPage
		recordsAfterCanonicalReferenceText = trawlkit.CanonicalArchiveRecordReferenceText(page.GetNextPageStartsAfterCanonicalRecordReference())
		allRecordsExported = page.GetAllSearchableRecordsExported()
		if allRecordsExported || projectionBoundReached || len(page.GetSearchableArchiveRecordsInCanonicalReferenceOrder()) == 0 {
			return nil
		}
	}
}

func storeSearchableRecordExportPage(
	database *sql.DB,
	registeredTrawler registeredTrawlerName,
	page *searchablerecord.TrawlerSearchableRecordExportPage,
	previouslyExportedRecordCount uint64,
	projectionBoundReached bool,
) error {
	transaction, err := database.Begin()
	if err != nil {
		return err
	}
	for _, record := range page.GetSearchableArchiveRecordsInCanonicalReferenceOrder() {
		projection, err := projectTypedSourceRecordForSearchIndex(registeredTrawler, record)
		if err != nil {
			_ = transaction.Rollback()
			return err
		}
		associatedUnixNanoseconds := archiveRecordAssociatedUnixNanoseconds(projection.associatedTime)
		searchPresentationProtobuf, err := proto.Marshal(projection.searchPresentation)
		if err != nil {
			_ = transaction.Rollback()
			return err
		}
		result, err := transaction.Exec(`
			insert into archive_records(
				registered_trawler, canonical_archive_record_reference,
				canonical_search_result_group_reference, local_trawler_short_reference,
				archive_record_kind_display_name, archive_record_associated_unix_nanoseconds,
				search_match_presentation_protobuf
			) values (?, ?, ?, ?, ?, ?, ?)`, registeredTrawler,
			trawlkit.CanonicalArchiveRecordReferenceText(record.GetCanonicalRecordReference()),
			trawlkit.CanonicalArchiveRecordReferenceText(record.GetCanonicalSearchResultGroupReference()),
			trawlkit.LocalTrawlerShortReferenceText(record.GetLocalShortReference()),
			projection.recordKindDisplayName, associatedUnixNanoseconds,
			searchPresentationProtobuf)
		if err != nil {
			_ = transaction.Rollback()
			return err
		}
		archiveRecordIdentifier, err := result.LastInsertId()
		if err != nil {
			_ = transaction.Rollback()
			return err
		}
		for sectionNumber, section := range projection.textSections {
			sectionContent := strings.TrimSpace(section.content)
			sectionSHA256 := sha256.Sum256([]byte(sectionContent))
			if _, err := transaction.Exec(`
				insert into searchable_record_text_sections(
					archive_record_identifier, searchable_record_text_section_number,
					searchable_record_text_section_name, opened_record_anchor_identifier,
					searchable_record_text_section_content,
					searchable_record_text_section_content_sha256
				) values (?, ?, ?, ?, ?, ?)`, archiveRecordIdentifier, sectionNumber,
				section.displayName,
				trawlkit.RecordAnchorIdentifierText(section.recordAnchor),
				sectionContent, sectionSHA256[:]); err != nil {
				_ = transaction.Rollback()
				return err
			}
		}
	}
	nextReference := trawlkit.CanonicalArchiveRecordReferenceText(page.GetNextPageStartsAfterCanonicalRecordReference())
	_, err = transaction.Exec(`
		insert into trawler_export_progress(
			registered_trawler, next_page_starts_after_canonical_record_reference,
			exported_record_count, all_searchable_records_exported, projection_bound_reached
		) values (?, ?, ?, ?, ?)
		on conflict(registered_trawler) do update set
			next_page_starts_after_canonical_record_reference = excluded.next_page_starts_after_canonical_record_reference,
			exported_record_count = excluded.exported_record_count,
			all_searchable_records_exported = excluded.all_searchable_records_exported,
			projection_bound_reached = excluded.projection_bound_reached`,
		registeredTrawler, nextReference,
		previouslyExportedRecordCount+uint64(len(page.GetSearchableArchiveRecordsInCanonicalReferenceOrder())),
		page.GetAllSearchableRecordsExported(), projectionBoundReached)
	if err != nil {
		_ = transaction.Rollback()
		return err
	}
	return transaction.Commit()
}

func archiveRecordAssociatedUnixNanoseconds(
	associatedTime *presentation.ArchiveRecordAssociatedTimeForDisplay,
) any {
	if associatedTime == nil {
		return nil
	}
	switch typedTime := associatedTime.GetArchiveRecordAssociatedTime().(type) {
	case *presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime:
		if typedTime.ExactTime != nil && typedTime.ExactTime.IsValid() {
			return typedTime.ExactTime.AsTime().UnixNano()
		}
	case *presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate:
		if typedTime.CalendarDate != nil {
			return time.Date(
				int(typedTime.CalendarDate.GetCalendarYear()),
				time.Month(typedTime.CalendarDate.GetCalendarMonthNumber()),
				int(typedTime.CalendarDate.GetCalendarDayOfMonth()),
				0, 0, 0, 0, time.Local,
			).UnixNano()
		}
	}
	return nil
}

func chunkRemainingSearchableRecordTextSections(database *sql.DB, measurement *experimentRunMeasurement) error {
	var lastChunkedSectionIdentifier int64
	if err := database.QueryRow(`select last_chunked_searchable_record_text_section_identifier from corpus_chunking_progress where singleton = 1`).Scan(&lastChunkedSectionIdentifier); err != nil {
		return err
	}
	for {
		var sectionIdentifier int64
		var sectionContent string
		err := database.QueryRow(`
			select searchable_record_text_section_identifier, searchable_record_text_section_content
			from searchable_record_text_sections
			where searchable_record_text_section_identifier > ?
			order by searchable_record_text_section_identifier limit 1`, lastChunkedSectionIdentifier).Scan(&sectionIdentifier, &sectionContent)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		passages := splitSearchableRecordTextSectionIntoPassages(sectionContent)
		storageStartedAt := time.Now()
		transaction, err := database.Begin()
		if err != nil {
			return err
		}
		for passageNumber, passage := range passages {
			passageSHA256 := sha256.Sum256([]byte(passage.content))
			if _, err := transaction.Exec(`
				insert into searchable_passages(
					searchable_record_text_section_identifier, section_passage_number,
					section_start_utf8_byte_offset, section_end_utf8_byte_offset,
					searchable_passage_content, searchable_passage_content_sha256
				) values (?, ?, ?, ?, ?, ?)`, sectionIdentifier, passageNumber,
				passage.sectionStartUTF8ByteOffset, passage.sectionEndUTF8ByteOffset,
				passage.content, passageSHA256[:]); err != nil {
				_ = transaction.Rollback()
				return err
			}
		}
		if _, err := transaction.Exec(`update corpus_chunking_progress set last_chunked_searchable_record_text_section_identifier = ? where singleton = 1`, sectionIdentifier); err != nil {
			_ = transaction.Rollback()
			return err
		}
		if err := transaction.Commit(); err != nil {
			return err
		}
		measurement.storageElapsed += time.Since(storageStartedAt)
		lastChunkedSectionIdentifier = sectionIdentifier
	}
}

func calculateSourceProjectionSHA256(database *sql.DB) (corpusContentSHA256, error) {
	rows, err := database.Query(`
		select record.registered_trawler, record.canonical_archive_record_reference,
		       record.canonical_search_result_group_reference,
		       record.local_trawler_short_reference, record.archive_record_kind_display_name,
		       coalesce(record.archive_record_associated_unix_nanoseconds, 0),
		       hex(record.search_match_presentation_protobuf),
		       section.searchable_record_text_section_number,
		       section.searchable_record_text_section_name,
		       section.opened_record_anchor_identifier,
		       section.searchable_record_text_section_content
		from archive_records record
		join searchable_record_text_sections section using (archive_record_identifier)
		order by record.registered_trawler, record.canonical_archive_record_reference,
		         section.searchable_record_text_section_number`)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	hash := sha256.New()
	for rows.Next() {
		var trawler, reference, searchResultGroupReference, shortReference, kind, presentationHex, sectionName, openedRecordAnchorIdentifier, sectionContent string
		var associatedTime, sectionNumber int64
		if err := rows.Scan(&trawler, &reference, &searchResultGroupReference, &shortReference, &kind, &associatedTime, &presentationHex, &sectionNumber, &sectionName, &openedRecordAnchorIdentifier, &sectionContent); err != nil {
			return "", err
		}
		writeLengthDelimitedHashFields(hash, trawler, reference, searchResultGroupReference, shortReference, kind, fmt.Sprint(associatedTime), presentationHex, fmt.Sprint(sectionNumber), sectionName, openedRecordAnchorIdentifier, sectionContent)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return corpusContentSHA256(hex.EncodeToString(hash.Sum(nil))), nil
}

func calculatePassageCorpusSHA256(database *sql.DB) (corpusContentSHA256, error) {
	rows, err := database.Query(`
		select record.registered_trawler, record.canonical_archive_record_reference,
		       record.canonical_search_result_group_reference,
		       section.searchable_record_text_section_number, section.searchable_record_text_section_name,
		       section.opened_record_anchor_identifier,
		       passage.section_passage_number, passage.section_start_utf8_byte_offset,
		       passage.section_end_utf8_byte_offset, passage.searchable_passage_content
		from searchable_passages passage
		join searchable_record_text_sections section using (searchable_record_text_section_identifier)
		join archive_records record using (archive_record_identifier)
		order by record.registered_trawler, record.canonical_archive_record_reference,
		         section.searchable_record_text_section_number, passage.section_passage_number`)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	hash := sha256.New()
	for rows.Next() {
		var trawler, reference, searchResultGroupReference, sectionName, openedRecordAnchorIdentifier, content string
		var sectionNumber, passageNumber, passageStartOffset, passageEndOffset int64
		if err := rows.Scan(&trawler, &reference, &searchResultGroupReference, &sectionNumber, &sectionName, &openedRecordAnchorIdentifier, &passageNumber, &passageStartOffset, &passageEndOffset, &content); err != nil {
			return "", err
		}
		writeLengthDelimitedHashFields(hash, trawler, reference, searchResultGroupReference, fmt.Sprint(sectionNumber), sectionName, openedRecordAnchorIdentifier,
			fmt.Sprint(passageNumber), fmt.Sprint(passageStartOffset), fmt.Sprint(passageEndOffset), content)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return corpusContentSHA256(hex.EncodeToString(hash.Sum(nil))), nil
}

func writeLengthDelimitedHashFields(writer io.Writer, fields ...string) {
	for _, field := range fields {
		_, _ = fmt.Fprintf(writer, "%d:%s", len(field), field)
	}
}

func writeCorpusMeasurement(database *sql.DB, databasePath string, output io.Writer) error {
	var recordCount, sectionCount, sourceUTF8Bytes, passageCount, passageUTF8Bytes int64
	var projectionSHA256, passageSHA256 string
	if err := database.QueryRow(`select count(*) from archive_records`).Scan(&recordCount); err != nil {
		return err
	}
	if err := database.QueryRow(`select count(*), coalesce(sum(length(cast(searchable_record_text_section_content as blob))), 0) from searchable_record_text_sections`).Scan(&sectionCount, &sourceUTF8Bytes); err != nil {
		return err
	}
	if err := database.QueryRow(`select count(*), coalesce(sum(length(cast(searchable_passage_content as blob))), 0) from searchable_passages`).Scan(&passageCount, &passageUTF8Bytes); err != nil {
		return err
	}
	if err := database.QueryRow(`select source_projection_sha256, passage_corpus_sha256 from corpus_metadata where singleton = 1`).Scan(&projectionSHA256, &passageSHA256); err != nil {
		return err
	}
	_, err := fmt.Fprintf(output, "records=%d sections=%d source_utf8_bytes=%d passages=%d passage_utf8_bytes=%d corpus_database_bytes=%d source_projection_sha256=%s passage_corpus_sha256=%s\n", recordCount, sectionCount, sourceUTF8Bytes, passageCount, passageUTF8Bytes, sqliteDatabaseFileSetSize(databasePath), projectionSHA256, passageSHA256)
	return err
}

func isSearchResearchTrawler(candidate registeredTrawlerName) bool {
	for _, source := range searchableRecordExportTrawlers() {
		if source.registeredTrawler == candidate {
			return true
		}
	}
	return false
}
