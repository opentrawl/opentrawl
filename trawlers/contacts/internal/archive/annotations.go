package archive

import (
	"context"
	"fmt"
	"strings"
)

func (s *Store) AnnotatePerson(ctx context.Context, personID, annotation, statedAt string) (string, error) {
	personID = strings.TrimSpace(personID)
	annotation = strings.TrimSpace(annotation)
	statedAt = strings.TrimSpace(statedAt)
	if personID == "" {
		return "", fmt.Errorf("person id is required")
	}
	if annotation == "" {
		return "", fmt.Errorf("annotation cannot be empty")
	}
	if statedAt == "" {
		return "", fmt.Errorf("annotation stated date is required")
	}
	result, err := s.store.DB().ExecContext(ctx, `
update people
set annotation = ?, annotation_stated_at = ?
where id = ?`, annotation, statedAt, personID)
	if err != nil {
		return "", err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if changed == 0 {
		return "", fmt.Errorf("person not found: %s", personID)
	}
	return personID, nil
}
