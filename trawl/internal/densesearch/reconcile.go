package densesearch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	searchablerecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/searchable_record"
	"google.golang.org/protobuf/proto"
)

type projectedRecordForIndex struct {
	record                         *searchablerecord.SearchableArchiveRecord
	projection                     searchableRecordProjection
	searchPresentationProtobuf     []byte
	passages                       []projectedPassageForIndex
	associatedUnixNanosecondsOrNil any
}

type projectedPassageForIndex struct {
	indexedPassage
	embeddingInputFingerprint [sha256.Size]byte
	embeddingInput            string
}

func ReconcileIndex(ctx context.Context, options BuildIndexOptions) error {
	if strings.TrimSpace(options.StateRoot) == "" || options.WithSourceArchivesLocked == nil {
		return errors.New("semantic search reconciliation needs a state root and archive lock")
	}
	if options.IndexPath == "" {
		options.IndexPath = IndexPath(options.StateRoot)
	}
	if options.ProgressOutput == nil {
		options.ProgressOutput = io.Discard
	}
	registeredTrawlers := make([]string, 0, len(options.Sources))
	sourcesByRegisteredTrawler := make(map[string]SearchableRecordSource, len(options.Sources))
	for _, source := range options.Sources {
		registeredTrawler := trawlkit.RegisteredTrawlerIdentityText(source.RegisteredTrawlerIdentity)
		if registeredTrawler == "" || source.ExportPage == nil {
			return errors.New("searchable source identity and exporter are required")
		}
		registeredTrawlers = append(registeredTrawlers, registeredTrawler)
		sourcesByRegisteredTrawler[registeredTrawler] = source
	}
	if err := EnsureSourcesHaveRequiredGenerations(ctx, options.StateRoot, registeredTrawlers); err != nil {
		return err
	}
	lock, err := AcquireIndexBuildLock(options.StateRoot)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	for {
		var targetGenerations []SourceRequiredGeneration
		publishedWasAlreadyCurrent := false
		err := options.WithSourceArchivesLocked(func() error {
			var err error
			targetGenerations, err = RequiredSourceGenerations(ctx, options.StateRoot, registeredTrawlers)
			if err != nil {
				return err
			}
			publishedWasAlreadyCurrent, err = publishedIndexMatchesTarget(options.IndexPath, targetGenerations)
			if err != nil || publishedWasAlreadyCurrent {
				return err
			}
			return snapshotIndexGeneration(ctx, options, targetGenerations, sourcesByRegisteredTrawler)
		})
		if err != nil {
			return err
		}
		if publishedWasAlreadyCurrent {
			return nil
		}
		if err := embedMissingStagingPassages(ctx, BuildingIndexPath(options.StateRoot)); err != nil {
			return err
		}
		publishedCurrent := false
		if err := options.WithSourceArchivesLocked(func() error {
			latestGenerations, err := RequiredSourceGenerations(ctx, options.StateRoot, registeredTrawlers)
			if err != nil {
				return err
			}
			if !sameSourceGenerations(targetGenerations, latestGenerations) {
				return nil
			}
			if err := publishStagingIndex(options.IndexPath, BuildingIndexPath(options.StateRoot), targetGenerations); err != nil {
				return err
			}
			publishedCurrent = true
			return nil
		}); err != nil {
			return err
		}
		if publishedCurrent {
			return nil
		}
	}
}

func snapshotIndexGeneration(
	ctx context.Context,
	options BuildIndexOptions,
	targetGenerations []SourceRequiredGeneration,
	sourcesByRegisteredTrawler map[string]SearchableRecordSource,
) (returnedError error) {
	stagingPath := BuildingIndexPath(options.StateRoot)
	if err := prepareStagingIndex(options.IndexPath, stagingPath); err != nil {
		return err
	}
	staging, err := sql.Open("sqlite3", stagingPath+"?_busy_timeout=5000")
	if err != nil {
		return err
	}
	staging.SetMaxOpenConns(1)
	defer func() {
		if staging != nil {
			returnedError = errors.Join(returnedError, staging.Close())
		}
	}()
	if err := verifyIndexContract(staging, false, true); err != nil {
		return err
	}
	published := openReusablePublishedIndex(options.IndexPath)
	if published != nil {
		defer func() { returnedError = errors.Join(returnedError, published.Close()) }()
	}
	for _, target := range targetGenerations {
		source := sourcesByRegisteredTrawler[target.RegisteredTrawler]
		recordCount, passageCount, err := reconcileSearchableRecordSource(
			ctx,
			staging,
			published,
			source,
			target,
		)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(
			options.ProgressOutput,
			"indexed_source=%s records=%d passages=%d\n",
			target.RegisteredTrawler,
			recordCount,
			passageCount,
		)
	}
	return removeAbsentSearchableSources(ctx, staging, targetGenerations)
}

func publishedIndexMatchesTarget(path string, target []SourceRequiredGeneration) (bool, error) {
	published := openCurrentPublishedIndex(path)
	if published == nil {
		return false, nil
	}
	defer func() { _ = published.Close() }()
	indexed, err := completeSourceGenerations(published)
	if err != nil {
		return false, err
	}
	return sameSourceGenerations(indexed, target), nil
}

func embedMissingStagingPassages(ctx context.Context, stagingPath string) (returnedError error) {
	staging, err := sql.Open("sqlite3", stagingPath+"?_busy_timeout=5000")
	if err != nil {
		return err
	}
	staging.SetMaxOpenConns(1)
	defer func() { returnedError = errors.Join(returnedError, staging.Close()) }()
	if err := verifyIndexContract(staging, false, true); err != nil {
		return err
	}
	modelVerified := false
	for {
		rows, err := staging.QueryContext(ctx, `
			select searchable_passage_identifier, searchable_text_section_display_name,
			       searchable_passage_content, embedding_input_fingerprint
			from searchable_passages
			where embedding_float32_blob is null
			order by searchable_passage_identifier
			limit ?`, maximumEmbeddingRequestPassageCount)
		if err != nil {
			return err
		}
		var passageIdentifiers []int64
		var embeddingInputs []string
		for rows.Next() {
			var passageIdentifier int64
			var sectionDisplayName, passageContent string
			var storedFingerprint []byte
			if err := rows.Scan(&passageIdentifier, &sectionDisplayName, &passageContent, &storedFingerprint); err != nil {
				_ = rows.Close()
				return err
			}
			embeddingInput := embeddingGemmaDocumentPrefix + sectionDisplayName + ":\n" + passageContent
			fingerprint := embeddingInputFingerprint(embeddingInput)
			if !bytes.Equal(storedFingerprint, fingerprint[:]) {
				_ = rows.Close()
				return errors.New("staged embedding input fingerprint does not match its passage")
			}
			passageIdentifiers = append(passageIdentifiers, passageIdentifier)
			embeddingInputs = append(embeddingInputs, embeddingInput)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(passageIdentifiers) == 0 {
			return nil
		}
		if !modelVerified {
			if err := verifyEmbeddingGemmaModel(ctx); err != nil {
				return err
			}
			modelVerified = true
		}
		embeddings, err := requestStoredEmbeddings(ctx, embeddingInputs)
		if err != nil {
			return err
		}
		transaction, err := staging.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for index, passageIdentifier := range passageIdentifiers {
			if _, err := transaction.ExecContext(ctx, `
				update searchable_passages set embedding_float32_blob = ?
				where searchable_passage_identifier = ? and embedding_float32_blob is null`,
				encodeFloat32Embedding(embeddings[index]), passageIdentifier); err != nil {
				_ = transaction.Rollback()
				return err
			}
		}
		if err := transaction.Commit(); err != nil {
			return err
		}
	}
}

func publishStagingIndex(
	publishedPath string,
	stagingPath string,
	target []SourceRequiredGeneration,
) (returnedError error) {
	staging, err := sql.Open("sqlite3", stagingPath+"?_busy_timeout=5000")
	if err != nil {
		return err
	}
	staging.SetMaxOpenConns(1)
	defer func() {
		if staging != nil {
			returnedError = errors.Join(returnedError, staging.Close())
		}
	}()
	if err := verifyIndexContract(staging, false, true); err != nil {
		return err
	}
	indexed, err := completeSourceGenerations(staging)
	if err != nil {
		return err
	}
	if !sameSourceGenerations(indexed, target) {
		return errors.New("staged source generations are incomplete")
	}
	var missingEmbeddingCount uint64
	if err := staging.QueryRow(`select count(*) from searchable_passages where embedding_float32_blob is null`).Scan(&missingEmbeddingCount); err != nil {
		return err
	}
	if missingEmbeddingCount != 0 {
		return errors.New("staged semantic search index has missing embeddings")
	}
	if _, err := staging.Exec(`
		delete from reusable_passage_embeddings;
		update dense_search_index set complete = 1 where singleton = 1`); err != nil {
		return err
	}
	if err := staging.Close(); err != nil {
		staging = nil
		return err
	}
	staging = nil
	if err := syncFile(stagingPath); err != nil {
		return err
	}
	if err := os.Rename(stagingPath, publishedPath); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(publishedPath))
}

func completeSourceGenerations(database *sql.DB) ([]SourceRequiredGeneration, error) {
	rows, err := database.Query(`
		select registered_trawler, required_generation
		from searchable_source_generations
		where all_source_records_indexed = 1
		order by registered_trawler`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var generations []SourceRequiredGeneration
	for rows.Next() {
		var generation SourceRequiredGeneration
		var requiredGeneration uint64
		if err := rows.Scan(&generation.RegisteredTrawler, &requiredGeneration); err != nil {
			return nil, err
		}
		generation.RequiredGeneration = SourceGeneration(requiredGeneration)
		generations = append(generations, generation)
	}
	return generations, rows.Err()
}

func removeAbsentSearchableSources(
	ctx context.Context,
	staging *sql.DB,
	target []SourceRequiredGeneration,
) (returnedError error) {
	transaction, err := staging.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if returnedError != nil {
			_ = transaction.Rollback()
		}
	}()
	if len(target) == 0 {
		if _, err := transaction.ExecContext(ctx, `
			delete from searchable_passages;
			delete from searchable_records;
			delete from searchable_source_generations`); err != nil {
			return err
		}
		return transaction.Commit()
	}
	placeholders := make([]string, len(target))
	arguments := make([]any, len(target))
	for index, source := range target {
		placeholders[index] = "?"
		arguments[index] = source.RegisteredTrawler
	}
	notCurrentSource := `not in (` + strings.Join(placeholders, ",") + `)`
	if _, err := transaction.ExecContext(ctx, `
		delete from searchable_passages where searchable_record_identifier in (
			select searchable_record_identifier from searchable_records where registered_trawler `+notCurrentSource+`
		)`, arguments...); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `delete from searchable_records where registered_trawler `+notCurrentSource, arguments...); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `delete from searchable_source_generations where registered_trawler `+notCurrentSource, arguments...); err != nil {
		return err
	}
	return transaction.Commit()
}

func prepareStagingIndex(publishedPath, stagingPath string) error {
	if staging, err := sql.Open("sqlite3", "file:"+stagingPath+"?mode=rw"); err == nil {
		verificationError := verifyIndexContract(staging, false, true)
		_ = staging.Close()
		if verificationError == nil {
			return nil
		}
		if err := os.Remove(stagingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if published := openCurrentPublishedIndex(publishedPath); published != nil {
		_ = published.Close()
		if err := copyFile(publishedPath, stagingPath); err != nil {
			return err
		}
		staging, err := sql.Open("sqlite3", stagingPath)
		if err != nil {
			return err
		}
		if _, err := staging.Exec(`update dense_search_index set complete = 0 where singleton = 1`); err != nil {
			_ = staging.Close()
			return err
		}
		return staging.Close()
	}
	if err := os.MkdirAll(filepath.Dir(stagingPath), 0o700); err != nil {
		return err
	}
	staging, err := sql.Open("sqlite3", stagingPath)
	if err != nil {
		return err
	}
	if err := initializeIndex(staging); err != nil {
		_ = staging.Close()
		return err
	}
	if err := staging.Close(); err != nil {
		return err
	}
	return os.Chmod(stagingPath, 0o600)
}

func openReusablePublishedIndex(path string) *sql.DB {
	database, err := sql.Open("sqlite3", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		return nil
	}
	database.SetMaxOpenConns(1)
	if err := verifyIndexContract(database, true, false); err != nil {
		_ = database.Close()
		return nil
	}
	return database
}

func openCurrentPublishedIndex(path string) *sql.DB {
	database := openReusablePublishedIndex(path)
	if database == nil {
		return nil
	}
	if err := verifyIndexContract(database, true, true); err != nil {
		_ = database.Close()
		return nil
	}
	return database
}

func reconcileSearchableRecordSource(
	ctx context.Context,
	staging *sql.DB,
	published *sql.DB,
	source SearchableRecordSource,
	target SourceRequiredGeneration,
) (recordCount uint64, passageCount uint64, returnedError error) {
	indexedGeneration, allIndexed, nextReference, found, err := stagingSourceProgress(staging, target.RegisteredTrawler)
	if err != nil {
		return 0, 0, err
	}
	if found && indexedGeneration == target.RequiredGeneration && allIndexed {
		return sourceIndexCounts(staging, target.RegisteredTrawler)
	}
	if !found || indexedGeneration != target.RequiredGeneration {
		if err := resetStagingSource(ctx, staging, target); err != nil {
			return 0, 0, err
		}
		nextReference = ""
	}
	if nextReference != "" {
		recordCount, passageCount, err = sourceIndexCounts(staging, target.RegisteredTrawler)
		if err != nil {
			return 0, 0, err
		}
	}
	var recordsAfterCanonicalReference = trawlkit.NewCanonicalArchiveRecordReference(nextReference)
	if nextReference == "" {
		recordsAfterCanonicalReference = nil
	}
	for {
		pageMaximum := uint32(searchableRecordExportPageSize)
		page, err := source.ExportPage(ctx, &searchablerecord.SearchableRecordExportRequest{
			RecordsAfterCanonicalRecordReference: recordsAfterCanonicalReference,
			MaximumRecordCount:                   pageMaximum,
		})
		if err != nil {
			return recordCount, passageCount, err
		}
		if page == nil {
			return recordCount, passageCount, errors.New("searchable record exporter returned no page")
		}
		pageNextReference := trawlkit.CanonicalArchiveRecordReferenceText(page.GetNextPageStartsAfterCanonicalRecordReference())
		pageRecordCount, pagePassageCount, err := reconcileSearchableRecordPage(
			ctx,
			staging,
			published,
			target.RegisteredTrawler,
			source.RegisteredTrawlerDisplayName,
			page,
			pageNextReference,
		)
		if err != nil {
			return recordCount, passageCount, err
		}
		recordCount += pageRecordCount
		passageCount += pagePassageCount
		if page.GetAllSearchableRecordsExported() || len(page.GetSearchableArchiveRecordsInCanonicalReferenceOrder()) == 0 {
			if err := markStagingSourceComplete(ctx, staging, target.RegisteredTrawler); err != nil {
				return recordCount, passageCount, err
			}
			return recordCount, passageCount, nil
		}
		if pageNextReference == "" {
			return recordCount, passageCount, errors.New("searchable record export page has no next-page reference")
		}
		recordsAfterCanonicalReference = page.GetNextPageStartsAfterCanonicalRecordReference()
	}
}

func reconcileSearchableRecordPage(
	ctx context.Context,
	staging *sql.DB,
	published *sql.DB,
	registeredTrawler string,
	registeredTrawlerDisplayName string,
	page *searchablerecord.TrawlerSearchableRecordExportPage,
	nextPageReference string,
) (recordCount uint64, passageCount uint64, returnedError error) {
	projectedRecords, err := projectSearchableRecordPage(registeredTrawler, registeredTrawlerDisplayName, page)
	if err != nil {
		return 0, 0, err
	}
	reusableEmbeddings, err := reusableEmbeddingsForProjectedRecords(
		staging,
		published,
		registeredTrawler,
		projectedRecords,
	)
	if err != nil {
		return 0, 0, err
	}
	transaction, err := staging.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		if returnedError != nil {
			_ = transaction.Rollback()
		}
	}()
	for _, projectedRecord := range projectedRecords {
		recordInsertion, err := transaction.ExecContext(ctx, `
			insert into searchable_records(
				registered_trawler, canonical_record_reference,
				canonical_search_result_group_reference, local_trawler_short_reference,
				archive_record_associated_unix_nanoseconds, search_match_presentation_protobuf
			) values (?, ?, ?, ?, ?, ?)`,
			registeredTrawler,
			trawlkit.CanonicalArchiveRecordReferenceText(projectedRecord.record.GetCanonicalRecordReference()),
			trawlkit.CanonicalArchiveRecordReferenceText(projectedRecord.record.GetCanonicalSearchResultGroupReference()),
			trawlkit.LocalTrawlerShortReferenceText(projectedRecord.record.GetLocalShortReference()),
			projectedRecord.associatedUnixNanosecondsOrNil,
			projectedRecord.searchPresentationProtobuf,
		)
		if err != nil {
			return 0, 0, err
		}
		recordIdentifier, err := recordInsertion.LastInsertId()
		if err != nil {
			return 0, 0, err
		}
		for _, passage := range projectedRecord.passages {
			encodedEmbedding := reusableEmbeddings[passage.embeddingInputFingerprint]
			if _, err := transaction.ExecContext(ctx, `
				insert into searchable_passages(
					searchable_record_identifier, searchable_text_section_display_name,
					record_anchor_identifier, section_start_utf8_byte_offset,
					section_end_utf8_byte_offset, searchable_passage_content,
					embedding_input_fingerprint, embedding_float32_blob
				) values (?, ?, ?, ?, ?, ?, ?, ?)`,
				recordIdentifier,
				passage.searchableTextSectionDisplayName,
				passage.recordAnchorIdentifier,
				passage.sectionStartUTF8ByteOffset,
				passage.sectionEndUTF8ByteOffset,
				passage.content,
				passage.embeddingInputFingerprint[:],
				encodedEmbedding,
			); err != nil {
				return 0, 0, err
			}
			passageCount++
		}
		recordCount++
	}
	if _, err := transaction.ExecContext(ctx, `
		update searchable_source_generations
		set next_page_starts_after_canonical_record_reference = ?
		where registered_trawler = ?`, nextPageReference, registeredTrawler); err != nil {
		return 0, 0, err
	}
	return recordCount, passageCount, transaction.Commit()
}

func projectSearchableRecordPage(
	registeredTrawler string,
	registeredTrawlerDisplayName string,
	page *searchablerecord.TrawlerSearchableRecordExportPage,
) ([]projectedRecordForIndex, error) {
	projectedRecords := make([]projectedRecordForIndex, 0, len(page.GetSearchableArchiveRecordsInCanonicalReferenceOrder()))
	for _, record := range page.GetSearchableArchiveRecordsInCanonicalReferenceOrder() {
		projection, err := projectTypedSourceRecord(registeredTrawlerDisplayName, record)
		if err != nil {
			return nil, err
		}
		presentationProtobuf, err := proto.MarshalOptions{Deterministic: true}.Marshal(projection.searchPresentation)
		if err != nil {
			return nil, err
		}
		projectedRecord := projectedRecordForIndex{
			record:                     record,
			projection:                 projection,
			searchPresentationProtobuf: presentationProtobuf,
		}
		if exactTime := projection.associatedTime.GetExactTime(); exactTime != nil && exactTime.IsValid() {
			projectedRecord.associatedUnixNanosecondsOrNil = exactTime.AsTime().UnixNano()
		}
		for _, section := range projection.textSections {
			for _, passage := range splitSearchableTextSectionIntoPassages(section.content) {
				formattedPassage := section.displayName + ":\n" + passage.content
				indexed := indexedPassage{
					searchableTextSectionDisplayName: section.displayName,
					recordAnchorIdentifier:           trawlkit.RecordAnchorIdentifierText(section.recordAnchor),
					sectionStartUTF8ByteOffset:       int64(passage.sectionStartUTF8ByteOffset),
					sectionEndUTF8ByteOffset:         int64(passage.sectionEndUTF8ByteOffset),
					content:                          passage.content,
				}
				projectedRecord.passages = append(projectedRecord.passages, projectedPassageForIndex{
					indexedPassage:            indexed,
					embeddingInputFingerprint: embeddingInputFingerprint(embeddingGemmaDocumentPrefix + formattedPassage),
					embeddingInput:            embeddingGemmaDocumentPrefix + formattedPassage,
				})
			}
		}
		if len(projectedRecord.passages) > 0 {
			projectedRecords = append(projectedRecords, projectedRecord)
		}
	}
	return projectedRecords, nil
}

func embeddingInputFingerprint(embeddingInput string) [sha256.Size]byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte(embeddingGemmaModelDigest))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(embeddingInput))
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], hash.Sum(nil))
	return fingerprint
}

func reusableEmbeddingsForProjectedRecords(
	staging *sql.DB,
	published *sql.DB,
	registeredTrawler string,
	projectedRecords []projectedRecordForIndex,
) (map[[sha256.Size]byte][]byte, error) {
	reusable := make(map[[sha256.Size]byte][]byte)
	if len(projectedRecords) == 0 {
		return reusable, nil
	}
	projectedFingerprints := make([][sha256.Size]byte, 0)
	for _, projectedRecord := range projectedRecords {
		for _, passage := range projectedRecord.passages {
			projectedFingerprints = append(projectedFingerprints, passage.embeddingInputFingerprint)
		}
	}
	for batchStart := 0; batchStart < len(projectedFingerprints); batchStart += 500 {
		batchEnd := min(batchStart+500, len(projectedFingerprints))
		placeholders := make([]string, batchEnd-batchStart)
		arguments := make([]any, batchEnd-batchStart)
		for index, fingerprint := range projectedFingerprints[batchStart:batchEnd] {
			placeholders[index] = "?"
			arguments[index] = fingerprint[:]
		}
		rows, err := staging.Query(`
			select embedding_input_fingerprint, embedding_float32_blob
			from reusable_passage_embeddings
			where embedding_input_fingerprint in (`+strings.Join(placeholders, ",")+`)`, arguments...)
		if err != nil {
			return nil, err
		}
		if err := collectReusableEmbeddings(rows, reusable); err != nil {
			return nil, err
		}
	}
	if published == nil {
		return reusable, nil
	}
	placeholders := make([]string, len(projectedRecords))
	arguments := make([]any, 0, len(projectedRecords)+1)
	arguments = append(arguments, registeredTrawler)
	for index, projectedRecord := range projectedRecords {
		placeholders[index] = "?"
		arguments = append(arguments, trawlkit.CanonicalArchiveRecordReferenceText(projectedRecord.record.GetCanonicalRecordReference()))
	}
	rows, err := published.Query(`
		select passage.embedding_input_fingerprint, passage.embedding_float32_blob
		from searchable_passages passage
		join searchable_records record using(searchable_record_identifier)
		where record.registered_trawler = ?
		  and record.canonical_record_reference in (`+strings.Join(placeholders, ",")+`)`, arguments...)
	if err != nil {
		return nil, err
	}
	if err := collectReusableEmbeddings(rows, reusable); err != nil {
		return nil, err
	}
	return reusable, nil
}

func collectReusableEmbeddings(
	rows *sql.Rows,
	reusable map[[sha256.Size]byte][]byte,
) error {
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var rawFingerprint, embedding []byte
		if err := rows.Scan(&rawFingerprint, &embedding); err != nil {
			return err
		}
		if len(rawFingerprint) != sha256.Size {
			return errors.New("reusable searchable passage fingerprint is invalid")
		}
		var fingerprint [sha256.Size]byte
		copy(fingerprint[:], rawFingerprint)
		reusable[fingerprint] = append([]byte(nil), embedding...)
	}
	return rows.Err()
}

func stagingSourceProgress(
	staging *sql.DB,
	registeredTrawler string,
) (SourceGeneration, bool, string, bool, error) {
	var generation uint64
	var allIndexed int
	var nextReference string
	err := staging.QueryRow(`
		select required_generation, all_source_records_indexed,
		       next_page_starts_after_canonical_record_reference
		from searchable_source_generations where registered_trawler = ?`, registeredTrawler).
		Scan(&generation, &allIndexed, &nextReference)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, "", false, nil
	}
	return SourceGeneration(generation), allIndexed == 1, nextReference, err == nil, err
}

func resetStagingSource(
	ctx context.Context,
	staging *sql.DB,
	target SourceRequiredGeneration,
) (returnedError error) {
	transaction, err := staging.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if returnedError != nil {
			_ = transaction.Rollback()
		}
	}()
	if _, err := transaction.ExecContext(ctx, `
		insert or ignore into reusable_passage_embeddings(
			embedding_input_fingerprint, embedding_float32_blob
		)
		select passage.embedding_input_fingerprint, passage.embedding_float32_blob
		from searchable_passages passage
		join searchable_records record using(searchable_record_identifier)
		where record.registered_trawler = ? and passage.embedding_float32_blob is not null`, target.RegisteredTrawler); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		delete from searchable_passages
		where searchable_record_identifier in (
			select searchable_record_identifier from searchable_records where registered_trawler = ?
		)`, target.RegisteredTrawler); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `delete from searchable_records where registered_trawler = ?`, target.RegisteredTrawler); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		insert into searchable_source_generations(
			registered_trawler, required_generation, all_source_records_indexed,
			next_page_starts_after_canonical_record_reference
		) values (?, ?, 0, '')
		on conflict(registered_trawler) do update set
			required_generation = excluded.required_generation,
			all_source_records_indexed = 0,
			next_page_starts_after_canonical_record_reference = ''`,
		target.RegisteredTrawler, uint64(target.RequiredGeneration)); err != nil {
		return err
	}
	return transaction.Commit()
}

func markStagingSourceComplete(ctx context.Context, staging *sql.DB, registeredTrawler string) error {
	_, err := staging.ExecContext(ctx, `
		update searchable_source_generations
		set all_source_records_indexed = 1
		where registered_trawler = ?`, registeredTrawler)
	return err
}

func sourceIndexCounts(index *sql.DB, registeredTrawler string) (uint64, uint64, error) {
	var recordCount, passageCount uint64
	err := index.QueryRow(`
		select count(distinct record.searchable_record_identifier), count(passage.searchable_passage_identifier)
		from searchable_records record
		left join searchable_passages passage using(searchable_record_identifier)
		where record.registered_trawler = ?`, registeredTrawler).Scan(&recordCount, &passageCount)
	return recordCount, passageCount, err
}

func sameSourceGenerations(left, right []SourceRequiredGeneration) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func copyFile(sourcePath, destinationPath string) (returnedError error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { returnedError = errors.Join(returnedError, source.Close()) }()
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { returnedError = errors.Join(returnedError, destination.Close()) }()
	if _, err := io.Copy(destination, source); err != nil {
		return err
	}
	return destination.Sync()
}

func syncFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return file.Sync()
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
