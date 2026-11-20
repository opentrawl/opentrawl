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
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/mailparse"
	"github.com/opentrawl/opentrawl/trawlkit/state"
)

const messageIngestHashVersion = "mail-decode-v7:mailparse-" + mailparse.EmailParserRevision

// backupEntityType keys each ingested backup shard in the shared update_state
// store: entity_id is the shard path, value its ingest hash. A shard whose
// stored hash differs (or is absent) is re-ingested; the ingest itself upserts
// by message id, so a dropped or reset store re-derives in one update.
const backupEntityType = "backup"

func (s *Store) PendingBackupShards(ctx context.Context, shards []BackupShard) ([]BackupShard, error) {
	st := state.New(s.store.DB())
	var pending []BackupShard
	for _, shard := range shards {
		rec, ok, err := st.Get(ctx, sourceName, backupEntityType, shard.Path)
		if err != nil {
			return nil, err
		}
		if !ok || rec.Value != expectedIngestHash(shard) {
			pending = append(pending, shard)
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
			var acceptedGmailMessageIdentifiers []string
			result.Seen, result.Inserted, acceptedGmailMessageIdentifiers, result.ParseElapsed, result.IndexElapsed, err = ingestMessages(ctx, tx, plaintext)
			if err == nil {
				err = assignShortReferencesForGmailMessageIdentifiersUsingCallerOwnedSQLTransaction(
					ctx,
					tx,
					acceptedGmailMessageIdentifiers,
				)
			}
		default:
			err = fmt.Errorf("unsupported backup shard kind %q", shard.Kind)
		}
		if err != nil {
			return err
		}
		return state.New(tx).Set(ctx, sourceName, backupEntityType, shard.Path, expectedIngestHash(shard))
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

func ingestMessages(ctx context.Context, tx *sql.Tx, data []byte) (int, int, []string, time.Duration, time.Duration, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	seen := 0
	inserted := 0
	var acceptedGmailMessageIdentifiers []string
	var parseElapsed time.Duration
	var indexElapsed time.Duration
	for {
		var row backupMessageRow
		if err := dec.Decode(&row); errors.Is(err, io.EOF) {
			return seen, inserted, acceptedGmailMessageIdentifiers, parseElapsed, indexElapsed, nil
		} else if err != nil {
			return seen, inserted, acceptedGmailMessageIdentifiers, parseElapsed, indexElapsed, fmt.Errorf("decode message row: %w", err)
		}
		parseStarted := time.Now()
		msg, err := row.message()
		parseElapsed += time.Since(parseStarted)
		if err != nil {
			return seen, inserted, acceptedGmailMessageIdentifiers, parseElapsed, indexElapsed, err
		}
		insertedResult, err := insertMessageWithTiming(ctx, tx, msg)
		if err != nil {
			return seen, inserted, acceptedGmailMessageIdentifiers, parseElapsed, indexElapsed, err
		}
		acceptedGmailMessageIdentifiers = append(acceptedGmailMessageIdentifiers, msg.ID)
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
	// mailparse degrades a malformed header line or MIME part on its own,
	// so one odd message is archived with everything that did parse.
	parsed := mailparse.ParseRawEmailMessage(raw)
	// A rare message carries no internalDate; fall back to the RFC822
	// Date header, else archive it dateless. One odd message must
	// never halt the crawl.
	when := time.Time{}
	internalDateMS, err := parseInternalDateMS(r.InternalDate)
	if err == nil {
		when = time.UnixMilli(internalDateMS)
	} else if !parsed.DateHeaderTime.IsZero() {
		when = parsed.DateHeaderTime
		internalDateMS = parsed.DateHeaderTime.UnixMilli()
	} else {
		internalDateMS = 0
	}
	return Message{
		ID:             id,
		ThreadID:       strings.TrimSpace(r.ThreadID),
		HistoryID:      strings.TrimSpace(r.HistoryID),
		InternalDateMS: internalDateMS,
		Time:           when,
		FromName:       parsed.SenderEmailAddress.DisplayName,
		FromAddress:    string(parsed.SenderEmailAddress.NormalizedEmailAddress),
		ToAddress:      mailparse.FormatEmailAddressList(parsed.RecipientEmailAddresses),
		CcAddress:      mailparse.FormatEmailAddressList(parsed.CopiedRecipientEmailAddresses),
		Subject:        parsed.EmailSubject,
		Body:           parsed.EmailBodyText,
		Labels:         append([]string(nil), r.LabelIDs...),
		Attachments:    gmailAttachments(parsed.EmailAttachments),
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

func gmailAttachments(parsedAttachments []mailparse.EmailAttachment) []Attachment {
	attachments := make([]Attachment, 0, len(parsedAttachments))
	for _, parsedAttachment := range parsedAttachments {
		attachments = append(attachments, Attachment{
			Filename: parsedAttachment.AttachmentFilename,
			MIMEType: parsedAttachment.AttachmentMediaType,
			Size:     parsedAttachment.AttachmentDecodedByteCount,
		})
	}
	return attachments
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
