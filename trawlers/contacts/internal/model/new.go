package model

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

func NewPerson(name string, now time.Time) Person {
	return Person{
		ID:        "person_" + uuid.NewString(),
		Name:      strings.TrimSpace(name),
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}
}

func NewNote(personID, kind, source, body string, occurredAt, now time.Time, topics []string) Note {
	if occurredAt.IsZero() {
		occurredAt = now
	}
	return Note{
		ID:         "note_" + uuid.NewString(),
		PersonID:   personID,
		OccurredAt: occurredAt.UTC(),
		CapturedAt: now.UTC(),
		Kind:       strings.TrimSpace(kind),
		Source:     strings.TrimSpace(source),
		Confidence: "high",
		Privacy:    "normal",
		Topics:     topics,
		Body:       body,
	}
}
