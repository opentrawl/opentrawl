package openrecord

import (
	"fmt"
	"net/url"
	"strings"

	calendarevent "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar_event"
	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	note "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/note"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

const (
	PersonDisplayNameAnchorID            = "person_display_name"
	PersonAlternativeDisplayNameAnchorID = "alternative_person_display_name"
	PersonEmailAddressAnchorID           = "email_address"
	PersonPhoneNumberAnchorID            = "phone_number"
	PersonPostalAddressAnchorID          = "postal_address"
	PersonAccountIdentifierAnchorID      = "account_identifier"
)

func ValidHTTPSURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

func Validate(record *open.OpenRecord) error {
	if record == nil {
		return fmt.Errorf("open record is missing")
	}
	registeredTrawler := record.GetRecordTrawler()
	if registeredTrawlerIdentityText(registeredTrawler) == "" {
		return fmt.Errorf("registered trawler identity is empty")
	}
	canonicalOpenedRecordReference := record.GetCanonicalRecordReference()
	if err := validateTrawlerOwnedRecordReference(
		registeredTrawler,
		canonicalOpenedRecordReference,
		"canonical opened record reference",
	); err != nil {
		return err
	}
	switch typedOpenedRecord := record.GetTypedOpenedRecord().(type) {
	case *open.OpenRecord_OpenedMessageRecordWithConversationContext:
		return validateOpenedMessageRecordWithConversationContext(
			registeredTrawler,
			canonicalOpenedRecordReference,
			typedOpenedRecord.OpenedMessageRecordWithConversationContext,
		)
	case *open.OpenRecord_ConversationRecord:
		return validateConversationRecord(
			registeredTrawler,
			canonicalOpenedRecordReference,
			typedOpenedRecord.ConversationRecord,
		)
	case *open.OpenRecord_PersonRecord:
		return validatePersonRecord(
			registeredTrawler,
			canonicalOpenedRecordReference,
			typedOpenedRecord.PersonRecord,
		)
	case *open.OpenRecord_CalendarEventRecord:
		return validateCalendarEventRecord(
			registeredTrawler,
			canonicalOpenedRecordReference,
			typedOpenedRecord.CalendarEventRecord,
		)
	case *open.OpenRecord_OpenedNoteRecord:
		return validateOpenedNoteRecord(
			registeredTrawler,
			canonicalOpenedRecordReference,
			typedOpenedRecord.OpenedNoteRecord,
		)
	case *open.OpenRecord_TrawlerSpecificOpenedRecordPresentation:
		return validateTrawlerSpecificOpenedRecordPresentation(typedOpenedRecord.TrawlerSpecificOpenedRecordPresentation)
	default:
		return fmt.Errorf("open record has no typed record")
	}
}

func ValidateRequestedAnchor(
	record *open.OpenRecord,
	requestedAnchor *identity.RecordAnchorIdentifier,
) error {
	if err := Validate(record); err != nil {
		return err
	}
	requestedAnchorIdentifier := recordAnchorIdentifierText(requestedAnchor)
	if requestedAnchorIdentifier == "" {
		return fmt.Errorf("requested anchor identifier is empty")
	}
	switch typedOpenedRecord := record.GetTypedOpenedRecord().(type) {
	case *open.OpenRecord_OpenedMessageRecordWithConversationContext:
		if !recordAnchorIdentifiersEqual(
			typedOpenedRecord.OpenedMessageRecordWithConversationContext.GetOpenedMessageRecordAnchor(),
			requestedAnchor,
		) {
			return fmt.Errorf("opened message does not contain requested anchor %q", requestedAnchorIdentifier)
		}
	case *open.OpenRecord_PersonRecord:
		if !personRecordContainsAnchor(typedOpenedRecord.PersonRecord, requestedAnchor) {
			return fmt.Errorf("person record does not contain requested anchor %q", requestedAnchorIdentifier)
		}
	case *open.OpenRecord_OpenedNoteRecord:
		if !openedNoteRecordContainsAnchor(typedOpenedRecord.OpenedNoteRecord, requestedAnchor) {
			return fmt.Errorf("opened note does not contain requested anchor %q", requestedAnchorIdentifier)
		}
	case *open.OpenRecord_TrawlerSpecificOpenedRecordPresentation:
		if !trawlerSpecificOpenedRecordPresentationContainsAnchor(
			typedOpenedRecord.TrawlerSpecificOpenedRecordPresentation,
			requestedAnchor,
		) {
			return fmt.Errorf("opened record does not contain requested anchor %q", requestedAnchorIdentifier)
		}
	default:
		return fmt.Errorf("opened record does not contain requested anchor %q", requestedAnchorIdentifier)
	}
	return nil
}

func validateOpenedNoteRecord(
	registeredTrawler *identity.RegisteredTrawlerIdentity,
	canonicalOpenedRecordReference *identity.CanonicalArchiveRecordReference,
	openedNoteRecord *note.OpenedNoteRecord,
) error {
	if openedNoteRecord == nil {
		return fmt.Errorf("opened note record is missing")
	}
	expectedOpenedRecordReference := openedNoteRecord.GetCanonicalNoteRecordReference()
	if openedNoteRecord.GetSpecificRecoveredNoteVersionWasOpened() {
		expectedOpenedRecordReference = openedNoteRecord.GetCanonicalOpenedNoteVersionRecordReference()
	}
	if !canonicalArchiveRecordReferencesEqual(expectedOpenedRecordReference, canonicalOpenedRecordReference) {
		return fmt.Errorf("canonical opened note record reference does not match the opened record")
	}
	if err := validateTrawlerOwnedRecordReference(
		registeredTrawler,
		openedNoteRecord.GetCanonicalNoteRecordReference(),
		"canonical note record reference",
	); err != nil {
		return err
	}
	return validateTrawlerOwnedRecordReference(
		registeredTrawler,
		openedNoteRecord.GetCanonicalOpenedNoteVersionRecordReference(),
		"canonical opened note version record reference",
	)
}

func openedNoteRecordContainsAnchor(
	openedNoteRecord *note.OpenedNoteRecord,
	requestedAnchor *identity.RecordAnchorIdentifier,
) bool {
	if openedNoteRecord == nil {
		return false
	}
	return recordAnchorIdentifiersEqual(openedNoteRecord.GetNoteDisplayNameAnchor(), requestedAnchor) ||
		recordAnchorIdentifiersEqual(openedNoteRecord.GetOpenedNoteBodyAnchor(), requestedAnchor)
}

func validateOpenedMessageRecordWithConversationContext(
	registeredTrawler *identity.RegisteredTrawlerIdentity,
	canonicalOpenedRecordReference *identity.CanonicalArchiveRecordReference,
	openedMessage *message.OpenedMessageRecordWithConversationContext,
) error {
	if openedMessage == nil {
		return fmt.Errorf("opened message record is missing")
	}
	if !canonicalArchiveRecordReferencesEqual(
		openedMessage.GetOpenedMessageRecordReference(),
		canonicalOpenedRecordReference,
	) {
		return fmt.Errorf("canonical opened message record reference does not match the opened record")
	}
	if err := validateTrawlerOwnedRecordReference(
		registeredTrawler,
		openedMessage.GetConversationRecordReference(),
		"canonical conversation record reference",
	); err != nil {
		return err
	}
	openedMessageCount := 0
	for _, messageRecord := range openedMessage.GetConversationContextMessageRecordsInDisplayOrder() {
		if messageRecord != nil &&
			canonicalArchiveRecordReferencesEqual(
				messageRecord.GetCanonicalRecordReference(),
				canonicalOpenedRecordReference,
			) {
			openedMessageCount++
		}
	}
	if openedMessageCount != 1 {
		return fmt.Errorf("opened message occurs %d times in conversation context", openedMessageCount)
	}
	return nil
}

func validateConversationRecord(
	registeredTrawler *identity.RegisteredTrawlerIdentity,
	canonicalOpenedRecordReference *identity.CanonicalArchiveRecordReference,
	conversationRecord *conversation.ConversationRecord,
) error {
	if conversationRecord == nil {
		return fmt.Errorf("conversation record is missing")
	}
	canonicalConversationRecordReference := conversationRecord.GetCanonicalRecordReference()
	if !canonicalArchiveRecordReferencesEqual(
		canonicalConversationRecordReference,
		canonicalOpenedRecordReference,
	) {
		return fmt.Errorf("canonical conversation record reference does not match the opened record")
	}
	return validateTrawlerOwnedRecordReference(
		registeredTrawler,
		canonicalConversationRecordReference,
		"canonical conversation record reference",
	)
}

func validatePersonRecord(
	registeredTrawler *identity.RegisteredTrawlerIdentity,
	canonicalOpenedRecordReference *identity.CanonicalArchiveRecordReference,
	personRecord *person.PersonRecord,
) error {
	if personRecord == nil {
		return fmt.Errorf("person record is missing")
	}
	canonicalPersonRecordReference := personRecord.GetCanonicalRecordReference()
	if !canonicalArchiveRecordReferencesEqual(
		canonicalPersonRecordReference,
		canonicalOpenedRecordReference,
	) {
		return fmt.Errorf("canonical person record reference does not match the opened record")
	}
	if err := validateTrawlerOwnedRecordReference(
		registeredTrawler,
		canonicalPersonRecordReference,
		"canonical person record reference",
	); err != nil {
		return err
	}
	if strings.TrimSpace(personRecord.GetPersonDisplayName()) == "" {
		return fmt.Errorf("person display name is empty")
	}
	for contactMethodIndex, contactMethod := range personRecord.GetPersonContactMethodsInDisplayOrder() {
		if contactMethod == nil {
			return fmt.Errorf("person contact method %d is missing", contactMethodIndex+1)
		}
		if contactMethod.GetPersonContactMethodKind() == person.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_UNSPECIFIED {
			return fmt.Errorf("person contact method %d kind is unspecified", contactMethodIndex+1)
		}
		if strings.TrimSpace(contactMethod.GetPersonContactMethodDisplayValue()) == "" {
			return fmt.Errorf("person contact method %d display value is empty", contactMethodIndex+1)
		}
	}
	return nil
}

func validateCalendarEventRecord(
	registeredTrawler *identity.RegisteredTrawlerIdentity,
	canonicalOpenedRecordReference *identity.CanonicalArchiveRecordReference,
	calendarEventRecord *calendarevent.CalendarEventRecord,
) error {
	if calendarEventRecord == nil {
		return fmt.Errorf("calendar event record is missing")
	}
	canonicalCalendarEventRecordReference := calendarEventRecord.GetCanonicalRecordReference()
	if !canonicalArchiveRecordReferencesEqual(
		canonicalCalendarEventRecordReference,
		canonicalOpenedRecordReference,
	) {
		return fmt.Errorf("canonical calendar event record reference does not match the opened record")
	}
	return validateTrawlerOwnedRecordReference(
		registeredTrawler,
		canonicalCalendarEventRecordReference,
		"canonical calendar event record reference",
	)
}

func validateTrawlerSpecificOpenedRecordPresentation(openedRecord *open.TrawlerSpecificOpenedRecordPresentation) error {
	if openedRecord == nil {
		return fmt.Errorf("trawler-specific opened record is missing")
	}
	if openedRecord.GetDetailPresentation() == nil {
		return fmt.Errorf("trawler-specific opened record detail presentation is missing")
	}
	return nil
}

func personRecordContainsAnchor(
	personRecord *person.PersonRecord,
	requestedAnchor *identity.RecordAnchorIdentifier,
) bool {
	if personRecord == nil {
		return false
	}
	requestedAnchorIdentifier := recordAnchorIdentifierText(requestedAnchor)
	switch requestedAnchorIdentifier {
	case PersonDisplayNameAnchorID:
		return strings.TrimSpace(personRecord.GetPersonDisplayName()) != ""
	case PersonAlternativeDisplayNameAnchorID:
		return len(personRecord.GetAlternativePersonDisplayNames()) > 0
	}
	var wantedContactMethodKind person.PersonContactMethodKind
	switch requestedAnchorIdentifier {
	case PersonEmailAddressAnchorID:
		wantedContactMethodKind = person.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_EMAIL_ADDRESS
	case PersonPhoneNumberAnchorID:
		wantedContactMethodKind = person.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_PHONE_NUMBER
	case PersonPostalAddressAnchorID:
		wantedContactMethodKind = person.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_POSTAL_ADDRESS
	case PersonAccountIdentifierAnchorID:
		wantedContactMethodKind = person.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_ACCOUNT_IDENTIFIER
	default:
		return false
	}
	for _, contactMethod := range personRecord.GetPersonContactMethodsInDisplayOrder() {
		if contactMethod != nil && contactMethod.GetPersonContactMethodKind() == wantedContactMethodKind {
			return true
		}
	}
	return false
}

func trawlerSpecificOpenedRecordPresentationContainsAnchor(
	openedRecord *open.TrawlerSpecificOpenedRecordPresentation,
	requestedAnchor *identity.RecordAnchorIdentifier,
) bool {
	if openedRecord == nil || openedRecord.GetDetailPresentation() == nil {
		return false
	}
	requestedAnchorIdentifier := recordAnchorIdentifierText(requestedAnchor)
	detail := openedRecord.GetDetailPresentation()
	if detail.GetDetailDisplayNameAnchor().GetRecordAnchorIdentifier() == requestedAnchorIdentifier {
		return true
	}
	if detail.GetBodyAnchor().GetRecordAnchorIdentifier() == requestedAnchorIdentifier {
		return true
	}
	for _, field := range detail.GetFieldsInDisplayOrder() {
		if field != nil &&
			field.GetFieldAnchor().GetRecordAnchorIdentifier() == requestedAnchorIdentifier {
			return true
		}
	}
	return false
}

func validateTrawlerOwnedRecordReference(
	registeredTrawler *identity.RegisteredTrawlerIdentity,
	recordReference *identity.CanonicalArchiveRecordReference,
	fieldName string,
) error {
	if !ValidSourceRef(registeredTrawler, recordReference) {
		return fmt.Errorf(
			"%s %q is outside the %q trawler namespace",
			fieldName,
			canonicalArchiveRecordReferenceText(recordReference),
			registeredTrawlerIdentityText(registeredTrawler),
		)
	}
	return nil
}

func ValidSourceRef(
	registeredTrawler *identity.RegisteredTrawlerIdentity,
	recordReference *identity.CanonicalArchiveRecordReference,
) bool {
	registeredTrawlerIdentity := registeredTrawlerIdentityText(registeredTrawler)
	if recordReference == nil {
		return false
	}
	recordReferenceText := canonicalArchiveRecordReferenceText(recordReference)
	if registeredTrawlerIdentity == "" ||
		recordReferenceText != recordReference.GetCanonicalArchiveRecordReference() {
		return false
	}
	prefix := registeredTrawlerIdentity + ":"
	return strings.HasPrefix(recordReferenceText, prefix) &&
		strings.TrimSpace(strings.TrimPrefix(recordReferenceText, prefix)) != ""
}

func registeredTrawlerIdentityText(
	registeredTrawler *identity.RegisteredTrawlerIdentity,
) string {
	if registeredTrawler == nil {
		return ""
	}
	return strings.TrimSpace(registeredTrawler.GetRegisteredTrawlerIdentity())
}

func canonicalArchiveRecordReferenceText(
	recordReference *identity.CanonicalArchiveRecordReference,
) string {
	if recordReference == nil {
		return ""
	}
	return strings.TrimSpace(recordReference.GetCanonicalArchiveRecordReference())
}

func canonicalArchiveRecordReferencesEqual(
	left *identity.CanonicalArchiveRecordReference,
	right *identity.CanonicalArchiveRecordReference,
) bool {
	leftText := canonicalArchiveRecordReferenceText(left)
	return leftText != "" && leftText == canonicalArchiveRecordReferenceText(right)
}

func recordAnchorIdentifierText(
	recordAnchor *identity.RecordAnchorIdentifier,
) string {
	if recordAnchor == nil {
		return ""
	}
	return strings.TrimSpace(recordAnchor.GetRecordAnchorIdentifier())
}

func recordAnchorIdentifiersEqual(
	left *identity.RecordAnchorIdentifier,
	right *identity.RecordAnchorIdentifier,
) bool {
	leftText := recordAnchorIdentifierText(left)
	return leftText != "" && leftText == recordAnchorIdentifierText(right)
}

func ValidAnchorID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' ||
			character == '_' ||
			character == '.' {
			continue
		}
		return false
	}
	return true
}
