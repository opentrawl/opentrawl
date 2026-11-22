// Package mailparse turns raw RFC 5322 email bytes into typed header values,
// line-structured body text and attachment metadata. It is shared by the
// Gmail and Apple Mail trawlers. One malformed header line or MIME part
// degrades only that line or part; parsing never fails as a whole.
package mailparse

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// EmailParserRevision names this parser's output. A trawler that keeps raw
// originals stores it beside each parsed message and parses the original
// again when it changes. Change it whenever parsed output changes.
const EmailParserRevision = "2026-11-22.2"

// EmailMessageIdentifier is one RFC 5322 msg-id without its angle brackets.
type EmailMessageIdentifier string

// MailingListIdentifier is the list-id from an RFC 2919 List-Id header,
// without its angle brackets.
type MailingListIdentifier string

type ParsedEmailMessage struct {
	EmailMessageIdentifier            EmailMessageIdentifier
	InReplyToEmailMessageIdentifiers  []EmailMessageIdentifier
	ReferencedEmailMessageIdentifiers []EmailMessageIdentifier
	SenderEmailAddress                EmailAddress
	ReplyToEmailAddresses             []EmailAddress
	RecipientEmailAddresses           []EmailAddress
	CopiedRecipientEmailAddresses     []EmailAddress
	DeliveredToEmailAddresses         []NormalizedEmailAddress
	MailingListIdentifier             MailingListIdentifier
	EmailSubject                      string
	DateHeaderTime                    time.Time
	EmailBodyText                     string
	EmailAttachments                  []EmailAttachment
}

type EmailAttachment struct {
	AttachmentFilename         string
	AttachmentMediaType        string
	AttachmentDecodedByteCount int64
	// AttachmentMIMESectionPath is the IMAP body section number, such as
	// "2" or "2.10". Apple Mail names detached attachment folders by it.
	AttachmentMIMESectionPath string
}

func ParseRawEmailMessage(rawEmailMessage []byte) ParsedEmailMessage {
	header, body := readEmailHeaderBlockAndBody(rawEmailMessage)
	parsed := ParsedEmailMessage{
		EmailMessageIdentifier:            firstEmailMessageIdentifier(header.Get("Message-Id")),
		InReplyToEmailMessageIdentifiers:  emailMessageIdentifiers(header.Get("In-Reply-To")),
		ReferencedEmailMessageIdentifiers: emailMessageIdentifiers(strings.Join(header.Values("References"), " ")),
		SenderEmailAddress:                ParseEmailAddress(header.Get("From")),
		ReplyToEmailAddresses:             ParseEmailAddressList(header.Get("Reply-To")),
		RecipientEmailAddresses:           ParseEmailAddressList(strings.Join(header.Values("To"), ", ")),
		CopiedRecipientEmailAddresses:     ParseEmailAddressList(strings.Join(header.Values("Cc"), ", ")),
		DeliveredToEmailAddresses:         deliveredToEmailAddresses(header.Values("Delivered-To")),
		MailingListIdentifier:             mailingListIdentifier(header.Get("List-Id")),
		EmailSubject:                      DecodeEmailHeaderText(header.Get("Subject")),
	}
	if dateHeaderTime, err := mail.ParseDate(strings.TrimSpace(header.Get("Date"))); err == nil {
		parsed.DateHeaderTime = dateHeaderTime
	}
	walk := emailMimeEntityWalk{}
	bodyText, _ := walk.entityText(header, body, "")
	parsed.EmailBodyText = NormalizeEmailBodyTextLineStructure(bodyText)
	parsed.EmailAttachments = walk.attachments
	return parsed
}

// readEmailHeaderBlockAndBody reads header fields leniently: a line without a
// colon is skipped instead of discarding every header after it.
func readEmailHeaderBlockAndBody(rawEmailMessage []byte) (textproto.MIMEHeader, []byte) {
	headerBlock, body := splitEmailHeaderBlockFromBody(rawEmailMessage)
	header := textproto.MIMEHeader{}
	var fieldName string
	var fieldValue strings.Builder
	flush := func() {
		if fieldName != "" {
			header.Add(fieldName, strings.TrimSpace(fieldValue.String()))
		}
		fieldName = ""
		fieldValue.Reset()
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(headerBlock), "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if fieldName != "" {
				fieldValue.WriteByte(' ')
				fieldValue.WriteString(strings.TrimSpace(line))
			}
			continue
		}
		flush()
		name, value, found := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !found || name == "" || strings.ContainsAny(name, " \t") {
			continue
		}
		fieldName = textproto.CanonicalMIMEHeaderKey(name)
		fieldValue.WriteString(value)
	}
	flush()
	return header, body
}

func splitEmailHeaderBlockFromBody(rawEmailMessage []byte) ([]byte, []byte) {
	for index := 0; index < len(rawEmailMessage); index++ {
		if rawEmailMessage[index] != '\n' {
			continue
		}
		rest := rawEmailMessage[index+1:]
		if bytes.HasPrefix(rest, []byte("\n")) {
			return rawEmailMessage[:index], rest[1:]
		}
		if bytes.HasPrefix(rest, []byte("\r\n")) {
			return rawEmailMessage[:index], rest[2:]
		}
	}
	return rawEmailMessage, nil
}

type emailMimeEntityWalk struct {
	attachments []EmailAttachment
}

// entityText returns the readable text of one MIME entity and whether it came
// from a text/plain part. multipart/alternative prefers its plain part; other
// multiparts join the text of every part in order.
func (walk *emailMimeEntityWalk) entityText(header textproto.MIMEHeader, body []byte, sectionPath string) (string, bool) {
	mediaType, mediaTypeParameters, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || strings.TrimSpace(mediaType) == "" {
		mediaType, mediaTypeParameters = "text/plain", map[string]string{}
	}
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "multipart/") && mediaTypeParameters["boundary"] != "" {
		return walk.multipartText(mediaType, mediaTypeParameters["boundary"], body, sectionPath)
	}
	if sectionPath == "" {
		sectionPath = "1"
	}
	decodedBody := decodeContentTransferEncoding(body, header.Get("Content-Transfer-Encoding"))
	disposition, dispositionParameters, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	filename := dispositionParameters["filename"]
	if filename == "" {
		filename = mediaTypeParameters["name"]
	}
	filename = strings.TrimSpace(DecodeEmailHeaderText(filename))
	if strings.EqualFold(disposition, "attachment") || filename != "" {
		walk.attachments = append(walk.attachments, EmailAttachment{
			AttachmentFilename:         filename,
			AttachmentMediaType:        mediaType,
			AttachmentDecodedByteCount: int64(len(decodedBody)),
			AttachmentMIMESectionPath:  sectionPath,
		})
		return "", false
	}
	switch mediaType {
	case "text/plain":
		plainText := decodeEmailTextPart(decodedBody, mediaTypeParameters["charset"])
		if plainTextPartContainsHTMLDocument(plainText) {
			return extractPlainTextPartWithHTMLDocument(plainText), false
		}
		return plainText, true
	case "text/html":
		return htmlEmailPartText(decodeEmailTextPart(decodedBody, mediaTypeParameters["charset"])), false
	case "message/rfc822", "message/global":
		return walk.forwardedEmailMessageText(decodedBody, sectionPath), false
	}
	return "", false
}

func (walk *emailMimeEntityWalk) multipartText(mediaType, boundary string, body []byte, sectionPath string) (string, bool) {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	type partText struct {
		text      string
		fromPlain bool
	}
	var partTexts []partText
	for partNumber := 1; ; partNumber++ {
		part, err := reader.NextRawPart()
		if err != nil {
			break
		}
		partBody, _ := io.ReadAll(part)
		_ = part.Close()
		childSectionPath := strconv.Itoa(partNumber)
		if sectionPath != "" {
			childSectionPath = sectionPath + "." + childSectionPath
		}
		text, fromPlain := walk.entityText(part.Header, partBody, childSectionPath)
		if strings.TrimSpace(text) != "" {
			partTexts = append(partTexts, partText{text: text, fromPlain: fromPlain})
		}
	}
	if mediaType == "multipart/alternative" {
		for _, candidate := range partTexts {
			if candidate.fromPlain {
				return candidate.text, true
			}
		}
		if len(partTexts) > 0 {
			return partTexts[0].text, false
		}
		return "", false
	}
	texts := make([]string, 0, len(partTexts))
	allFromPlain := len(partTexts) > 0
	for _, candidate := range partTexts {
		texts = append(texts, candidate.text)
		allFromPlain = allFromPlain && candidate.fromPlain
	}
	return strings.Join(texts, "\n\n"), allFromPlain
}

// forwardedEmailMessageText keeps a forwarded message readable in the outer
// body: its own From, Date, Subject and To lines, then its text.
func (walk *emailMimeEntityWalk) forwardedEmailMessageText(forwardedEmailMessage []byte, sectionPath string) string {
	header, body := readEmailHeaderBlockAndBody(forwardedEmailMessage)
	lines := []string{"Forwarded message"}
	for _, fieldName := range []string{"From", "Date", "Subject", "To"} {
		if value := DecodeEmailHeaderText(header.Get(fieldName)); value != "" {
			lines = append(lines, fieldName+": "+value)
		}
	}
	text, _ := walk.entityText(header, body, sectionPath)
	return strings.Join(lines, "\n") + "\n\n" + text
}

// decodeContentTransferEncoding returns as much decoded content as the part
// allows: a broken base64 or quoted-printable tail keeps what came before it.
func decodeContentTransferEncoding(body []byte, contentTransferEncoding string) []byte {
	var decoder io.Reader
	switch strings.ToLower(strings.TrimSpace(contentTransferEncoding)) {
	case "base64":
		decoder = base64.NewDecoder(base64.StdEncoding, bytes.NewReader(body))
	case "quoted-printable":
		decoder = quotedprintable.NewReader(bytes.NewReader(body))
	default:
		return body
	}
	decoded, _ := io.ReadAll(decoder)
	return decoded
}

func emailMessageIdentifiers(headerValue string) []EmailMessageIdentifier {
	var identifiers []EmailMessageIdentifier
	rest := headerValue
	for {
		start := strings.IndexByte(rest, '<')
		if start < 0 {
			break
		}
		end := strings.IndexByte(rest[start:], '>')
		if end < 0 {
			break
		}
		if identifier := strings.TrimSpace(rest[start+1 : start+end]); identifier != "" {
			identifiers = append(identifiers, EmailMessageIdentifier(identifier))
		}
		rest = rest[start+end+1:]
	}
	if len(identifiers) == 0 {
		if identifier := strings.TrimSpace(headerValue); identifier != "" && !strings.ContainsAny(identifier, " \t") {
			identifiers = append(identifiers, EmailMessageIdentifier(identifier))
		}
	}
	return identifiers
}

func firstEmailMessageIdentifier(headerValue string) EmailMessageIdentifier {
	identifiers := emailMessageIdentifiers(headerValue)
	if len(identifiers) == 0 {
		return ""
	}
	return identifiers[0]
}

func deliveredToEmailAddresses(headerValues []string) []NormalizedEmailAddress {
	var addresses []NormalizedEmailAddress
	for _, headerValue := range headerValues {
		if address := ParseEmailAddress(headerValue).NormalizedEmailAddress; address != "" {
			addresses = append(addresses, address)
		}
	}
	return addresses
}

func mailingListIdentifier(headerValue string) MailingListIdentifier {
	headerValue = DecodeEmailHeaderText(headerValue)
	if start, end := strings.LastIndexByte(headerValue, '<'), strings.LastIndexByte(headerValue, '>'); start >= 0 && end > start {
		return MailingListIdentifier(strings.TrimSpace(headerValue[start+1 : end]))
	}
	return MailingListIdentifier(strings.TrimSpace(headerValue))
}

// JoinEmailMessageIdentifiers writes identifiers in header form, each in
// angle brackets and separated by one space.
func JoinEmailMessageIdentifiers(identifiers []EmailMessageIdentifier) string {
	written := make([]string, 0, len(identifiers))
	for _, identifier := range identifiers {
		written = append(written, "<"+string(identifier)+">")
	}
	return strings.Join(written, " ")
}
