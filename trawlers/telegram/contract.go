package telegram

import "github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"

const (
	defaultSearchLimit = 20
	openContextRadius  = 10
)

type contactsEnvelope struct {
	Contacts []store.Contact
	Total    int
}

type foldersEnvelope struct {
	Folders []store.Folder
}
