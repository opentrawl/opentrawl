package gogcrawl

import (
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
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

func projectOpenPresentation(value archive.OpenResult) *presentationv1.PresentationDocument {
	record := projectOpenRecord(value)
	title := strings.TrimSpace(record.Headers.Subject)
	if title == "" {
		title = "(no subject)"
	}
	fields := []*presentationv1.Field{{Label: "Ref", Display: record.Ref}}
	if from := formatPresentationAddress(record.Headers.GetFromName(), record.Headers.GetFromAddress()); from != "" {
		fields = append(fields, &presentationv1.Field{Label: "From", Display: from})
	}
	if value := strings.TrimSpace(record.Headers.ToAddress); value != "" {
		fields = append(fields, &presentationv1.Field{Label: "To", Display: value})
	}
	if value := strings.TrimSpace(record.Headers.GetCcAddress()); value != "" {
		fields = append(fields, &presentationv1.Field{Label: "Cc", Display: value})
	}
	if value := strings.TrimSpace(record.Time); value != "" {
		fields = append(fields, &presentationv1.Field{Label: "Date", Display: value})
	}
	if labels := joinPresentationStrings(record.Labels); labels != "" {
		fields = append(fields, &presentationv1.Field{Label: "Labels", Display: labels})
	}
	fields = append(fields, &presentationv1.Field{Label: "Unread", Display: formatPresentationBool(record.Unread)})
	blocks := []*presentationv1.Block{{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: fields}}}}
	if body := strings.TrimSpace(record.Body); body != "" {
		blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: body}}})
	}
	rows := make([]*presentationv1.Row, 0, len(record.Attachments))
	for _, attachment := range record.Attachments {
		rows = append(rows, &presentationv1.Row{Role: presentationv1.Row_ROLE_NORMAL, Cells: []*presentationv1.Cell{{Display: attachment.Filename}, {Display: attachment.MimeType}, {Display: formatPresentationBytes(attachment.Size)}}})
	}
	blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Table{Table: &presentationv1.Table{Columns: []string{"File", "Type", "Bytes"}, Rows: rows}}})
	document := &presentationv1.PresentationDocument{Title: title, Blocks: blocks}
	if record.BodyTruncated {
		document.Facts = append(document.Facts, &presentationv1.Fact{Kind: presentationv1.Fact_KIND_TRUNCATION, Message: fmt.Sprintf("Message body is truncated; %d characters omitted.", record.GetBodyElidedChars())})
	}
	return document
}

func formatPresentationAddress(name, address string) string {
	name = strings.TrimSpace(name)
	address = strings.TrimSpace(address)
	if name != "" && address != "" {
		return name + " <" + address + ">"
	}
	if name != "" {
		return name
	}
	return address
}

func joinPresentationStrings(values []string) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			items = append(items, value)
		}
	}
	return strings.Join(items, ", ")
}

func formatPresentationBool(value bool) string {
	if value {
		return "Yes"
	}
	return "No"
}

func formatPresentationBytes(value int64) string { return fmt.Sprintf("%d bytes", value) }
