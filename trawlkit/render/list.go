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
	if _, err := fmt.Fprintln(w, strings.TrimSpace(heading)); err != nil {
		return err
	}
	for _, hint := range hints {
		hint = strings.TrimSpace(hint)
		if hint == "" {
			continue
		}
		if _, err := fmt.Fprintln(w, hint); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}
