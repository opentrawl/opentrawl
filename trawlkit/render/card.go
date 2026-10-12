package render

import (
	"io"
	"strings"
)

// A card shows one opened record: a title, labelled fields, an optional body
// and the next commands.
type cardField struct {
	label string
	value string
}

type card struct {
	title        string
	fields       []cardField
	body         string
	nextCommands []NextCommand
}

func writeCard(writer io.Writer, openedRecordCard card) error {
	wroteTitleOrField := false
	if title := strings.TrimSpace(openedRecordCard.title); title != "" {
		if err := writeLines(writer, Wrap(title, OutputWidth(writer))); err != nil {
			return err
		}
		wroteTitleOrField = true
	}
	for _, field := range openedRecordCard.fields {
		label := displayLabel(field.label)
		value := strings.TrimSpace(field.value)
		if label == "" || value == "" {
			continue
		}
		if err := writeWrappedField(writer, label, value); err != nil {
			return err
		}
		wroteTitleOrField = true
	}
	if body := strings.TrimSpace(openedRecordCard.body); body != "" {
		if wroteTitleOrField {
			if err := writeLine(writer, ""); err != nil {
				return err
			}
		}
		if err := writeLines(writer, Wrap(body, OutputWidth(writer))); err != nil {
			return err
		}
	}
	return writeNextCommands(writer, openedRecordCard.nextCommands)
}
