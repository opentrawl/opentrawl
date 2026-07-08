package notesdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notestime"
)

const appleUnixOffset = 978307200

var ErrMalformed = errors.New("malformed notes database")

func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", writeDSN(path))
	if err != nil {
		return nil, wrapMalformed(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, wrapMalformed(err)
	}
	if err := integrityProbe(ctx, db); err != nil {
		_ = db.Close()
		return nil, wrapMalformed(err)
	}
	return db, nil
}

func ReadModificationIndex(ctx context.Context, db *sql.DB) (_ map[string]string, err error) {
	defer func() { err = wrapMalformed(err) }()
	rows, err := db.QueryContext(ctx, `
select note.ZIDENTIFIER, note.ZMODIFICATIONDATE1, length(data.ZDATA)
from ZICCLOUDSYNCINGOBJECT note
join ZICNOTEDATA data on note.ZNOTEDATA = data.Z_PK
where note.ZIDENTIFIER is not null
  and trim(note.ZIDENTIFIER) <> ''
  and note.ZNOTEDATA is not null
  and data.ZDATA is not null
order by note.ZIDENTIFIER`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var id string
		var modified sql.NullFloat64
		var zdataBytes sql.NullInt64
		if err := rows.Scan(&id, &modified, &zdataBytes); err != nil {
			return nil, err
		}
		out[id] = bodyChangeKey(modified, zdataBytes)
	}
	return out, rows.Err()
}

func ReadBodies(ctx context.Context, db *sql.DB, changed map[string]bool) (_ []Body, err error) {
	defer func() { err = wrapMalformed(err) }()
	args := []any{}
	filter := ""
	if changed != nil {
		ids := changedIDs(changed)
		if len(ids) == 0 {
			return nil, nil
		}
		filter = "  and note.ZIDENTIFIER in (" + placeholders(len(ids)) + ")\n"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	rows, err := db.QueryContext(ctx, `
select note.ZIDENTIFIER, note.ZMODIFICATIONDATE1, data.ZDATA
from ZICCLOUDSYNCINGOBJECT note
join ZICNOTEDATA data on note.ZNOTEDATA = data.Z_PK
where note.ZIDENTIFIER is not null
  and trim(note.ZIDENTIFIER) <> ''
  and data.ZDATA is not null
`+filter+`order by note.ZIDENTIFIER`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Body{}
	for rows.Next() {
		var id string
		var modified sql.NullFloat64
		var zdata []byte
		if err := rows.Scan(&id, &modified, &zdata); err != nil {
			return nil, err
		}
		out = append(out, Body{NoteID: id, SourceModifiedAt: appleDate(modified), ZData: zdata})
	}
	return out, rows.Err()
}

func ReadFinalState(ctx context.Context, db *sql.DB) (_ FinalState, err error) {
	defer func() { err = wrapMalformed(err) }()
	rows, err := db.QueryContext(ctx, `
select note.ZIDENTIFIER,
       coalesce(note.ZTITLE1, ''),
       coalesce(folder.ZTITLE2, ''),
       note.ZCREATIONDATE1,
       note.ZCREATIONDATE3,
       note.ZMODIFICATIONDATE1,
       data.ZDATA,
       note.ZACTIVITYEVENTSDATA
from ZICCLOUDSYNCINGOBJECT note
left join ZICNOTEDATA data on note.ZNOTEDATA = data.Z_PK
left join ZICCLOUDSYNCINGOBJECT folder on note.ZFOLDER = folder.Z_PK
where note.ZIDENTIFIER is not null
  and trim(note.ZIDENTIFIER) <> ''
  and note.ZNOTEDATA is not null
order by note.ZIDENTIFIER`)
	if err != nil {
		return FinalState{}, err
	}
	defer func() { _ = rows.Close() }()
	state := FinalState{}
	for rows.Next() {
		var note Note
		var created1, created3, modified sql.NullFloat64
		var zdata, activity []byte
		if err := rows.Scan(&note.ID, &note.Title, &note.Folder, &created1, &created3, &modified, &zdata, &activity); err != nil {
			return FinalState{}, err
		}
		note.CreatedAt = firstAppleDate(created1, created3)
		note.ModifiedAt = appleDate(modified)
		state.Notes = append(state.Notes, note)
		if len(zdata) > 0 {
			state.Bodies = append(state.Bodies, Body{NoteID: note.ID, SourceModifiedAt: note.ModifiedAt, ZData: zdata})
		}
		if len(activity) > 0 {
			state.Activities = append(state.Activities, ActivityBlob{NoteID: note.ID, Data: activity})
		}
	}
	if err := rows.Err(); err != nil {
		return FinalState{}, err
	}
	return state, nil
}

func integrityProbe(ctx context.Context, db *sql.DB) error {
	var count int
	return db.QueryRowContext(ctx, "select count(*) from ZICNOTEDATA").Scan(&count)
}

func writeDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("_busy_timeout", "5000")
	q.Set("_foreign_keys", "1")
	u.RawQuery = q.Encode()
	return u.String()
}

func wrapMalformed(err error) error {
	if err == nil || !malformedText(err) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrMalformed, err)
}

func malformedText(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "malformed") ||
		strings.Contains(msg, "file is not a database") ||
		strings.Contains(msg, "database disk image is malformed")
}

func changedIDs(changed map[string]bool) []string {
	ids := []string{}
	for id, ok := range changed {
		if ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func placeholders(count int) string {
	out := strings.Builder{}
	for i := 0; i < count; i++ {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteByte('?')
	}
	return out.String()
}

func bodyChangeKey(modified sql.NullFloat64, zdataBytes sql.NullInt64) string {
	modifiedKey := ""
	if modified.Valid {
		modifiedKey = strconv.FormatFloat(modified.Float64, 'g', -1, 64)
	}
	bytesKey := ""
	if zdataBytes.Valid {
		bytesKey = strconv.FormatInt(zdataBytes.Int64, 10)
	}
	return modifiedKey + "|" + bytesKey
}

func firstAppleDate(first, second sql.NullFloat64) string {
	if first.Valid {
		return appleDate(first)
	}
	return appleDate(second)
}

func appleDate(value sql.NullFloat64) string {
	if !value.Valid {
		return ""
	}
	return AppleDateFloat(value.Float64)
}

func AppleDateFloat(value float64) string {
	whole, frac := math.Modf(value)
	nsec := int64(math.Round(frac * 1_000_000_000))
	if nsec >= 1_000_000_000 {
		whole++
		nsec -= 1_000_000_000
	}
	return notestime.Format(time.Unix(int64(whole)+appleUnixOffset, nsec))
}

func ChangedSince(prev, next map[string]string) map[string]bool {
	out := map[string]bool{}
	for id, value := range next {
		if prev[id] != value {
			out[id] = true
		}
	}
	return out
}
