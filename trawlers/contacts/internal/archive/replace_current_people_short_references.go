package archive

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlkit"
)

func replaceShortReferencesForCurrentPeopleUsingContactsSnapshotTransaction(
	ctx context.Context,
	contactsSnapshotTransaction *sql.Tx,
) error {
	rows, err := contactsSnapshotTransaction.QueryContext(ctx, `select id from people order by id`)
	if err != nil {
		return fmt.Errorf("read current Contacts people for short reference assignment: %w", err)
	}
	shortReferenceAssignmentCandidatesForCurrentPeople := []trawlkit.ShortReferenceAssignmentCandidate{}
	for rows.Next() {
		var personID string
		if err := rows.Scan(&personID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read current Contacts person identity for short reference assignment: %w", err)
		}
		shortReferenceAssignmentCandidatesForCurrentPeople = append(
			shortReferenceAssignmentCandidatesForCurrentPeople,
			trawlkit.ShortReferenceAssignmentCandidate{
				StableRecordReferenceUsedForShortReferenceAssignment: trawlkit.NewCanonicalArchiveRecordReference(
					PersonRef(personID),
				),
			},
		)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("read current Contacts people for short reference assignment: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close current Contacts people short reference assignment rows: %w", err)
	}
	err = trawlkit.ReplaceShortReferencesForCompleteArchiveRecordSnapshotUsingCallerOwnedSQLTransaction(
		ctx,
		contactsSnapshotTransaction,
		shortReferenceAssignmentCandidatesForCurrentPeople,
	)
	if err != nil {
		return fmt.Errorf("assign current Contacts person short references: %w", err)
	}
	return nil
}
