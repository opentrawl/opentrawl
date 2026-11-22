package archive

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
)

// ExportArchiveIndexChanges streams all Contacts person records into core index.db.
func (s *Store) ExportArchiveIndexChanges(
	ctx context.Context,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	var count int64
	var maxUpdated string
	if err := s.database().QueryRowContext(
		ctx,
		`select count(*), coalesce(max(updated_at), '') from people`,
	).Scan(&count, &maxUpdated); err != nil {
		return fmt.Errorf("read contacts index cursor state: %w", err)
	}
	nextCursor := fmt.Sprintf("contacts:%d:%s", count, maxUpdated)
	if sinceOpaqueCursor != "" && sinceOpaqueCursor == nextCursor {
		return nil
	}

	valuesByPerson, err := s.loadContactValuesByPerson(ctx)
	if err != nil {
		return err
	}

	rows, err := s.database().QueryContext(ctx, `
		select id, name, aka_json, body, person_relationship_or_context_description
		from people
		order by id
	`)
	if err != nil {
		return fmt.Errorf("read contacts people for unified index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	upserts := make([]*indexcontract.IndexedArchiveRecordUpsert, 0, count)
	for rows.Next() {
		var id, name, akaJSON, body, contextDesc string
		if err := rows.Scan(&id, &name, &akaJSON, &body, &contextDesc); err != nil {
			return fmt.Errorf("scan contact person for unified index: %w", err)
		}
		ref := trawlkit.NewCanonicalArchiveRecordReference(PersonRef(id))
		participants := buildContactParticipants(name, akaJSON, valuesByPerson[id])
		fullBody := strings.TrimSpace(strings.Join([]string{contextDesc, body}, "\n"))
		upserts = append(upserts, &indexcontract.IndexedArchiveRecordUpsert{
			StableRecordReference:          ref,
			ResolvedRecordReference:        ref,
			RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_PERSON,
			ConversationOrThreadIdentifier: PersonRef(id),
			Title:                          strings.TrimSpace(name),
			BodyText:                       fullBody,
			Participants:                   participants,
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return emit(&indexcontract.ArchiveIndexChangeBatch{
		Upserts:                     upserts,
		CompleteSnapshotReplacement: true,
		NextOpaqueCursor:            nextCursor,
	})
}

func (s *Store) loadContactValuesByPerson(ctx context.Context) (map[string][]string, error) {
	rows, err := s.database().QueryContext(ctx, `select person_id, value from contact_values where trim(value) <> '' order by person_id, position`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string][]string)
	for rows.Next() {
		var personID, val string
		if err := rows.Scan(&personID, &val); err != nil {
			return nil, err
		}
		out[personID] = append(out[personID], strings.TrimSpace(val))
	}
	return out, rows.Err()
}

func buildContactParticipants(name, akaJSON string, values []string) []*indexcontract.ArchiveRecordParticipant {
	out := []*indexcontract.ArchiveRecordParticipant{{
		DisplayName:     strings.TrimSpace(name),
		ParticipantRole: "person",
	}}
	var akas []string
	if err := json.Unmarshal([]byte(akaJSON), &akas); err == nil {
		for _, aka := range akas {
			if trimmed := strings.TrimSpace(aka); trimmed != "" {
				out = append(out, &indexcontract.ArchiveRecordParticipant{
					DisplayName:     trimmed,
					ParticipantRole: "alias",
				})
			}
		}
	}
	for _, val := range values {
		out = append(out, &indexcontract.ArchiveRecordParticipant{
			AddressOrHandle: val,
			ParticipantRole: "contact",
		})
	}
	return out
}
