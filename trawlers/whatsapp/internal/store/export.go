package store

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store/storedb"
)

func (s *Store) Contacts(ctx context.Context) ([]Contact, error) {
	return s.exportContacts(ctx)
}

func (s *Store) exportContacts(ctx context.Context) ([]Contact, error) {
	rows, err := s.q.ExportContacts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Contact, 0, len(rows))
	for _, row := range rows {
		out = append(out, contactFromRow(row))
	}
	return out, nil
}

func contactFromRow(row storedb.ExportContactsRow) Contact {
	return Contact{
		JID:          row.Jid,
		Phone:        row.Phone,
		FullName:     row.FullName,
		FirstName:    row.FirstName,
		LastName:     row.LastName,
		BusinessName: row.BusinessName,
		Username:     row.Username,
		LID:          row.Lid,
		AboutText:    row.AboutText,
		UpdatedAt:    fromUnix(row.UpdatedAt),
	}
}
