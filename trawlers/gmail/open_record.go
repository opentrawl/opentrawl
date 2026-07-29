package gmail

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/gmail/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	gmailopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/gmail/open/v1"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ trawlkit.RecordOpener = (*Crawler)(nil)

func (c *Crawler) OpenRecord(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, ref string) (*openv1.OpenRecord, error) {
	value, err := c.loadOpenMessage(ctx, req, ref)
	if err != nil {
		return nil, err
	}
	if err := validateOpenTimestamps(value); err != nil {
		return nil, err
	}
	machine := projectOpenRecord(value)
	data, err := anypb.New(machine)
	if err != nil {
		return nil, err
	}
	record := &openv1.OpenRecord{
		RegisteredTrawlerManifestIdentity: c.RegisteredTrawlerDeclaration().RegisteredTrawlerManifestIdentity,
		CanonicalOpenedRecordReference:    machine.GetRef(),
		TypedOpenedRecord: &openv1.OpenRecord_TrawlerSpecificOpenedRecord{
			TrawlerSpecificOpenedRecord: &openv1.TrawlerSpecificOpenedRecord{
				TypedTrawlerSpecificOpenedRecord:              data,
				TrawlerSpecificOpenedRecordDetailPresentation: projectOpenDetailPresentation(value),
			},
		},
	}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

func validateOpenTimestamps(value archive.OpenResult) error {
	return presentation.ValidateTimestamps(value.Time)
}

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

func projectOpenDetailPresentation(value archive.OpenResult) *presentationv1.TrawlerSpecificCommandDetailPresentation {
	record := projectOpenRecord(value)
	title := strings.TrimSpace(record.Headers.Subject)
	if title == "" {
		title = "(no subject)"
	}
	fields := make([]*presentationv1.TrawlerSpecificCommandDetailPresentationField, 0, 6+len(record.Attachments))
	if from := formatPresentationAddress(record.Headers.GetFromName(), record.Headers.GetFromAddress()); from != "" {
		fields = append(fields, gmailDetailTextField("From", from, ""))
	}
	if value := strings.TrimSpace(record.Headers.ToAddress); value != "" {
		fields = append(fields, gmailDetailTextField("To", value, ""))
	}
	if value := strings.TrimSpace(record.Headers.GetCcAddress()); value != "" {
		fields = append(fields, gmailDetailTextField("Cc", value, ""))
	}
	if value := strings.TrimSpace(record.Time); value != "" {
		parsedTime, _ := time.Parse(time.RFC3339Nano, value)
		fields = append(fields, gmailDetailExactTimeField("Date", parsedTime))
	}
	if labels := joinPresentationStrings(record.Labels); labels != "" {
		fields = append(fields, gmailDetailTextField("Labels", labels, ""))
	}
	fields = append(fields, gmailDetailTextField("Unread", formatPresentationBool(record.Unread), ""))
	for index, attachment := range record.Attachments {
		attachmentDescription := strings.Join(compactPresentationValues(
			attachment.Filename,
			attachment.MimeType,
			presentation.Bytes(attachment.Size),
		), " · ")
		fields = append(fields, gmailDetailTextField("Attachment", attachmentDescription, attachmentAnchorID(index)))
	}
	titleAnchorIdentifier := "subject"
	detail := &presentationv1.TrawlerSpecificCommandDetailPresentation{
		DetailDisplayName:                      title,
		DetailDisplayNameFixedAnchorIdentifier: &titleAnchorIdentifier,
		FieldsInDisplayOrder:                   fields,
	}
	if body := strings.TrimSpace(record.Body); body != "" {
		bodyAnchorIdentifier := "body"
		detail.Body = &presentationv1.TrawlerSpecificCommandDetailPresentation_BodyText{BodyText: body}
		detail.BodyFixedAnchorIdentifier = &bodyAnchorIdentifier
	}
	if record.BodyTruncated {
		detail.FieldsInDisplayOrder = append(detail.FieldsInDisplayOrder,
			gmailDetailTextField("Body", fmt.Sprintf("%d characters omitted", record.GetBodyElidedChars()), ""),
		)
	}
	return detail
}

func gmailDetailTextField(fieldDisplayName, textValue, fixedAnchorIdentifier string) *presentationv1.TrawlerSpecificCommandDetailPresentationField {
	field := &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentationv1.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_Text{Text: textValue},
		},
	}
	if fixedAnchorIdentifier != "" {
		field.FieldFixedAnchorIdentifier = &fixedAnchorIdentifier
	}
	return field
}

func gmailDetailExactTimeField(fieldDisplayName string, exactTime time.Time) *presentationv1.TrawlerSpecificCommandDetailPresentationField {
	return &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue: &presentationv1.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTimeForDisplay: &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
					ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
						ExactTime: timestamppb.New(exactTime),
					},
				},
			},
		},
	}
}

func compactPresentationValues(values ...string) []string {
	compacted := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			compacted = append(compacted, value)
		}
	}
	return compacted
}

func attachmentAnchorID(index int) string {
	return fmt.Sprintf("attachment-%d", index+1)
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
