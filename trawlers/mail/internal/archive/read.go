package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/mailparse"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func (s *Store) CountMessagesStillInAppleMail(ctx context.Context) (int64, error) {
	var count int64
	err := s.store.DB().QueryRowContext(ctx, `select count(*) from messages where deleted_from_source_at = ''`).Scan(&count)
	return count, err
}

type MailSearchOptions struct {
	SearchText                     string
	ResultLimit                    int
	SearchTotalIsLowerBoundAtLimit bool
	ReceivedAtOrAfter              time.Time
	ReceivedAtOrBefore             time.Time
	UnresolvedPersonFilterText     string
	ExactPersonFilterIdentifiers   []*person.ExactPersonFilterIdentifier
}

type MailSearchHit struct {
	MailMessageRecordReference  string
	MessageReceivedTime         time.Time
	SenderDisplayText           string
	EmailSubject                string
	AppleMailMailboxName        string
	AppleMailAccountDisplayName string
	MatchingTextSnippet         string
	FirstMatchingFieldName      string
}

type MailSearchResult struct {
	MailSearchHitsInDisplayOrder   []MailSearchHit
	TotalSearchMatches             int64
	TotalSearchMatchesIsLowerBound bool
	MoreSearchMatchesExist         bool
}

// SearchMessages searches messages that Apple Mail still has. A message
// deleted from Apple Mail stays openable by its link but leaves search.
func (s *Store) SearchMessages(ctx context.Context, options MailSearchOptions) (MailSearchResult, error) {
	searchText := strings.TrimSpace(options.SearchText)
	personFilterEnabled, participantKeys, err := s.personFilterParticipantKeys(ctx, options.UnresolvedPersonFilterText, options.ExactPersonFilterIdentifiers)
	if err != nil {
		return MailSearchResult{}, err
	}
	if searchText == "" && !personFilterEnabled && options.ReceivedAtOrAfter.IsZero() && options.ReceivedAtOrBefore.IsZero() {
		return MailSearchResult{}, errors.New("search text is required")
	}
	fromClause := "from messages m"
	clauses := []string{"m.deleted_from_source_at = ''"}
	var arguments []any
	matchColumns := `'' as subject_match, '' as body_match`
	if searchText != "" {
		fromClause = "from messages_fts join messages m on m.id = messages_fts.id"
		clauses = append(clauses, "messages_fts match ?")
		ftsQuery := ckstore.FTS5TokenQuery(searchText)
		if ftsQuery == "" {
			ftsQuery = ckstore.FTS5Phrase(searchText)
		}
		arguments = append(arguments, ftsQuery)
		matchColumns = ckstore.FTS5MarkedSearchResultSnippetSQLExpression("messages_fts", 1) + ", " +
			ckstore.FTS5MarkedSearchResultSnippetSQLExpression("messages_fts", 2)
	}
	if !options.ReceivedAtOrAfter.IsZero() {
		clauses = append(clauses, "m.time_unix >= ?")
		arguments = append(arguments, options.ReceivedAtOrAfter.Unix())
	}
	if !options.ReceivedAtOrBefore.IsZero() {
		clauses = append(clauses, "m.time_unix <= ?")
		arguments = append(arguments, options.ReceivedAtOrBefore.Unix())
	}
	if personFilterEnabled {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(participantKeys)), ",")
		if placeholders == "" {
			placeholders = "null"
		}
		clauses = append(clauses, `exists (select 1 from message_participants mp where mp.message_id = m.id and mp.participant_key in (`+placeholders+`))`)
		for _, key := range participantKeys {
			arguments = append(arguments, key)
		}
	}
	whereClause := "where " + strings.Join(clauses, " and ")
	result := MailSearchResult{}
	limit := max(options.ResultLimit, 0)
	boundedTotal := options.SearchTotalIsLowerBoundAtLimit && limit > 0
	if !boundedTotal {
		if err := s.store.DB().QueryRowContext(ctx, "select count(*) "+fromClause+" "+whereClause, arguments...).Scan(&result.TotalSearchMatches); err != nil {
			return MailSearchResult{}, fmt.Errorf("count matching messages: %w", err)
		}
	}
	query := `select m.id, m.time_unix, m.from_name, m.from_address, m.subject, m.body, m.apple_mail_mailbox_name,
       coalesce((select apple_mail_account_display_name from apple_mail_accounts account
                 where account.apple_mail_account_identifier = m.apple_mail_account_identifier), ''), ` + matchColumns + "\n" +
		fromClause + "\n" + whereClause + "\norder by m.time_unix desc, m.id desc"
	queryArguments := arguments
	if limit > 0 {
		query += "\nlimit ?"
		queryArguments = append(append([]any(nil), arguments...), limit+1)
	}
	ownerAddresses, err := s.OwnerEmailAddresses(ctx)
	if err != nil {
		return MailSearchResult{}, err
	}
	rows, err := s.store.DB().QueryContext(ctx, query, queryArguments...)
	if err != nil {
		return MailSearchResult{}, fmt.Errorf("search messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var identifier ArchivedMailMessageIdentifier
		var receivedUnix int64
		var fromName, fromAddress, subject, body, mailboxName, accountDisplayName, subjectMatch, bodyMatch string
		if err := rows.Scan(&identifier, &receivedUnix, &fromName, &fromAddress, &subject, &body, &mailboxName, &accountDisplayName, &subjectMatch, &bodyMatch); err != nil {
			return MailSearchResult{}, err
		}
		hit := MailSearchHit{
			MailMessageRecordReference:  MailMessageRecordReference(identifier),
			SenderDisplayText:           senderDisplayText(fromName, fromAddress, ownerAddresses),
			EmailSubject:                subject,
			AppleMailMailboxName:        mailboxName,
			AppleMailAccountDisplayName: accountDisplayName,
			MatchingTextSnippet:         ckstore.FTS5Snippet(body, searchText),
			FirstMatchingFieldName:      "subject",
		}
		if receivedUnix != 0 {
			hit.MessageReceivedTime = time.Unix(receivedUnix, 0)
		}
		if len(ckstore.ParseFTS5MarkedText(subjectMatch)) == 0 && len(ckstore.ParseFTS5MarkedText(bodyMatch)) > 0 {
			hit.FirstMatchingFieldName = "body"
		}
		result.MailSearchHitsInDisplayOrder = append(result.MailSearchHitsInDisplayOrder, hit)
	}
	if err := rows.Err(); err != nil {
		return MailSearchResult{}, err
	}
	if limit > 0 && len(result.MailSearchHitsInDisplayOrder) > limit {
		result.MailSearchHitsInDisplayOrder = result.MailSearchHitsInDisplayOrder[:limit]
		result.MoreSearchMatchesExist = true
		if boundedTotal {
			result.TotalSearchMatches = int64(limit + 1)
			result.TotalSearchMatchesIsLowerBound = true
		}
	} else if boundedTotal {
		result.TotalSearchMatches = int64(len(result.MailSearchHitsInDisplayOrder))
	}
	return result, nil
}

func senderDisplayText(fromName, fromAddress string, ownerAddresses map[mailparse.NormalizedEmailAddress]struct{}) string {
	if _, isOwner := ownerAddresses[mailparse.NormalizedEmailAddress(fromAddress)]; isOwner && fromAddress != "" {
		return "me (" + fromAddress + ")"
	}
	if strings.TrimSpace(fromName) != "" {
		return fromName
	}
	if fromAddress != "" {
		return fromAddress
	}
	return "unknown sender"
}

type OpenedMailMessage struct {
	MailMessageRecordReference    string
	AppleMailAccountDisplayName   string
	AppleMailMailboxName          string
	MessageReceivedTime           time.Time
	SenderDisplayName             string
	SenderEmailAddress            string
	RecipientEmailAddresses       string
	CopiedRecipientEmailAddresses string
	ReplyToEmailAddresses         string
	MailingListIdentifier         string
	RFC5322MessageIdentifier      string
	InReplyToMessageIdentifiers   string
	EmailSubject                  string
	EmailBodyText                 string
	MessageIsUnread               bool
	ArchivedMailAttachments       []ArchivedMailAttachment
	DeletedFromAppleMailTime      time.Time
}

func (s *Store) OpenMessage(ctx context.Context, mailMessageRecordReference string) (OpenedMailMessage, error) {
	identifier, err := parseMailMessageRecordReference(mailMessageRecordReference)
	if err != nil {
		return OpenedMailMessage{}, err
	}
	opened := OpenedMailMessage{MailMessageRecordReference: MailMessageRecordReference(identifier)}
	var receivedUnix int64
	var deletedFromSourceAt string
	err = s.store.DB().QueryRowContext(ctx, `
select coalesce(account.apple_mail_account_display_name, ''), m.apple_mail_mailbox_name, m.time_unix,
       m.from_name, m.from_address, m.to_address, m.cc_address, m.reply_to_address, m.mailing_list_identifier,
       m.rfc5322_message_identifier, m.in_reply_to_message_identifiers, m.subject, m.body, m.is_unread, m.deleted_from_source_at
from messages m
left join apple_mail_accounts account on account.apple_mail_account_identifier = m.apple_mail_account_identifier
where m.id = ?`, identifier).Scan(
		&opened.AppleMailAccountDisplayName, &opened.AppleMailMailboxName, &receivedUnix,
		&opened.SenderDisplayName, &opened.SenderEmailAddress, &opened.RecipientEmailAddresses, &opened.CopiedRecipientEmailAddresses,
		&opened.ReplyToEmailAddresses, &opened.MailingListIdentifier, &opened.RFC5322MessageIdentifier, &opened.InReplyToMessageIdentifiers,
		&opened.EmailSubject, &opened.EmailBodyText, &opened.MessageIsUnread, &deletedFromSourceAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return OpenedMailMessage{}, fmt.Errorf("message not found: %s", mailMessageRecordReference)
	}
	if err != nil {
		return OpenedMailMessage{}, err
	}
	if receivedUnix != 0 {
		opened.MessageReceivedTime = time.Unix(receivedUnix, 0)
	}
	if deletedFromSourceAt != "" {
		if opened.DeletedFromAppleMailTime, err = time.Parse(time.RFC3339, deletedFromSourceAt); err != nil {
			return OpenedMailMessage{}, fmt.Errorf("invalid deleted from source time %q: %w", deletedFromSourceAt, err)
		}
	}
	rows, err := s.store.DB().QueryContext(ctx, `select filename, mime_type, size_bytes from attachments where message_id = ? order by id`, identifier)
	if err != nil {
		return OpenedMailMessage{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var attachment ArchivedMailAttachment
		var byteCount sql.NullInt64
		if err := rows.Scan(&attachment.AttachmentFilename, &attachment.AttachmentMediaType, &byteCount); err != nil {
			return OpenedMailMessage{}, err
		}
		if byteCount.Valid {
			attachment.AttachmentByteCount = &byteCount.Int64
		}
		opened.ArchivedMailAttachments = append(opened.ArchivedMailAttachments, attachment)
	}
	return opened, rows.Err()
}
