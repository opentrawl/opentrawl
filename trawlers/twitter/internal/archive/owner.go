package archive

import (
	"encoding/json"
	"errors"
	"fmt"
)

// owner is the account that generated the dump. tweets.js carries no author
// fields (the archive is implicitly the owner's), so account.js is the only
// source of identity for authored tweets.
type owner struct {
	ID     string
	Handle string
	Name   string
}

type accountWrapper struct {
	Account rawAccount `json:"account"`
}

type rawAccount struct {
	AccountID   string `json:"accountId"`
	Username    string `json:"username"`
	DisplayName string `json:"accountDisplayName"`
}

func parseAccount(data []byte) (owner, error) {
	body, err := unwrapYTD(data)
	if err != nil {
		return owner{}, err
	}
	var wrapped []accountWrapper
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return owner{}, fmt.Errorf("parse account.js: %w", err)
	}
	if len(wrapped) == 0 {
		return owner{}, errors.New("account.js contained no account record")
	}
	acct := wrapped[0].Account
	if acct.AccountID == "" || acct.Username == "" {
		return owner{}, errors.New("account.js record is missing accountId or username")
	}
	return owner{ID: acct.AccountID, Handle: acct.Username, Name: acct.DisplayName}, nil
}
