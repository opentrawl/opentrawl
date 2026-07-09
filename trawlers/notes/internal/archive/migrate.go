package archive

import (
	"context"
	"database/sql"
	"log"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/projection"
)

// migrate self-heals the archive to the current SchemaVersion the next time it
// is opened. The only domain migration today is the TRAWL-210 re-projection:
// every stored body is re-decoded from its archived zdata to markdown. It runs
// before EnsureSchemaVersion bumps the recorded version, so it sees the old
// data, and it is a no-op once the recorded version already matches.
func (s *Store) migrate(ctx context.Context) error {
	current, err := s.store.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if current >= SchemaVersion {
		return nil
	}
	reprojected, err := s.reprojectAll(ctx)
	if err != nil {
		return err
	}
	if reprojected > 0 {
		log.Printf("notes archive: re-projected %d stored bodies to markdown (schema v%d to v%d)", reprojected, current, SchemaVersion)
	}
	return nil
}

type storedBody struct {
	noteID string
	sha    string
	title  string
	zdata  []byte
}

// reprojectAll re-decodes every note_versions row from its archived zdata and
// rewrites text/text_status/unsupported_reason, then rebuilds notes_fts so that
// decoded rows are indexed and unsupported rows are not. Tables resolve against
// note_table_data (empty for a pre-migration archive, so those render the "not
// captured" marker). Returns the number of rows rewritten.
func (s *Store) reprojectAll(ctx context.Context) (int, error) {
	count := 0
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		bodies, err := loadStoredBodies(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `delete from notes_fts`); err != nil {
			return err
		}
		resolve := tableResolver(ctx, tx)
		for _, body := range bodies {
			text, status, unsupported := reproject(body.zdata, resolve)
			if _, err := tx.ExecContext(ctx, `
update note_versions
set text = ?, text_status = ?, unsupported_reason = ?
where note_id = ? and zdata_sha256 = ?`,
				text, status, unsupported, body.noteID, body.sha); err != nil {
				return err
			}
			if status == "decoded" {
				if _, err := tx.ExecContext(ctx, `
insert into notes_fts (note_id, zdata_sha256, title, body)
values (?, ?, ?, ?)`, body.noteID, body.sha, body.title, text); err != nil {
					return err
				}
			}
			count++
		}
		return nil
	})
	return count, err
}

func loadStoredBodies(ctx context.Context, tx *sql.Tx) ([]storedBody, error) {
	rows, err := tx.QueryContext(ctx, `
select nv.note_id, nv.zdata_sha256, nv.zdata, coalesce(n.title, '')
from note_versions nv
left join notes n on n.note_id = nv.note_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []storedBody
	for rows.Next() {
		var body storedBody
		if err := rows.Scan(&body.noteID, &body.sha, &body.zdata, &body.title); err != nil {
			return nil, err
		}
		out = append(out, body)
	}
	return out, rows.Err()
}

func reproject(zdata []byte, resolve projection.TableResolver) (text, status, unsupported string) {
	decoded, err := projection.DecodeMarkdown(zdata, resolve)
	if err == nil {
		return strings.TrimSpace(decoded), "decoded", ""
	}
	return "", "unsupported", err.Error()
}
