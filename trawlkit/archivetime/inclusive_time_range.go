package archivetime

import "time"

// InclusiveTimeRange represents an inclusive UTC time window shared across
// unified search, semantic search, and trawler-native SQL stores so every
// surface applies identical >= after and <= before boundary semantics.
type InclusiveTimeRange struct {
	After  time.Time
	Before time.Time
}

func NewInclusiveTimeRange(after, before time.Time) InclusiveTimeRange {
	return InclusiveTimeRange{
		After:  after,
		Before: before,
	}
}

func InclusiveTimeRangeFromUnixBounds(afterUnix, beforeUnix *int64) InclusiveTimeRange {
	var r InclusiveTimeRange
	if afterUnix != nil {
		r.After = time.Unix(*afterUnix, 0).UTC()
	}
	if beforeUnix != nil {
		r.Before = time.Unix(*beforeUnix, 0).UTC()
	}
	return r
}

func (r InclusiveTimeRange) IsEmpty() bool {
	return r.After.IsZero() && r.Before.IsZero()
}

func (r InclusiveTimeRange) ContainsTime(t time.Time) bool {
	if r.IsEmpty() {
		return true
	}
	if t.IsZero() {
		return false
	}
	if !r.After.IsZero() && t.Before(r.After) {
		return false
	}
	if !r.Before.IsZero() && t.After(r.Before) {
		return false
	}
	return true
}

func (r InclusiveTimeRange) ContainsUnixSeconds(unixSeconds int64) bool {
	if r.IsEmpty() {
		return true
	}
	if unixSeconds == 0 {
		return false
	}
	if !r.After.IsZero() && unixSeconds < r.After.Unix() {
		return false
	}
	if !r.Before.IsZero() && unixSeconds > r.Before.Unix() {
		return false
	}
	return true
}

func (r InclusiveTimeRange) AppendUnixSQLClauses(column string, where *[]string, args *[]any) {
	if !r.After.IsZero() {
		*where = append(*where, column+" >= ?")
		*args = append(*args, r.After.Unix())
	}
	if !r.Before.IsZero() {
		*where = append(*where, column+" <= ?")
		*args = append(*args, r.Before.Unix())
	}
}

const fixedNanosecondUTCWithoutZoneFormat = "2006-01-02T15:04:05.000000000"
const wholeSecondUTCFormat = "2006-01-02T15:04:05"

func normalizedRFC3339UTCColumnExpression(column string) string {
	return "substr(case when instr(" + column + ", '.') > 0 then rtrim(" + column + ", 'Z') else rtrim(" + column + ", 'Z') || '.' end || '000000000', 1, 29)"
}

// AppendRFC3339UTCSQLClauses appends both an indexable coarse string bound and
// an exact nanosecond-padded chronological bound for SQLite columns storing
// RFC3339 / RFC3339Nano UTC strings (where raw string comparison fails because
// '.' sorts before 'Z').
func (r InclusiveTimeRange) AppendRFC3339UTCSQLClauses(
	column string,
	where *[]string,
	args *[]any,
) {
	normalizedExpr := normalizedRFC3339UTCColumnExpression(column)
	if !r.After.IsZero() {
		afterUTC := r.After.UTC()
		*where = append(*where, column+" >= ?")
		*args = append(*args, afterUTC.Format(wholeSecondUTCFormat))
		*where = append(*where, normalizedExpr+" >= ?")
		*args = append(*args, afterUTC.Format(fixedNanosecondUTCWithoutZoneFormat))
	}
	if !r.Before.IsZero() {
		beforeUTC := r.Before.UTC()
		*where = append(*where, column+" <= ?")
		*args = append(*args, beforeUTC.Format(wholeSecondUTCFormat)+"Z")
		*where = append(*where, normalizedExpr+" <= ?")
		*args = append(*args, beforeUTC.Format(fixedNanosecondUTCWithoutZoneFormat))
	}
}
