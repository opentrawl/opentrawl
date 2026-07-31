package trawlkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/shortref"
)

var (
	ErrUnknownShortRef                                            = errors.New("unknown short ref")
	ErrAmbiguousShortRef                                          = errors.New("ambiguous short ref")
	ErrLocalConversationShortReferenceDoesNotIdentifyConversation = errors.New("local conversation short reference does not identify a conversation")
)

func (r *TrawlerCommandExecutionRequest) ResolveLocalConversationShortReferenceToProviderNativeConversationIdentifier(
	ctx context.Context,
	localConversationShortReferenceAcceptedBySelectedTrawler *LocalTrawlerShortReference,
	providerNativeConversationRecordReferencePrefix string,
) (string, error) {
	localConversationShortReferenceText :=
		LocalTrawlerShortReferenceText(localConversationShortReferenceAcceptedBySelectedTrawler)
	if localConversationShortReferenceText == "" {
		return "", nil
	}
	if !ValidShortRef(localConversationShortReferenceText) {
		return "", ErrUnknownShortRef
	}
	canonicalRecordReferences, err := r.ResolveShortReference(ctx, localConversationShortReferenceAcceptedBySelectedTrawler)
	if err != nil {
		return "", err
	}
	canonicalConversationRecordReference :=
		CanonicalArchiveRecordReferenceText(canonicalRecordReferences[0])
	if !strings.HasPrefix(canonicalConversationRecordReference, providerNativeConversationRecordReferencePrefix) {
		return "", ErrLocalConversationShortReferenceDoesNotIdentifyConversation
	}
	return strings.TrimPrefix(canonicalConversationRecordReference, providerNativeConversationRecordReferencePrefix), nil
}

func ValidShortRef(alias string) bool {
	return shortref.ValidAlias(strings.TrimSpace(alias))
}

// AssignShortReferences extends the link index during a successful trawler
// update without deleting rows.
//
// Existing aliases are permanent: an update changes resolved record references
// for matching stable record references and assigns aliases only to records not
// already indexed. Rows for deleted records intentionally persist, so opening
// one reports not found. Aliases never re-shorten after a collision has made
// them longer.
func (r *TrawlerCommandExecutionRequest) AssignShortReferences(ctx context.Context, records []ShortReferenceAssignmentCandidate) (int, error) {
	if r == nil || r.OpenedTrawlerArchiveStore == nil {
		return 0, errors.New("archive store is not open")
	}
	assignedShortReferenceCount := 0
	err := r.OpenedTrawlerArchiveStore.WithTx(ctx, func(callerOwnedSQLTransaction *sql.Tx) error {
		var err error
		assignedShortReferenceCount, err = AssignShortReferencesForArchiveRecordsUsingCallerOwnedSQLTransaction(
			ctx,
			callerOwnedSQLTransaction,
			records,
		)
		return err
	})
	if err != nil {
		return 0, err
	}
	return assignedShortReferenceCount, nil
}

func AssignShortReferencesForArchiveRecordsUsingCallerOwnedSQLTransaction(
	ctx context.Context,
	callerOwnedSQLTransaction *sql.Tx,
	records []ShortReferenceAssignmentCandidate,
) (int, error) {
	if callerOwnedSQLTransaction == nil {
		return 0, errors.New("caller-owned SQL transaction is missing")
	}
	shortReferenceAssignmentIndexRecords, err := makeShortReferenceAssignmentIndexRecords(records)
	if err != nil {
		return 0, err
	}
	stableRecordReferences := collectStableRecordReferences(shortReferenceAssignmentIndexRecords)
	resolvedRecordReferencesByStableReference := mapResolvedRecordReferencesByStableReference(shortReferenceAssignmentIndexRecords)
	if err := shortref.EnsureSchema(ctx, callerOwnedSQLTransaction); err != nil {
		return 0, fmt.Errorf("assign short refs: %w", err)
	}
	index := shortref.NewSQLiteIndex(callerOwnedSQLTransaction)
	if err := index.UpdateCanonicalRefs(ctx, resolvedRecordReferencesByStableReference); err != nil {
		return 0, fmt.Errorf("assign short refs: %w", err)
	}
	indexedStableRecordReferences, err := index.IndexedFullRefs(ctx, stableRecordReferences)
	if err != nil {
		return 0, fmt.Errorf("assign short refs: %w", err)
	}
	newStableRecordReferences := selectNewStableRecordReferences(shortReferenceAssignmentIndexRecords, indexedStableRecordReferences)
	aliases, err := index.AllAliases(ctx)
	if err != nil {
		return 0, fmt.Errorf("assign short refs: %w", err)
	}
	entries, err := shortref.BuildSliceAvoidingAliases(newStableRecordReferences, aliases)
	if err != nil {
		return 0, fmt.Errorf("assign short refs: %w", err)
	}
	if err := index.UpsertCanonicalEntries(
		ctx,
		shortRefLookupEntries(entries, aliases),
		resolvedRecordReferencesByStableReference,
	); err != nil {
		return 0, fmt.Errorf("assign short refs: %w", err)
	}
	return len(shortReferenceAssignmentIndexRecords), nil
}

func (r *TrawlerCommandExecutionRequest) ResolveShortReference(
	ctx context.Context,
	localTrawlerShortReference *LocalTrawlerShortReference,
) ([]*CanonicalArchiveRecordReference, error) {
	alias := LocalTrawlerShortReferenceText(localTrawlerShortReference)
	if !ValidShortRef(alias) {
		return nil, ErrUnknownShortRef
	}
	matches, err := r.lookupShortRef(ctx, alias)
	if err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, ErrUnknownShortRef
	case 1:
		return []*CanonicalArchiveRecordReference{
			NewCanonicalArchiveRecordReference(matches[0]),
		}, nil
	default:
		canonicalArchiveRecordReferences := make(
			[]*CanonicalArchiveRecordReference,
			0,
			len(matches),
		)
		for _, match := range matches {
			canonicalArchiveRecordReferences = append(
				canonicalArchiveRecordReferences,
				NewCanonicalArchiveRecordReference(match),
			)
		}
		return canonicalArchiveRecordReferences, ErrAmbiguousShortRef
	}
}

func (r *TrawlerCommandExecutionRequest) LocalShortReferencesForCanonicalArchiveRecordReferences(
	ctx context.Context,
	refs []*CanonicalArchiveRecordReference,
) ([]CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if r == nil || r.OpenedTrawlerArchiveStore == nil {
		return nil, errors.New("archive store is not open")
	}
	index := shortref.NewSQLiteIndex(r.OpenedTrawlerArchiveStore.DB())
	canonical := make([]string, 0, len(refs))
	for _, ref := range refs {
		canonicalArchiveRecordReference := CanonicalArchiveRecordReferenceText(ref)
		if canonicalArchiveRecordReference == "" {
			continue
		}
		canonical = append(canonical, canonicalArchiveRecordReference)
	}
	aliases, err := shortRefAliases(ctx, index, canonical)
	if err != nil {
		return nil, err
	}
	references := make(
		[]CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference,
		0,
		len(canonical),
	)
	for _, canonicalArchiveRecordReference := range canonical {
		localTrawlerShortReference := strings.TrimSpace(aliases[canonicalArchiveRecordReference])
		if localTrawlerShortReference == "" {
			continue
		}
		references = append(references, CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference{
			CanonicalArchiveRecordReference: NewCanonicalArchiveRecordReference(canonicalArchiveRecordReference),
			LocalTrawlerShortReference:      NewLocalTrawlerShortReference(localTrawlerShortReference),
		})
	}
	return references, nil
}

type CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference struct {
	CanonicalArchiveRecordReference *CanonicalArchiveRecordReference
	LocalTrawlerShortReference      *LocalTrawlerShortReference
}

func readAssignedLocalShortReferencesByCanonicalRecordReference(
	ctx context.Context,
	request *TrawlerCommandExecutionRequest,
	canonicalRecordReferences []*CanonicalArchiveRecordReference,
) ([]CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, error) {
	if len(canonicalRecordReferences) == 0 {
		return nil, nil
	}
	if request == nil {
		return nil, errors.New("trawler command request is missing")
	}
	return request.LocalShortReferencesForCanonicalArchiveRecordReferences(ctx, canonicalRecordReferences)
}

func LocalTrawlerShortReferenceForCanonicalArchiveRecordReference(
	references []CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference,
	canonicalArchiveRecordReference *CanonicalArchiveRecordReference,
) *LocalTrawlerShortReference {
	wantedCanonicalArchiveRecordReference :=
		CanonicalArchiveRecordReferenceText(canonicalArchiveRecordReference)
	for _, reference := range references {
		if CanonicalArchiveRecordReferenceText(reference.CanonicalArchiveRecordReference) ==
			wantedCanonicalArchiveRecordReference {
			return reference.LocalTrawlerShortReference
		}
	}
	return nil
}

func (r *TrawlerCommandExecutionRequest) lookupShortRef(ctx context.Context, alias string) ([]string, error) {
	if r == nil || r.OpenedTrawlerArchiveStore == nil {
		return nil, errors.New("archive store is not open")
	}
	return shortref.NewSQLiteIndex(r.OpenedTrawlerArchiveStore.DB()).Lookup(ctx, alias)
}

func shortRefAliases(ctx context.Context, index *shortref.SQLiteIndex, refs []string) (map[string]string, error) {
	refs = uniqueStrings(refs)
	if len(refs) == 0 {
		return nil, nil
	}
	return index.Aliases(ctx, refs)
}

func shortRefLookupEntries(entries []shortref.Entry, reservedAliases map[string]struct{}) []shortref.Entry {
	lookupEntries := shortref.LookupEntries(entries)
	if len(reservedAliases) == 0 {
		return lookupEntries
	}
	filtered := lookupEntries[:0]
	for _, entry := range lookupEntries {
		if _, reserved := reservedAliases[entry.Alias]; reserved {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

type shortReferenceAssignmentIndexRecord struct {
	stableRecordReference   string
	resolvedRecordReference string
}

func makeShortReferenceAssignmentIndexRecords(records []ShortReferenceAssignmentCandidate) ([]shortReferenceAssignmentIndexRecord, error) {
	indexRecords := make([]shortReferenceAssignmentIndexRecord, 0, len(records))
	indexRecordPositionByStableReference := make(map[string]int, len(records))
	for _, record := range records {
		// The alias index is ref-shape-agnostic; trawlers own their ref grammar.
		stableRecordReference := CanonicalArchiveRecordReferenceText(
			record.StableRecordReferenceUsedForShortReferenceAssignment,
		)
		if stableRecordReference == "" {
			continue
		}
		resolvedRecordReference := CanonicalArchiveRecordReferenceText(
			record.CurrentRecordReferenceReturnedWhenShortReferenceIsResolved,
		)
		if resolvedRecordReference == "" {
			resolvedRecordReference = stableRecordReference
		}
		if existingPosition, exists := indexRecordPositionByStableReference[stableRecordReference]; exists {
			if indexRecords[existingPosition].resolvedRecordReference != resolvedRecordReference {
				return nil, fmt.Errorf("short reference %q has conflicting resolved record references", stableRecordReference)
			}
			continue
		}
		indexRecordPositionByStableReference[stableRecordReference] = len(indexRecords)
		indexRecords = append(indexRecords, shortReferenceAssignmentIndexRecord{
			stableRecordReference:   stableRecordReference,
			resolvedRecordReference: resolvedRecordReference,
		})
	}
	return indexRecords, nil
}

func collectStableRecordReferences(records []shortReferenceAssignmentIndexRecord) []string {
	stableRecordReferences := make([]string, 0, len(records))
	for _, record := range records {
		stableRecordReferences = append(stableRecordReferences, record.stableRecordReference)
	}
	return stableRecordReferences
}

func mapResolvedRecordReferencesByStableReference(records []shortReferenceAssignmentIndexRecord) map[string]string {
	resolvedRecordReferencesByStableReference := make(map[string]string, len(records))
	for _, record := range records {
		resolvedRecordReferencesByStableReference[record.stableRecordReference] = record.resolvedRecordReference
	}
	return resolvedRecordReferencesByStableReference
}

func selectNewStableRecordReferences(records []shortReferenceAssignmentIndexRecord, indexedStableRecordReferences map[string]struct{}) []string {
	newStableRecordReferences := make([]string, 0, len(records))
	for _, record := range records {
		if _, indexed := indexedStableRecordReferences[record.stableRecordReference]; indexed {
			continue
		}
		newStableRecordReferences = append(newStableRecordReferences, record.stableRecordReference)
	}
	return newStableRecordReferences
}
