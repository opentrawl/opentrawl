package gogcrawl

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/gog"
	"github.com/opentrawl/opentrawl/trawlkit/control"
)

func (c *Crawler) exportContacts(ctx context.Context) ([]control.Contact, error) {
	var out []control.Contact
	pageToken := ""
	for {
		page, err := c.gog.Contacts(ctx, gog.DefaultPageSize, pageToken)
		if err != nil {
			return nil, err
		}
		for _, contact := range page.Contacts {
			name := strings.TrimSpace(contact.Name)
			phone := strings.TrimSpace(contact.Phone)
			if name == "" || phone == "" {
				continue
			}
			out = append(out, control.Contact{DisplayName: name, PhoneNumbers: []string{phone}})
		}
		if page.NextPageToken == "" {
			return out, nil
		}
		pageToken = page.NextPageToken
	}
}
