package photos

import (
	"database/sql"
	"testing"
)

func TestCoreDataTimeRetainsFractionalSeconds(t *testing.T) {
	got := coreDataTime(sql.NullFloat64{Float64: 1.25, Valid: true})
	if got != "2001-01-01T00:00:01.25Z" {
		t.Fatalf("coreDataTime = %q", got)
	}
}
