package index

import (
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/avatar"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/markdown"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

func (s Store) RepairAvatarMetadata(person model.Person, now time.Time) (model.Person, bool, error) {
	p, changed, err := avatar.RepairMetadata(person, now)
	if err != nil {
		p = avatar.Clear(person)
		p.UpdatedAt = now.UTC()
		if writeErr := markdown.WritePerson(p.Path, p); writeErr != nil {
			return model.Person{}, false, writeErr
		}
		return p, true, nil
	}
	if !changed {
		return p, false, nil
	}
	if err := markdown.WritePerson(p.Path, p); err != nil {
		return model.Person{}, false, err
	}
	return p, true, nil
}
