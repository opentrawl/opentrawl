package openrecord

import (
	"fmt"
	"net/url"
	"strings"

	calendareventv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar_event/v1"
	conversationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation/v1"
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
	registeredTrawlerManifestIdentity := strings.TrimSpace(record.GetRegisteredTrawlerManifestIdentity())
	if registeredTrawlerManifestIdentity == "" {
		return fmt.Errorf("registered trawler manifest identity is empty")
	}
	canonicalOpenedRecordReference := strings.TrimSpace(record.GetCanonicalOpenedRecordReference())
	if err := validateTrawlerOwnedRecordReference(
		registeredTrawlerManifestIdentity,
		canonicalOpenedRecordReference,
		"canonical opened record reference",
	); err != nil {
		return err
	}
	switch typedOpenedRecord := record.GetTypedOpenedRecord().(type) {
	case *openv1.OpenRecord_OpenedMessageRecordWithConversationContext:
		return validateOpenedMessageRecordWithConversationContext(
			registeredTrawlerManifestIdentity,
			canonicalOpenedRecordReference,
			typedOpenedRecord.OpenedMessageRecordWithConversationContext,
		)
	case *openv1.OpenRecord_ConversationRecord:
		return validateConversationRecord(
			registeredTrawlerManifestIdentity,
			canonicalOpenedRecordReference,
			typedOpenedRecord.ConversationRecord,
		)
	case *openv1.OpenRecord_PersonRecord:
		return validatePersonRecord(
			registeredTrawlerManifestIdentity,
			canonicalOpenedRecordReference,
			typedOpenedRecord.PersonRecord,
		)
	case *openv1.OpenRecord_CalendarEventRecord:
		return validateCalendarEventRecord(
			registeredTrawlerManifestIdentity,
			canonicalOpenedRecordReference,
			typedOpenedRecord.CalendarEventRecord,
		)
	case *openv1.OpenRecord_TrawlerSpecificOpenedRecord:
		return validateTrawlerSpecificOpenedRecord(typedOpenedRecord.TrawlerSpecificOpenedRecord)
	default:
		return fmt.Errorf("open record has no typed record")
	}
}

func ValidateRequestedAnchor(record *openv1.OpenRecord, requestedAnchorIdentifier string) error {
	if err := Validate(record); err != nil {
		return err
	}
	requestedAnchorIdentifier = strings.TrimSpace(requestedAnchorIdentifier)
	if requestedAnchorIdentifier == "" {
		return fmt.Errorf("requested anchor identifier is empty")
	}
	switch typedOpenedRecord := record.GetTypedOpenedRecord().(type) {
	case *openv1.OpenRecord_OpenedMessageRecordWithConversationContext:
		if typedOpenedRecord.OpenedMessageRecordWithConversationContext.GetOpenedMessageRecordFixedAnchorIdentifier() != requestedAnchorIdentifier {
			return fmt.Errorf("opened message does not contain requested anchor %q", requestedAnchorIdentifier)
		}
	case *openv1.OpenRecord_PersonRecord:
		if !personRecordContainsAnchor(typedOpenedRecord.PersonRecord, requestedAnchorIdentifier) {
			return fmt.Errorf("person record does not contain requested anchor %q", requestedAnchorIdentifier)
		}
	case *openv1.OpenRecord_TrawlerSpecificOpenedRecord:
		if !trawlerSpecificOpenedRecordContainsAnchor(
			typedOpenedRecord.TrawlerSpecificOpenedRecord,
			requestedAnchorIdentifier,
		) {
			return fmt.Errorf("opened record does not contain requested anchor %q", requestedAnchorIdentifier)
		}
	default:
		return fmt.Errorf("opened record does not contain requested anchor %q", requestedAnchorIdentifier)
	}
	return nil
}

func validateOpenedMessageRecordWithConversationContext(
	registeredTrawlerManifestIdentity string,
	canonicalOpenedRecordReference string,
	openedMessage *messagev1.OpenedMessageRecordWithConversationContext,
) error {
	if openedMessage == nil {
		return fmt.Errorf("opened message record is missing")
	}
	if strings.TrimSpace(openedMessage.GetCanonicalOpenedMessageRecordReference()) != canonicalOpenedRecordReference {
		return fmt.Errorf("canonical opened message record reference does not match the opened record")
	}
	if err := validateTrawlerOwnedRecordReference(
		registeredTrawlerManifestIdentity,
		openedMessage.GetCanonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
		"canonical conversation record reference",
	); err != nil {
		return err
	}
	openedMessageCount := 0
	for _, messageRecord := range openedMessage.GetConversationContextMessageRecordsInDisplayOrder() {
		if messageRecord != nil &&
			strings.TrimSpace(messageRecord.GetCanonicalMessageRecordReferenceForGloballyRoutableTrawlLinkAssignment()) ==
				canonicalOpenedRecordReference {
			openedMessageCount++
		}
	}
	if openedMessageCount != 1 {
		return fmt.Errorf("opened message occurs %d times in conversation context", openedMessageCount)
	}
	return nil
}

func validateConversationRecord(
	registeredTrawlerManifestIdentity string,
	canonicalOpenedRecordReference string,
	conversationRecord *conversationv1.ConversationRecord,
) error {
	if conversationRecord == nil {
		return fmt.Errorf("conversation record is missing")
	}
	canonicalConversationRecordReference := strings.TrimSpace(
		conversationRecord.GetCanonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
	)
	if canonicalConversationRecordReference != canonicalOpenedRecordReference {
		return fmt.Errorf("canonical conversation record reference does not match the opened record")
	}
	return validateTrawlerOwnedRecordReference(
		registeredTrawlerManifestIdentity,
		canonicalConversationRecordReference,
		"canonical conversation record reference",
	)
}

func validatePersonRecord(
	registeredTrawlerManifestIdentity string,
	canonicalOpenedRecordReference string,
	personRecord *personv1.PersonRecord,
) error {
	if personRecord == nil {
		return fmt.Errorf("person record is missing")
	}
	canonicalPersonRecordReference := strings.TrimSpace(
		personRecord.GetCanonicalPersonRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
	)
	if canonicalPersonRecordReference != canonicalOpenedRecordReference {
		return fmt.Errorf("canonical person record reference does not match the opened record")
	}
	if err := validateTrawlerOwnedRecordReference(
		registeredTrawlerManifestIdentity,
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
	registeredTrawlerManifestIdentity string,
	canonicalOpenedRecordReference string,
	calendarEventRecord *calendareventv1.CalendarEventRecord,
) error {
	if calendarEventRecord == nil {
		return fmt.Errorf("calendar event record is missing")
	}
	canonicalCalendarEventRecordReference := strings.TrimSpace(
		calendarEventRecord.GetCanonicalCalendarEventRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
	)
	if canonicalCalendarEventRecordReference != canonicalOpenedRecordReference {
		return fmt.Errorf("canonical calendar event record reference does not match the opened record")
	}
	return validateTrawlerOwnedRecordReference(
		registeredTrawlerManifestIdentity,
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

func personRecordContainsAnchor(personRecord *personv1.PersonRecord, requestedAnchorIdentifier string) bool {
	if personRecord == nil {
		return false
	}
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
	requestedAnchorIdentifier string,
) bool {
	if openedRecord == nil || openedRecord.GetTrawlerSpecificOpenedRecordDetailPresentation() == nil {
		return false
	}
	detail := openedRecord.GetTrawlerSpecificOpenedRecordDetailPresentation()
	if detail.DetailDisplayNameFixedAnchorIdentifier != nil &&
		detail.GetDetailDisplayNameFixedAnchorIdentifier() == requestedAnchorIdentifier {
		return true
	}
	if detail.BodyFixedAnchorIdentifier != nil &&
		detail.GetBodyFixedAnchorIdentifier() == requestedAnchorIdentifier {
		return true
	}
	for _, field := range detail.GetFieldsInDisplayOrder() {
		if field != nil && field.FieldFixedAnchorIdentifier != nil &&
			field.GetFieldFixedAnchorIdentifier() == requestedAnchorIdentifier {
			return true
		}
	}
	return false
}

func validateTrawlerOwnedRecordReference(
	registeredTrawlerManifestIdentity string,
	recordReference string,
	fieldName string,
) error {
	if !ValidSourceRef(registeredTrawlerManifestIdentity, recordReference) {
		return fmt.Errorf(
			"%s %q is outside the %q trawler namespace",
			fieldName,
			recordReference,
			strings.TrimSpace(registeredTrawlerManifestIdentity),
		)
	}
	return nil
}

func ValidSourceRef(registeredTrawlerManifestIdentity string, recordReference string) bool {
	registeredTrawlerManifestIdentity = strings.TrimSpace(registeredTrawlerManifestIdentity)
	if registeredTrawlerManifestIdentity == "" || recordReference != strings.TrimSpace(recordReference) {
		return false
	}
	prefix := registeredTrawlerManifestIdentity + ":"
	return strings.HasPrefix(recordReference, prefix) &&
		strings.TrimSpace(strings.TrimPrefix(recordReference, prefix)) != ""
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
