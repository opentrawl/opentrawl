package render

import (
	"fmt"
	"io"
	"strings"
	"time"
)

func ShortLocalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

func writeListIntro(w io.Writer, heading string, hints []string) error {
	for _, line := range Wrap(strings.TrimSpace(heading), OutputWidth(w)) {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	for _, hint := range hints {
		hint = strings.TrimSpace(hint)
		if hint == "" {
			continue
		}
		if err := WriteTrawlCommandHint(w, hint); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}
