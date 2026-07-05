package index

import (
	"time"

	"github.com/openclaw/clawdex/internal/avatar"
	"github.com/openclaw/clawdex/internal/markdown"
	"github.com/openclaw/clawdex/internal/model"
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
