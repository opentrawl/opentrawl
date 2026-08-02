package archive

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

func (s *Store) SetPersonRelationshipOrContextDescription(
	ctx context.Context,
	personIdentifier string,
	personRelationshipOrContextDescription model.PersonRelationshipOrContextDescription,
	personRelationshipOrContextDescriptionStatedDate model.PersonRelationshipOrContextDescriptionStatedDate,
) (string, error) {
	personIdentifier = strings.TrimSpace(personIdentifier)
	personRelationshipOrContextDescription = model.PersonRelationshipOrContextDescription(
		strings.TrimSpace(string(personRelationshipOrContextDescription)),
	)
	if personIdentifier == "" {
		return "", fmt.Errorf("person id is required")
	}
	if personRelationshipOrContextDescription == "" {
		return "", fmt.Errorf("person relationship or context description cannot be empty")
	}
	if personRelationshipOrContextDescriptionStatedDate.IsZero() {
		return "", fmt.Errorf("person relationship or context description stated date is required")
	}
	storedPersonRelationshipOrContextDescriptionStatedDate, err :=
		storedPersonRelationshipOrContextDescriptionStatedDateText(
			personRelationshipOrContextDescriptionStatedDate,
		)
	if err != nil {
		return "", err
	}
	result, err := s.database().ExecContext(ctx, `
update people
set person_relationship_or_context_description = ?,
    person_relationship_or_context_description_stated_date = ?
where id = ?`,
		personRelationshipOrContextDescription,
		storedPersonRelationshipOrContextDescriptionStatedDate,
		personIdentifier,
	)
	if err != nil {
		return "", err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if changed == 0 {
		return "", fmt.Errorf("person not found: %s", personIdentifier)
	}
	return personIdentifier, nil
}

func storedPersonRelationshipOrContextDescriptionStatedDateText(
	date model.PersonRelationshipOrContextDescriptionStatedDate,
) (string, error) {
	if date.IsZero() {
		return "", nil
	}
	storedDateText := fmt.Sprintf(
		"%04d-%02d-%02d",
		date.CalendarYear,
		date.CalendarMonthNumber,
		date.CalendarDayOfMonth,
	)
	if _, err := time.Parse("2006-01-02", storedDateText); err != nil {
		return "", fmt.Errorf("person relationship or context description stated date: %w", err)
	}
	return storedDateText, nil
}

func personRelationshipOrContextDescriptionStatedDateFromStoredText(
	storedDateText string,
) (model.PersonRelationshipOrContextDescriptionStatedDate, error) {
	storedDateText = strings.TrimSpace(storedDateText)
	if storedDateText == "" {
		return model.PersonRelationshipOrContextDescriptionStatedDate{}, nil
	}
	parsedDate, err := time.Parse("2006-01-02", storedDateText)
	if err != nil {
		return model.PersonRelationshipOrContextDescriptionStatedDate{}, fmt.Errorf(
			"parse person relationship or context description stated date: %w",
			err,
		)
	}
	return model.PersonRelationshipOrContextDescriptionStatedDate{
		CalendarYear:        int32(parsedDate.Year()),
		CalendarMonthNumber: int32(parsedDate.Month()),
		CalendarDayOfMonth:  int32(parsedDate.Day()),
	}, nil
}
