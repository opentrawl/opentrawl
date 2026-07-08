package notestime

import "time"

const Layout = "2006-01-02T15:04:05.000000000Z07:00"

func Format(value time.Time) string {
	return value.UTC().Format(Layout)
}
