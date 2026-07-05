package main

import (
	"fmt"
	"strconv"
	"strings"
)

type IssueIdentifier struct {
	TeamKey string
	Number  int
}

func ParseIssueIdentifier(raw string) (IssueIdentifier, error) {
	text := strings.TrimSpace(raw)
	sep := strings.LastIndex(text, "-")
	if sep <= 0 || sep == len(text)-1 {
		return IssueIdentifier{}, fmt.Errorf("issue must look like TRAWL-99")
	}
	team := strings.ToUpper(strings.TrimSpace(text[:sep]))
	numberText := strings.TrimSpace(text[sep+1:])
	number, err := strconv.Atoi(numberText)
	if err != nil || number < 1 {
		return IssueIdentifier{}, fmt.Errorf("issue number must be a positive integer")
	}
	for _, r := range team {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return IssueIdentifier{}, fmt.Errorf("team key must use letters and numbers")
		}
	}
	return IssueIdentifier{TeamKey: team, Number: number}, nil
}

func (id IssueIdentifier) String() string {
	return fmt.Sprintf("%s-%d", id.TeamKey, id.Number)
}
