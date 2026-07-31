package openrecord

import (
	"fmt"
	"net/url"
	"strings"

	calendareventv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar_event/v1"
	conversationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation/v1"
	identityv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity/v1"
	messagev1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
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

func Validate(record *openv1.OpenRecord) error {
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
	case *openv1.OpenRecord_OpenedMessageRecordWithConversationContext:
		return validateOpenedMessageRecordWithConversationContext(
			registeredTrawler,
			canonicalOpenedRecordReference,
			typedOpenedRecord.OpenedMessageRecordWithConversationContext,
		)
	case *openv1.OpenRecord_ConversationRecord:
		return validateConversationRecord(
			registeredTrawler,
			canonicalOpenedRecordReference,
			typedOpenedRecord.ConversationRecord,
		)
	case *openv1.OpenRecord_PersonRecord:
		return validatePersonRecord(
			registeredTrawler,
			canonicalOpenedRecordReference,
			typedOpenedRecord.PersonRecord,
		)
	case *openv1.OpenRecord_CalendarEventRecord:
		return validateCalendarEventRecord(
			registeredTrawler,
			canonicalOpenedRecordReference,
			typedOpenedRecord.CalendarEventRecord,
		)
	case *openv1.OpenRecord_TrawlerSpecificOpenedRecord:
		return validateTrawlerSpecificOpenedRecord(typedOpenedRecord.TrawlerSpecificOpenedRecord)
	default:
		return fmt.Errorf("open record has no typed record")
	}
}

func ValidateRequestedAnchor(
	record *openv1.OpenRecord,
	requestedAnchor *identityv1.RecordAnchorIdentifier,
) error {
	if err := Validate(record); err != nil {
		return err
	}
	requestedAnchorIdentifier := recordAnchorIdentifierText(requestedAnchor)
	if requestedAnchorIdentifier == "" {
		return fmt.Errorf("requested anchor identifier is empty")
	}
	switch typedOpenedRecord := record.GetTypedOpenedRecord().(type) {
	case *openv1.OpenRecord_OpenedMessageRecordWithConversationContext:
		if !recordAnchorIdentifiersEqual(
			typedOpenedRecord.OpenedMessageRecordWithConversationContext.GetOpenedMessageRecordAnchor(),
			requestedAnchor,
		) {
			return fmt.Errorf("opened message does not contain requested anchor %q", requestedAnchorIdentifier)
		}
	case *openv1.OpenRecord_PersonRecord:
		if !personRecordContainsAnchor(typedOpenedRecord.PersonRecord, requestedAnchor) {
			return fmt.Errorf("person record does not contain requested anchor %q", requestedAnchorIdentifier)
		}
	case *openv1.OpenRecord_TrawlerSpecificOpenedRecord:
		if !trawlerSpecificOpenedRecordContainsAnchor(
			typedOpenedRecord.TrawlerSpecificOpenedRecord,
			requestedAnchor,
		) {
			return fmt.Errorf("opened record does not contain requested anchor %q", requestedAnchorIdentifier)
		}
	default:
		return fmt.Errorf("opened record does not contain requested anchor %q", requestedAnchorIdentifier)
	}
	return nil
}

func validateOpenedMessageRecordWithConversationContext(
	registeredTrawler *identityv1.RegisteredTrawlerIdentity,
	canonicalOpenedRecordReference *identityv1.CanonicalArchiveRecordReference,
	openedMessage *messagev1.OpenedMessageRecordWithConversationContext,
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
	registeredTrawler *identityv1.RegisteredTrawlerIdentity,
	canonicalOpenedRecordReference *identityv1.CanonicalArchiveRecordReference,
	conversationRecord *conversationv1.ConversationRecord,
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
	registeredTrawler *identityv1.RegisteredTrawlerIdentity,
	canonicalOpenedRecordReference *identityv1.CanonicalArchiveRecordReference,
	personRecord *personv1.PersonRecord,
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
		if contactMethod.GetPersonContactMethodKind() == personv1.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_UNSPECIFIED {
			return fmt.Errorf("person contact method %d kind is unspecified", contactMethodIndex+1)
		}
		if strings.TrimSpace(contactMethod.GetPersonContactMethodDisplayValue()) == "" {
			return fmt.Errorf("person contact method %d display value is empty", contactMethodIndex+1)
		}
	}
	return nil
}

func validateCalendarEventRecord(
	registeredTrawler *identityv1.RegisteredTrawlerIdentity,
	canonicalOpenedRecordReference *identityv1.CanonicalArchiveRecordReference,
	calendarEventRecord *calendareventv1.CalendarEventRecord,
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

func validateTrawlerSpecificOpenedRecord(openedRecord *openv1.TrawlerSpecificOpenedRecord) error {
	if openedRecord == nil {
		return fmt.Errorf("trawler-specific opened record is missing")
	}
	typedOpenedRecord := openedRecord.GetTypedTrawlerSpecificOpenedRecord()
	if typedOpenedRecord == nil || strings.TrimSpace(typedOpenedRecord.GetTypeUrl()) == "" {
		return fmt.Errorf("typed trawler-specific opened record is missing")
	}
	if openedRecord.GetTrawlerSpecificOpenedRecordDetailPresentation() == nil {
		return fmt.Errorf("trawler-specific opened record detail presentation is missing")
	}
	return nil
}

func personRecordContainsAnchor(
	personRecord *personv1.PersonRecord,
	requestedAnchor *identityv1.RecordAnchorIdentifier,
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
	wantedContactMethodKind := personv1.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_UNSPECIFIED
	switch requestedAnchorIdentifier {
	case PersonEmailAddressAnchorID:
		wantedContactMethodKind = personv1.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_EMAIL_ADDRESS
	case PersonPhoneNumberAnchorID:
		wantedContactMethodKind = personv1.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_PHONE_NUMBER
	case PersonPostalAddressAnchorID:
		wantedContactMethodKind = personv1.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_POSTAL_ADDRESS
	case PersonAccountIdentifierAnchorID:
		wantedContactMethodKind = personv1.PersonContactMethodKind_PERSON_CONTACT_METHOD_KIND_ACCOUNT_IDENTIFIER
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

func trawlerSpecificOpenedRecordContainsAnchor(
	openedRecord *openv1.TrawlerSpecificOpenedRecord,
	requestedAnchor *identityv1.RecordAnchorIdentifier,
) bool {
	if openedRecord == nil || openedRecord.GetTrawlerSpecificOpenedRecordDetailPresentation() == nil {
		return false
	}
	requestedAnchorIdentifier := recordAnchorIdentifierText(requestedAnchor)
	detail := openedRecord.GetTrawlerSpecificOpenedRecordDetailPresentation()
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
	registeredTrawler *identityv1.RegisteredTrawlerIdentity,
	recordReference *identityv1.CanonicalArchiveRecordReference,
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
	registeredTrawler *identityv1.RegisteredTrawlerIdentity,
	recordReference *identityv1.CanonicalArchiveRecordReference,
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
	registeredTrawler *identityv1.RegisteredTrawlerIdentity,
) string {
	if registeredTrawler == nil {
		return ""
	}
	return strings.TrimSpace(registeredTrawler.GetRegisteredTrawlerIdentity())
}

func canonicalArchiveRecordReferenceText(
	recordReference *identityv1.CanonicalArchiveRecordReference,
) string {
	if recordReference == nil {
		return ""
	}
	return strings.TrimSpace(recordReference.GetCanonicalArchiveRecordReference())
}

func canonicalArchiveRecordReferencesEqual(
	left *identityv1.CanonicalArchiveRecordReference,
	right *identityv1.CanonicalArchiveRecordReference,
) bool {
	leftText := canonicalArchiveRecordReferenceText(left)
	return leftText != "" && leftText == canonicalArchiveRecordReferenceText(right)
}

func recordAnchorIdentifierText(
	recordAnchor *identityv1.RecordAnchorIdentifier,
) string {
	if recordAnchor == nil {
		return ""
	}
	return strings.TrimSpace(recordAnchor.GetRecordAnchorIdentifier())
}

func recordAnchorIdentifiersEqual(
	left *identityv1.RecordAnchorIdentifier,
	right *identityv1.RecordAnchorIdentifier,
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
