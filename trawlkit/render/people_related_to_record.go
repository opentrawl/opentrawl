package render

import (
	"strings"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

const (
	maximumPersonDisplayNamesPerRoleInSendersAndRecipients = 2
	maximumPersonDisplayNamesWithoutRoles                  = 3
	sendersToRecipientsSeparator                           = " → "
)

// formatSendersAndRecipients writes the who of a record the same way in every
// list, including a single conversation: "<sender> → me", or the names when the
// record has no sender.
func formatSendersAndRecipients(people []*person.PersonRelatedToArchiveRecord) string {
	senders := personDisplayNamesWithRole(people, person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER)
	recipients := personDisplayNamesWithRole(people, person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT)
	if len(recipients) > 0 {
		// A sender the source did not name is left blank: "→ me", never "me".
		return strings.TrimSpace(previewOfPersonDisplayNames(senders, maximumPersonDisplayNamesPerRoleInSendersAndRecipients) +
			sendersToRecipientsSeparator +
			previewOfPersonDisplayNames(recipients, maximumPersonDisplayNamesPerRoleInSendersAndRecipients))
	}
	displayNames := make([]string, 0, len(people))
	seenDisplayNames := make(map[string]struct{}, len(people))
	for _, person := range people {
		if person == nil {
			continue
		}
		displayName := strings.TrimSpace(person.GetPersonDisplayName())
		normalizedDisplayName := strings.ToLower(displayName)
		if displayName == "" {
			continue
		}
		if _, alreadyIncluded := seenDisplayNames[normalizedDisplayName]; alreadyIncluded {
			continue
		}
		seenDisplayNames[normalizedDisplayName] = struct{}{}
		displayNames = append(displayNames, displayName)
	}
	return previewOfPersonDisplayNames(displayNames, maximumPersonDisplayNamesWithoutRoles)
}

func previewOfPersonDisplayNames(displayNames []string, maximumDisplayNames int) string {
	if len(displayNames) <= maximumDisplayNames {
		return strings.Join(displayNames, ", ")
	}
	return strings.Join(displayNames[:maximumDisplayNames], ", ") +
		", +" +
		FormatInteger(int64(len(displayNames)-maximumDisplayNames))
}

func personDisplayNamesWithRole(
	people []*person.PersonRelatedToArchiveRecord,
	role person.PersonRoleInArchiveRecord,
) []string {
	displayNames := make([]string, 0, len(people))
	for _, person := range people {
		if person == nil || person.GetPersonRoleInArchiveRecord() != role {
			continue
		}
		if displayName := strings.TrimSpace(person.GetPersonDisplayName()); displayName != "" {
			displayNames = append(displayNames, displayName)
		}
	}
	return displayNames
}

// containerNamesThatAreNotPeople drops a conversation or container name that
// only repeats a person already shown, as a one-to-one chat named after the
// other person does.
func containerNamesThatAreNotPeople(
	containerNames []string,
	people []*person.PersonRelatedToArchiveRecord,
) []string {
	personDisplayNames := make(map[string]struct{}, len(people))
	for _, relatedPerson := range people {
		if displayName := strings.ToLower(strings.TrimSpace(relatedPerson.GetPersonDisplayName())); displayName != "" {
			personDisplayNames[displayName] = struct{}{}
		}
	}
	keptContainerNames := make([]string, 0, len(containerNames))
	for _, containerName := range containerNames {
		containerName = strings.TrimSpace(containerName)
		if containerName == "" {
			continue
		}
		if _, namesPerson := personDisplayNames[strings.ToLower(containerName)]; namesPerson {
			continue
		}
		keptContainerNames = append(keptContainerNames, containerName)
	}
	return keptContainerNames
}
