package render

import (
	"fmt"
	"io"
	"strings"
)

type CardField struct {
	Label string
	Value string
}

type Card struct {
	Title  string
	Fields []CardField
	Body   string
	Hints  []string
}

func WriteCard(w io.Writer, c Card) error {
	wrote := false
	if title := strings.TrimSpace(c.Title); title != "" {
		if _, err := fmt.Fprintln(w, title); err != nil {
			return err
		}
		wrote = true
	}
	for _, field := range c.Fields {
		label := strings.TrimSpace(field.Label)
		value := strings.TrimSpace(field.Value)
		if label == "" || value == "" {
			continue
		}
		if err := WriteWrappedField(w, label, value); err != nil {
			return err
		}
		wrote = true
	}
	body := strings.TrimSpace(c.Body)
	if body != "" {
		if wrote {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		for _, line := range Wrap(body, OutputWidth(w)) {
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
		wrote = true
	}
	return writeCardHints(w, c.Hints, wrote)
}

func writeCardHints(w io.Writer, hints []string, wrote bool) error {
	clean := make([]string, 0, len(hints))
	for _, hint := range hints {
		if hint = strings.TrimSpace(hint); hint != "" {
			clean = append(clean, hint)
		}
	}
	if len(clean) == 0 {
		return nil
	}
	if wrote {
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	for _, hint := range clean {
		if _, err := fmt.Fprintln(w, hint); err != nil {
			return err
		}
	}
	return nil
}
