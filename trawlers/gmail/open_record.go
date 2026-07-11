package gogcrawl

import (
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	gmailopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/gmail/open/v1"
)

func projectOpenRecord(value archive.OpenResult) *gmailopenv1.GmailRecord {
	record := &gmailopenv1.GmailRecord{
		Ref:      value.Ref,
		Id:       value.ID,
		ThreadId: value.ThreadID,
		Time:     value.Time,
		Headers: &gmailopenv1.Headers{
			ToAddress: value.Headers.ToAddress,
			Subject:   value.Headers.Subject,
		},
		Labels:        append([]string(nil), value.Labels...),
		Unread:        value.Unread,
		Attachments:   make([]*gmailopenv1.Attachment, 0, len(value.Attachments)),
		Body:          value.Body,
		BodyTruncated: value.BodyTruncated,
	}
	setOptionalString(&record.Headers.FromName, value.Headers.FromName)
	setOptionalString(&record.Headers.FromAddress, value.Headers.FromAddress)
	setOptionalString(&record.Headers.CcAddress, value.Headers.CcAddress)
	for _, attachment := range value.Attachments {
		record.Attachments = append(record.Attachments, &gmailopenv1.Attachment{
			Filename: attachment.Filename,
			MimeType: attachment.MIMEType,
			Size:     attachment.Size,
		})
	}
	if value.BodyElidedChars != 0 {
		elided := int64(value.BodyElidedChars)
		record.BodyElidedChars = &elided
	}
	return record
}

func setOptionalString(target **string, value string) {
	if value != "" {
		*target = &value
	}
}
