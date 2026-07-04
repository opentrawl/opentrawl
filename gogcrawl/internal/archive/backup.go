package archive

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const messageIngestHashVersion = "mail-decode-v6"

func (s *Store) PendingBackupShards(ctx context.Context, shards []BackupShard) ([]BackupShard, error) {
	var pending []BackupShard
	for _, shard := range shards {
		var hash string
		err := s.store.DB().QueryRowContext(ctx, `select hash from ingested_shards where path = ?`, shard.Path).Scan(&hash)
		if err == sql.ErrNoRows || (err == nil && hash != expectedIngestHash(shard)) {
			pending = append(pending, shard)
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	return pending, nil
}

func (s *Store) IngestBackupShard(ctx context.Context, shard BackupShard, plaintext []byte) (IngestResult, error) {
	result := IngestResult{Shard: shard}
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		switch shard.Kind {
		case BackupShardLabels:
			started := time.Now()
			result.Labels, err = ingestLabels(ctx, tx, plaintext)
			result.ParseElapsed = time.Since(started)
		case BackupShardMessages:
			result.Seen, result.Inserted, result.ParseElapsed, result.IndexElapsed, err = ingestMessages(ctx, tx, plaintext)
		default:
			err = fmt.Errorf("unsupported backup shard kind %q", shard.Kind)
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
insert into ingested_shards(path, hash, kind, rows, ingested_at)
values (?, ?, ?, ?, ?)
on conflict(path) do update set
  hash = excluded.hash,
  kind = excluded.kind,
  rows = excluded.rows,
  ingested_at = excluded.ingested_at
`, shard.Path, expectedIngestHash(shard), string(shard.Kind), result.Seen+result.Labels, time.Now().Local().Format(time.RFC3339))
		return err
	})
	return result, err
}

func expectedIngestHash(shard BackupShard) string {
	if shard.Kind == BackupShardMessages {
		return messageIngestHashVersion + ":" + shard.Hash
	}
	return shard.Hash
}

func ingestLabels(ctx context.Context, tx *sql.Tx, data []byte) (int, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	count := 0
	for {
		var raw map[string]any
		if err := dec.Decode(&raw); errors.Is(err, io.EOF) {
			return count, nil
		} else if err != nil {
			return count, fmt.Errorf("decode label row: %w", err)
		}
		id := manifestString(raw["id"])
		if id == "" {
			continue
		}
		row, err := json.Marshal(raw)
		if err != nil {
			return count, err
		}
		_, err = tx.ExecContext(ctx, `
insert into gmail_labels(id, name, type, raw_json)
values (?, ?, ?, ?)
on conflict(id) do update set
  name = excluded.name,
  type = excluded.type,
  raw_json = excluded.raw_json
`, id, manifestString(raw["name"]), manifestString(raw["type"]), string(row))
		if err != nil {
			return count, fmt.Errorf("insert label: %w", err)
		}
		count++
	}
}

func ingestMessages(ctx context.Context, tx *sql.Tx, data []byte) (int, int, time.Duration, time.Duration, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	seen := 0
	inserted := 0
	var parseElapsed time.Duration
	var indexElapsed time.Duration
	for {
		var row backupMessageRow
		if err := dec.Decode(&row); errors.Is(err, io.EOF) {
			return seen, inserted, parseElapsed, indexElapsed, nil
		} else if err != nil {
			return seen, inserted, parseElapsed, indexElapsed, fmt.Errorf("decode message row: %w", err)
		}
		parseStarted := time.Now()
		msg, err := row.message()
		parseElapsed += time.Since(parseStarted)
		if err != nil {
			return seen, inserted, parseElapsed, indexElapsed, err
		}
		insertedResult, err := insertMessageWithTiming(ctx, tx, msg)
		if err != nil {
			return seen, inserted, parseElapsed, indexElapsed, err
		}
		indexElapsed += insertedResult.IndexElapsed
		seen++
		if insertedResult.Inserted {
			inserted++
		}
	}
}

type backupMessageRow struct {
	ID           string          `json:"id"`
	ThreadID     string          `json:"threadId"`
	HistoryID    string          `json:"historyId"`
	InternalDate json.RawMessage `json:"internalDate"`
	LabelIDs     []string        `json:"labelIds"`
	SizeEstimate int64           `json:"sizeEstimate"`
	Raw          string          `json:"raw"`
}

func (r backupMessageRow) message() (Message, error) {
	id := strings.TrimSpace(r.ID)
	if id == "" {
		return Message{}, fmt.Errorf("message row has no id")
	}
	raw, err := decodeRawMessage(r.Raw)
	if err != nil {
		return Message{}, fmt.Errorf("message %s raw: %w", id, err)
	}
	// Real mailboxes contain mail with garbage headers (spam with HTML
	// where headers belong). A message that will not parse is archived
	// degraded — id, time, size — never dropped, never fatal.
	parsed, parseErr := parseRawMail(raw)
	if parseErr != nil {
		parsed = parsedMail{Subject: "(unparseable message)"}
	}
	// A rare message carries no internalDate; fall back to the RFC822
	// Date header, else archive it dateless. One odd message must
	// never halt the crawl.
	when := time.Time{}
	internalDateMS, err := parseInternalDateMS(r.InternalDate)
	if err == nil {
		when = time.UnixMilli(internalDateMS)
	} else if !parsed.Date.IsZero() {
		when = parsed.Date
		internalDateMS = parsed.Date.UnixMilli()
	} else {
		internalDateMS = 0
	}
	return Message{
		ID:             id,
		ThreadID:       strings.TrimSpace(r.ThreadID),
		HistoryID:      strings.TrimSpace(r.HistoryID),
		InternalDateMS: internalDateMS,
		Time:           when,
		FromName:       parsed.FromName,
		FromAddress:    parsed.FromAddress,
		ToAddress:      parsed.ToAddress,
		CcAddress:      parsed.CcAddress,
		Subject:        parsed.Subject,
		Body:           parsed.Body,
		Labels:         append([]string(nil), r.LabelIDs...),
		Attachments:    parsed.Attachments,
	}, nil
}

func parseInternalDateMS(raw json.RawMessage) (int64, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return 0, fmt.Errorf("missing")
	}
	if strings.HasPrefix(text, `"`) {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, err
		}
		return strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, err
	}
	return number.Int64()
}

func decodeRawMessage(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	for _, enc := range []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	} {
		data, err := enc.DecodeString(value)
		if err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("base64url decode failed")
}

type parsedMail struct {
	Date        time.Time
	FromName    string
	FromAddress string
	ToAddress   string
	CcAddress   string
	Subject     string
	Body        string
	Attachments []Attachment
}

func parseRawMail(raw []byte) (parsedMail, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return parsedMail{}, err
	}
	fromName, fromAddress := parseAddressHeader(msg.Header.Get("From"))
	date, _ := msg.Header.Date()
	textParts, attachments, err := parseEntity(msg.Header, msg.Body)
	if err != nil {
		return parsedMail{}, err
	}
	bodyParts := textParts.Plain
	if len(bodyParts) == 0 {
		bodyParts = htmlTextParts(textParts.HTML)
	}
	return parsedMail{
		Date:        date,
		FromName:    fromName,
		FromAddress: fromAddress,
		ToAddress:   parseAddressListHeader(msg.Header.Get("To")),
		CcAddress:   parseAddressListHeader(msg.Header.Get("Cc")),
		Subject:     decodeHeader(msg.Header.Get("Subject")),
		Body:        flattenWhitespace(strings.Join(bodyParts, "\n\n")),
		Attachments: attachments,
	}, nil
}

type mailTextParts struct {
	Plain []string
	HTML  []string
}

type mailHeader interface {
	Get(string) string
}

func parseEntity(header mailHeader, body io.Reader) (mailTextParts, []Attachment, error) {
	contentType := header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || strings.TrimSpace(mediaType) == "" {
		mediaType = "text/plain"
		params = map[string]string{}
	}
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return mailTextParts{}, nil, fmt.Errorf("multipart message has no boundary")
		}
		reader := multipart.NewReader(body, boundary)
		var texts mailTextParts
		var attachments []Attachment
		for {
			part, err := reader.NextRawPart()
			if errors.Is(err, io.EOF) {
				return texts, attachments, nil
			}
			if err != nil {
				return mailTextParts{}, nil, err
			}
			partTexts, partAttachments, err := parseEntity(part.Header, part)
			_ = part.Close()
			if err != nil {
				return mailTextParts{}, nil, err
			}
			texts.Plain = append(texts.Plain, partTexts.Plain...)
			texts.HTML = append(texts.HTML, partTexts.HTML...)
			attachments = append(attachments, partAttachments...)
		}
	}
	decoded, err := io.ReadAll(decodeTransfer(body, header.Get("Content-Transfer-Encoding")))
	if err != nil {
		return mailTextParts{}, nil, err
	}
	disposition, dispositionParams, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	filename := dispositionParams["filename"]
	if filename == "" {
		filename = params["name"]
	}
	filename = decodeHeader(filename)
	if strings.EqualFold(disposition, "attachment") || strings.TrimSpace(filename) != "" {
		return mailTextParts{}, []Attachment{{
			Filename: strings.TrimSpace(filename),
			MIMEType: mediaType,
			Size:     int64(len(decoded)),
		}}, nil
	}
	switch mediaType {
	case "text/plain":
		return mailTextParts{Plain: []string{decodeMessageTextPart(decoded, params["charset"])}}, nil, nil
	case "text/html":
		return mailTextParts{HTML: []string{decodeMessageTextPart(decoded, params["charset"])}}, nil, nil
	}
	return mailTextParts{}, nil, nil
}

func decodeMessageTextPart(decoded []byte, charset string) string {
	text := decodeTextPart(decoded, charset)
	return decodeResidualQuotedPrintableText(text)
}

func decodeTransfer(body io.Reader, encoding string) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		return quotedprintable.NewReader(body)
	default:
		return body
	}
}

// decodeResidualQuotedPrintableText handles the sender bug class where a
// part declares 7bit/US-ASCII but its content carries quoted-printable
// escapes. It runs AFTER charset conversion, so the escapes encode raw
// bytes whose real charset the sender never declared — require the result
// to be valid UTF-8, else keep the original.
func decodeResidualQuotedPrintableText(text string) string {
	data := []byte(text)
	hexEscapes, distinctHexEscapes := quotedPrintableHexEscapeStats(data)
	if !hasQuotedPrintableSoftBreak(data) && distinctHexEscapes < 3 {
		return text
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
	if err != nil {
		return text
	}
	if !utf8.Valid(decoded) {
		return text
	}
	if decodedHexEscapes, _ := quotedPrintableHexEscapeStats(decoded); decodedHexEscapes >= hexEscapes {
		return text
	}
	return string(decoded)
}

func hasQuotedPrintableSoftBreak(data []byte) bool {
	return bytes.Contains(data, []byte("=\r\n")) || bytes.Contains(data, []byte("=\n"))
}

func quotedPrintableHexEscapeStats(data []byte) (int, int) {
	var seen [256]bool
	total := 0
	distinct := 0
	for i := 0; i+2 < len(data); i++ {
		if data[i] != '=' || !isHex(data[i+1]) || !isHex(data[i+2]) {
			continue
		}
		total++
		value := hexValue(data[i+1])<<4 | hexValue(data[i+2])
		if !seen[value] {
			seen[value] = true
			distinct++
		}
	}
	return total, distinct
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'A' && b <= 'F') || (b >= 'a' && b <= 'f')
}

func hexValue(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10
	default:
		return b - 'a' + 10
	}
}

func manifestString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}
