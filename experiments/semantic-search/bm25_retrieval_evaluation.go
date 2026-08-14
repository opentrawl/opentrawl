package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"
)

func evaluateBM25Retrieval(arguments []string) error {
	flags := flag.NewFlagSet("evaluate-bm25", flag.ContinueOnError)
	corpusPath := flags.String("corpus", "", "private frozen corpus database")
	manifestPath := flags.String("manifest", "", "full-corpus frozen manifest")
	queryPath := flags.String("queries", "", "private frozen retrieval questions")
	resultPath := flags.String("results", "", "new private BM25 evaluation result database")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *corpusPath == "" || *manifestPath == "" || *queryPath == "" || *resultPath == "" {
		return errors.New("--corpus, --manifest, --queries and --results are required")
	}
	if _, err := os.Stat(*resultPath); err == nil {
		return errors.New("result database already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	corpusSHA256, err := sha256File(*corpusPath)
	if err != nil {
		return err
	}
	manifestSHA256, err := sha256File(*manifestPath)
	if err != nil {
		return err
	}
	querySHA256, err := sha256File(*queryPath)
	if err != nil {
		return err
	}
	manifestDocumentCount, _, err := readFullCorpusManifestContract(*manifestPath, corpusSHA256)
	if err != nil {
		return err
	}
	corpus, err := openFrozenSearchCorpus(*corpusPath, *manifestPath)
	if err != nil {
		return err
	}
	defer corpus.Close()
	queries, err := readAndResolveRetrievalEvaluationQueries(*queryPath, corpus)
	if err != nil {
		return err
	}
	if err := validateConstrainedSupportingEvidenceEligibility(queries, corpus); err != nil {
		return err
	}
	eligibleRecordCountsByLaneAndQuery, err := countEligibleCanonicalRecordsByLaneAndQuery(corpus, queries)
	if err != nil {
		return err
	}
	resultDatabase, err := createBM25RetrievalEvaluationResultDatabase(
		*resultPath,
		corpusSHA256,
		manifestSHA256,
		querySHA256,
		manifestDocumentCount,
	)
	if err != nil {
		return err
	}
	defer resultDatabase.Close()
	startedAt := time.Now()
	aggregates := map[retrievalEvaluationLane]*retrievalAggregateMetrics{
		unfilteredRetrievalEvaluationLane:  {},
		constrainedRetrievalEvaluationLane: {},
	}
	for queryNumber, query := range queries {
		for _, lane := range []retrievalEvaluationLane{unfilteredRetrievalEvaluationLane, constrainedRetrievalEvaluationLane} {
			constraints := constraintsForEvaluationLane(lane, query)
			retrieval, err := retrieveProductionShapedCanonicalRecords(
				corpus,
				query.InformationNeed,
				constraints,
				nil,
				maximumRetrievedCanonicalRecordsPerQuery,
			)
			if err != nil {
				return err
			}
			rankedRecords := retrieval.bm25Candidates[:min(len(retrieval.bm25Candidates), maximumRetrievedCanonicalRecordsPerQuery)]
			metrics := calculateQueryEvaluationMetrics(query, rankedRecords)
			acceptableRecordsInBM25Candidates := countAcceptableRecordsInCandidates(query, retrieval.bm25Candidates)
			if err := storeBM25RetrievalEvaluationResult(
				resultDatabase,
				lane,
				query,
				rankedRecords,
				eligibleRecordCountsByLaneAndQuery[lane][queryNumber],
				metrics,
				acceptableRecordsInBM25Candidates,
			); err != nil {
				return err
			}
			addQueryEvaluationMetrics(aggregates[lane], query, metrics)
		}
	}
	elapsed := time.Since(startedAt)
	if _, err := resultDatabase.Exec("update evaluation_metadata set elapsed_nanoseconds = ?", elapsed.Nanoseconds()); err != nil {
		return err
	}
	if err := resultDatabase.Close(); err != nil {
		return err
	}
	if err := os.Chmod(*resultPath, 0o600); err != nil {
		return err
	}
	for _, lane := range []retrievalEvaluationLane{unfilteredRetrievalEvaluationLane, constrainedRetrievalEvaluationLane} {
		metrics := aggregates[lane]
		fmt.Printf("lane=%s queries=%d hit_at_5=%d hit_at_20=%d mean_reciprocal_rank=%.6f labelled_record_recall_at_20=%.6f labelled_source_recall_at_20=%.6f weakening_record_recall_at_20=%.6f\n",
			lane,
			metrics.queryCount,
			metrics.queriesWithAcceptableRecordAtFive,
			metrics.queriesWithAcceptableRecordAtTwenty,
			metrics.reciprocalRankTotal/float64(metrics.queryCount),
			ratio(metrics.acceptableRecordLabelsRetrieved, metrics.acceptableRecordLabels),
			ratio(metrics.acceptableSourceLabelsRetrieved, metrics.acceptableSourceLabels),
			ratio(metrics.weakeningRecordLabelsRetrieved, metrics.weakeningRecordLabels),
		)
	}
	fmt.Printf("results=%s elapsed=%s\n", *resultPath, elapsed.Round(time.Millisecond))
	return nil
}

func countAcceptableRecordsInCandidates(query resolvedRetrievalEvaluationQuery, candidates []rankedCanonicalArchiveRecord) int {
	count := 0
	for _, candidate := range candidates {
		if _, acceptable := query.acceptableSupportingRecords[candidate.identity]; acceptable {
			count++
		}
	}
	return count
}

func countEligibleCanonicalRecordsByLaneAndQuery(corpus *sql.DB, queries []resolvedRetrievalEvaluationQuery) (map[retrievalEvaluationLane][]int64, error) {
	counts := map[retrievalEvaluationLane][]int64{
		unfilteredRetrievalEvaluationLane:  make([]int64, len(queries)),
		constrainedRetrievalEvaluationLane: make([]int64, len(queries)),
	}
	rows, err := corpus.Query(`
		select document.registered_trawler, document.canonical_archive_record_reference,
		       document.local_short_reference, document.associated_time
		from frozen_manifest.selected_documents selected
		join archive_documents document on document.document_identifier = selected.document_identifier
		order by selected.document_identifier`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var previousIdentity canonicalArchiveRecordIdentity
	previousIdentityExists := false
	completedIdentities := make(map[canonicalArchiveRecordIdentity]struct{})
	for rows.Next() {
		var record rankedCanonicalArchiveRecord
		if err := rows.Scan(
			&record.identity.registeredTrawler,
			&record.identity.canonicalArchiveRecordReference,
			&record.localShortReference,
			&record.associatedTime,
		); err != nil {
			return nil, err
		}
		if previousIdentityExists && record.identity == previousIdentity {
			continue
		}
		if _, reappeared := completedIdentities[record.identity]; reappeared {
			return nil, errors.New("frozen manifest does not keep canonical record chunks consecutive")
		}
		if previousIdentityExists {
			completedIdentities[previousIdentity] = struct{}{}
		}
		previousIdentity = record.identity
		previousIdentityExists = true
		for queryNumber, query := range queries {
			counts[unfilteredRetrievalEvaluationLane][queryNumber]++
			if evaluationQueryAcceptsRecord(query.SuppliedConstraints, record) {
				counts[constrainedRetrievalEvaluationLane][queryNumber]++
			}
		}
	}
	return counts, rows.Err()
}

func createBM25RetrievalEvaluationResultDatabase(resultPath string, corpusSHA256 string, manifestSHA256 string, querySHA256 string, documentCount int64) (*sql.DB, error) {
	if err := os.MkdirAll(filepathDirectory(resultPath), 0o700); err != nil {
		return nil, err
	}
	resultDatabase, err := sql.Open("sqlite3", resultPath)
	if err != nil {
		return nil, err
	}
	resultDatabase.SetMaxOpenConns(1)
	if err := resultDatabase.Ping(); err != nil {
		resultDatabase.Close()
		return nil, err
	}
	if err := os.Chmod(resultPath, 0o600); err != nil {
		resultDatabase.Close()
		return nil, err
	}
	if _, err := resultDatabase.Exec(`
		create table evaluation_metadata(
			corpus_sha256 text not null,
			manifest_sha256 text not null,
			query_sha256 text not null,
			indexed_document_count integer not null,
			retrieval_method text not null,
			lexical_query_method text not null,
			candidate_canonical_records integer not null,
			maximum_retrieved_canonical_records_per_query integer not null,
			date_predicate text not null,
			elapsed_nanoseconds integer not null
		);
		create table retrieved_canonical_records(
			lane text not null,
			query_identifier text not null,
			rank integer not null,
			document_identifier integer not null,
			registered_trawler text not null,
			canonical_archive_record_reference text not null,
			local_short_reference text not null,
			bm25_score real not null,
			primary key(lane, query_identifier, rank),
			unique(lane, query_identifier, registered_trawler, canonical_archive_record_reference)
		);
		create table query_metrics(
			lane text not null,
			query_identifier text not null,
			category text not null,
			required_evidence_scope text not null,
			eligible_canonical_record_count integer not null,
			returned_canonical_record_count integer not null,
			acceptable_record_count integer not null,
			first_acceptable_rank integer not null,
			acceptable_records_retrieved_at_five integer not null,
			acceptable_records_retrieved_at_twenty integer not null,
			weakening_record_count integer not null,
			weakening_records_retrieved_at_twenty integer not null,
			acceptable_source_count integer not null,
			acceptable_sources_retrieved_at_twenty integer not null,
			acceptable_records_in_bm25_candidates integer not null,
			primary key(lane, query_identifier)
		);
	`); err != nil {
		resultDatabase.Close()
		return nil, err
	}
	if _, err := resultDatabase.Exec(`insert into evaluation_metadata values (?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		corpusSHA256,
		manifestSHA256,
		querySHA256,
		documentCount,
		"canonical-record SQLite FTS5 BM25 rank",
		"sanitized unique query tokens joined with OR; default FTS5 unicode61 tokenizer",
		hybridCandidateCanonicalRecordsPerBranch,
		maximumRetrievedCanonicalRecordsPerQuery,
		"inclusive comparison of substr(associated_time, 1, 10) against YYYY-MM-DD bounds",
	); err != nil {
		resultDatabase.Close()
		return nil, err
	}
	return resultDatabase, nil
}

func storeBM25RetrievalEvaluationResult(resultDatabase *sql.DB, lane retrievalEvaluationLane, query resolvedRetrievalEvaluationQuery, rankedRecords []rankedCanonicalArchiveRecord, eligibleCanonicalRecordCount int64, metrics queryEvaluationMetrics, acceptableRecordsInBM25Candidates int) error {
	transaction, err := resultDatabase.Begin()
	if err != nil {
		return err
	}
	for recordNumber, record := range rankedRecords {
		if _, err := transaction.Exec(`insert into retrieved_canonical_records values (?, ?, ?, ?, ?, ?, ?, ?)`,
			lane,
			query.Identifier,
			recordNumber+1,
			record.documentIdentifier,
			record.identity.registeredTrawler,
			record.identity.canonicalArchiveRecordReference,
			record.localShortReference,
			record.cosineSimilarity,
		); err != nil {
			transaction.Rollback()
			return err
		}
	}
	if _, err := transaction.Exec(`insert into query_metrics values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		lane,
		query.Identifier,
		query.Category,
		query.RequiredEvidenceScope,
		eligibleCanonicalRecordCount,
		len(rankedRecords),
		len(query.acceptableSupportingRecords),
		metrics.firstAcceptableRank,
		metrics.acceptableRecordsRetrievedAtFive,
		metrics.acceptableRecordsRetrievedAtTwenty,
		len(query.contradictionOrWeakeningRecords),
		metrics.weakeningRecordsRetrievedAtTwenty,
		metrics.acceptableSources,
		metrics.acceptableSourcesRetrievedAtTwenty,
		acceptableRecordsInBM25Candidates,
	); err != nil {
		transaction.Rollback()
		return err
	}
	return transaction.Commit()
}
